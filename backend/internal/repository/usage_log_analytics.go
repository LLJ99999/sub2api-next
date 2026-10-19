package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"go.uber.org/zap"
)

// usageAnalyticsDimension 描述一个聚合维度在 usage_logs / ops_error_logs 上的取值表达式。
type usageAnalyticsDimension struct {
	// usageExpr usage_logs（别名 ul）上的维度表达式，结果转为 text 作为 key。
	usageExpr string
	// errorExpr ops_error_logs（DISTINCT 子查询别名 metric_errors）上的维度表达式。
	errorExpr string
	// nameJoin 聚合后补充名称的 LEFT JOIN 与选择列；模型维度无需关联。
	nameJoin   string
	nameSelect string
	platform   string
	hasID      bool
}

func resolveUsageAnalyticsDimension(dimension string) usageAnalyticsDimension {
	switch dimension {
	case usagestats.UsageAnalyticsDimensionGroup:
		return usageAnalyticsDimension{
			usageExpr:  "COALESCE(ul.group_id, 0)::text",
			errorExpr:  "COALESCE(metric_errors.group_id, 0)::text",
			nameJoin:   "LEFT JOIN groups g ON g.id = agg.dim_key::bigint",
			nameSelect: "COALESCE(g.name, '')",
			platform:   "''",
			hasID:      true,
		}
	case usagestats.UsageAnalyticsDimensionModel:
		return usageAnalyticsDimension{
			usageExpr:  resolveModelDimensionExpressionWithAlias(usagestats.ModelSourceRequested, "ul"),
			errorExpr:  "COALESCE(NULLIF(TRIM(metric_errors.model), ''), '')",
			nameSelect: "agg.dim_key",
			platform:   "''",
		}
	default:
		return usageAnalyticsDimension{
			usageExpr:  "ul.account_id::text",
			errorExpr:  "metric_errors.account_id::text",
			nameJoin:   "LEFT JOIN accounts a ON a.id = agg.dim_key::bigint",
			nameSelect: "COALESCE(a.name, '')",
			platform:   "COALESCE(a.platform, '')",
			hasID:      true,
		}
	}
}

// buildUsageAnalyticsQuery 生成按维度聚合 usage_logs 的 SQL。
//
// GROUPING SETS ((dim), ()) 一次扫描同时得到分组行与合计行；percentile_cont
// 自动忽略 NULL（无首字时间 / TPS 不适用的记录不参与分位数）。
func buildUsageAnalyticsQuery(filters usagestats.UsageAnalyticsFilters) (string, []any) {
	dim := resolveUsageAnalyticsDimension(filters.Dimension)
	conditions := []string{"ul.created_at >= $1", "ul.created_at < $2"}
	args := []any{filters.StartTime, filters.EndTime}
	if filters.GroupID > 0 {
		args = append(args, filters.GroupID)
		conditions = append(conditions, fmt.Sprintf("ul.group_id = $%d", len(args)))
	}
	if filters.AccountID > 0 {
		args = append(args, filters.AccountID)
		conditions = append(conditions, fmt.Sprintf("ul.account_id = $%d", len(args)))
	}
	if model := strings.TrimSpace(filters.Model); model != "" {
		args = append(args, model)
		conditions = append(conditions, fmt.Sprintf("%s = $%d",
			resolveModelDimensionExpressionWithAlias(usagestats.ModelSourceRequested, "ul"), len(args)))
	}

	query := fmt.Sprintf(`
WITH scoped AS (
	SELECT
		%[1]s AS dim_key,
		ul.input_tokens,
		ul.output_tokens,
		ul.cache_creation_tokens,
		ul.cache_read_tokens,
		ul.first_token_ms,
		%[2]s AS output_tps,
		ul.actual_cost,
		%[3]s AS upstream_cost
	FROM usage_logs ul
	WHERE %[4]s
),
agg AS (
	SELECT
		GROUPING(dim_key) AS is_total,
		dim_key,
		COUNT(*) AS requests,
		COALESCE(SUM(input_tokens), 0) AS input_tokens,
		COALESCE(SUM(output_tokens), 0) AS output_tokens,
		COALESCE(SUM(cache_creation_tokens), 0) AS cache_creation_tokens,
		COALESCE(SUM(cache_read_tokens), 0) AS cache_read_tokens,
		%[5]s AS cache_hit_rate,
		percentile_cont(0.5) WITHIN GROUP (ORDER BY first_token_ms) AS ttft_p50,
		percentile_cont(0.95) WITHIN GROUP (ORDER BY first_token_ms) AS ttft_p95,
		percentile_cont(0.5) WITHIN GROUP (ORDER BY output_tps) AS tps_p50,
		percentile_cont(0.95) WITHIN GROUP (ORDER BY output_tps) AS tps_p95,
		COALESCE(SUM(actual_cost), 0) AS revenue,
		COALESCE(SUM(upstream_cost), 0) AS upstream_cost
	FROM scoped
	GROUP BY GROUPING SETS ((dim_key), ())
)
SELECT
	agg.is_total,
	COALESCE(agg.dim_key, '') AS dim_key,
	%[6]s AS dim_name,
	%[7]s AS dim_platform,
	agg.requests,
	agg.input_tokens,
	agg.output_tokens,
	agg.cache_creation_tokens,
	agg.cache_read_tokens,
	agg.cache_hit_rate,
	agg.ttft_p50,
	agg.ttft_p95,
	agg.tps_p50,
	agg.tps_p95,
	agg.revenue,
	agg.upstream_cost
FROM agg
%[8]s
ORDER BY agg.is_total DESC, agg.revenue DESC, agg.requests DESC, dim_key ASC`,
		dim.usageExpr,
		usageLogOutputTPSSQL("ul"),
		usageLogUpstreamCostSQL("ul"),
		strings.Join(conditions, " AND "),
		usageLogAggregateCacheHitRateSQL(""),
		dimNameSelectForTotals(dim),
		dimPlatformSelectForTotals(dim),
		dim.nameJoin,
	)
	return query, args
}

// 合计行 dim_key 为 NULL，名称列必须容忍 NULL 关联（LEFT JOIN 天然返回 NULL）。
func dimNameSelectForTotals(dim usageAnalyticsDimension) string {
	return "CASE WHEN agg.is_total = 1 THEN '' ELSE " + dim.nameSelect + " END"
}

func dimPlatformSelectForTotals(dim usageAnalyticsDimension) string {
	return "CASE WHEN agg.is_total = 1 THEN '' ELSE " + dim.platform + " END"
}

// buildUsageAnalyticsErrorQuery 生成按维度统计错误数的 SQL（复用运维面板按请求去重的口径）。
func buildUsageAnalyticsErrorQuery(filters usagestats.UsageAnalyticsFilters) (string, []any) {
	dim := resolveUsageAnalyticsDimension(filters.Dimension)
	clauses := []string{"created_at >= $1", "created_at < $2", "is_count_tokens = FALSE"}
	args := []any{filters.StartTime, filters.EndTime}
	if filters.GroupID > 0 {
		args = append(args, filters.GroupID)
		clauses = append(clauses, fmt.Sprintf("group_id = $%d", len(args)))
	}
	if filters.AccountID > 0 {
		args = append(args, filters.AccountID)
		clauses = append(clauses, fmt.Sprintf("account_id = $%d", len(args)))
	}
	if model := strings.TrimSpace(filters.Model); model != "" {
		args = append(args, model)
		clauses = append(clauses, fmt.Sprintf("model = $%d", len(args)))
	}
	where := "WHERE " + strings.Join(clauses, " AND ")
	query := fmt.Sprintf(`
SELECT
	GROUPING(%[1]s) AS is_total,
	COALESCE(%[1]s, '') AS dim_key,
	COUNT(*) AS error_count
FROM %[2]s
WHERE COALESCE(metric_errors.status_code, 0) >= 400
GROUP BY GROUPING SETS ((%[1]s), ())`, dim.errorExpr, opsMetricErrorRowsSQL(where))
	return query, args
}

// GetUsageAnalytics 按上游账号 / 分组 / 模型聚合用量、延迟分位数、收入与上游成本。
func (r *usageLogRepository) GetUsageAnalytics(ctx context.Context, filters usagestats.UsageAnalyticsFilters) (*usagestats.UsageAnalyticsResult, error) {
	dimension, ok := usagestats.NormalizeUsageAnalyticsDimension(filters.Dimension)
	if !ok {
		return nil, fmt.Errorf("invalid usage analytics dimension %q", filters.Dimension)
	}
	filters.Dimension = dimension
	limit := filters.Limit
	if limit <= 0 {
		limit = usagestats.UsageAnalyticsDefaultLimit
	}
	if limit > usagestats.UsageAnalyticsMaxLimit {
		limit = usagestats.UsageAnalyticsMaxLimit
	}

	query, args := buildUsageAnalyticsQuery(filters)
	rows, err := r.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query usage analytics: %w", err)
	}
	result := &usagestats.UsageAnalyticsResult{
		Dimension: dimension,
		StartTime: filters.StartTime,
		EndTime:   filters.EndTime,
		Rows:      make([]usagestats.UsageAnalyticsRow, 0),
	}
	hasID := resolveUsageAnalyticsDimension(dimension).hasID
	if err := scanUsageAnalyticsRows(rows, hasID, result); err != nil {
		return nil, err
	}

	errorCounts, errorTotal, errErr := r.queryUsageAnalyticsErrorCounts(ctx, filters)
	if errErr != nil {
		// 错误数是辅助指标：ops_error_logs 不可用时仍返回用量聚合。
		logger.L().Warn("usage analytics: error count query failed", zap.String("dimension", dimension), zap.Error(errErr))
	} else {
		result.ErrorCountAvailable = true
		applyUsageAnalyticsErrorCounts(result, errorCounts, errorTotal)
	}

	if len(result.Rows) > limit {
		result.Rows = result.Rows[:limit]
		result.Truncated = true
	}
	return result, nil
}

func scanUsageAnalyticsRows(rows *sql.Rows, hasID bool, result *usagestats.UsageAnalyticsResult) (err error) {
	defer func() {
		if closeErr := rows.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}()
	for rows.Next() {
		var (
			isTotal                          int
			row                              usagestats.UsageAnalyticsRow
			cacheRate, p50, p95, tps50, tps95 sql.NullFloat64
		)
		if err = rows.Scan(
			&isTotal,
			&row.Key,
			&row.Name,
			&row.Platform,
			&row.Requests,
			&row.InputTokens,
			&row.OutputTokens,
			&row.CacheCreationTokens,
			&row.CacheReadTokens,
			&cacheRate,
			&p50,
			&p95,
			&tps50,
			&tps95,
			&row.Revenue,
			&row.UpstreamCost,
		); err != nil {
			return fmt.Errorf("scan usage analytics: %w", err)
		}
		row.TotalTokens = row.InputTokens + row.OutputTokens + row.CacheCreationTokens + row.CacheReadTokens
		row.CacheHitRate = nullFloatPtr(cacheRate)
		row.FirstTokenP50Ms = nullFloatPtr(p50)
		row.FirstTokenP95Ms = nullFloatPtr(p95)
		row.OutputTPSP50 = nullFloatPtr(tps50)
		row.OutputTPSP95 = nullFloatPtr(tps95)
		row.FinalizeMargin()
		if isTotal == 1 {
			row.Key = ""
			row.Name = ""
			row.Platform = ""
			result.Totals = row
			continue
		}
		if hasID {
			if id, parseErr := strconv.ParseInt(row.Key, 10, 64); parseErr == nil {
				row.ID = &id
			}
		}
		result.Rows = append(result.Rows, row)
	}
	return rows.Err()
}

func (r *usageLogRepository) queryUsageAnalyticsErrorCounts(ctx context.Context, filters usagestats.UsageAnalyticsFilters) (counts map[string]int64, total int64, err error) {
	query, args := buildUsageAnalyticsErrorQuery(filters)
	rows, err := r.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}()
	counts = make(map[string]int64)
	for rows.Next() {
		var (
			isTotal int
			key     string
			count   int64
		)
		if err = rows.Scan(&isTotal, &key, &count); err != nil {
			return nil, 0, err
		}
		if isTotal == 1 {
			total = count
			continue
		}
		counts[key] += count
	}
	return counts, total, rows.Err()
}

// applyUsageAnalyticsErrorCounts 把错误数合并进用量行。只出现错误、没有成功用量的维度值
// 不会新增行（无收入与成本，可在运维错误页查看），但计入合计行。
func applyUsageAnalyticsErrorCounts(result *usagestats.UsageAnalyticsResult, counts map[string]int64, total int64) {
	for i := range result.Rows {
		count := counts[result.Rows[i].Key]
		result.Rows[i].ErrorCount = &count
	}
	totalCount := total
	result.Totals.ErrorCount = &totalCount
}

func nullFloatPtr(v sql.NullFloat64) *float64 {
	if !v.Valid {
		return nil
	}
	f := v.Float64
	return &f
}
