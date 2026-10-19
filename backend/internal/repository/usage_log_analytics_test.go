//go:build unit

package repository

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/stretchr/testify/require"
)

var usageAnalyticsColumns = []string{
	"is_total", "dim_key", "dim_name", "dim_platform", "requests",
	"input_tokens", "output_tokens", "cache_creation_tokens", "cache_read_tokens",
	"cache_hit_rate", "ttft_p50", "ttft_p95", "tps_p50", "tps_p95", "revenue", "upstream_cost",
}

func TestUsageLogOrderByDerivedMetrics(t *testing.T) {
	tps := usageLogOrderBy(pagination.PaginationParams{SortBy: "output_tps", SortOrder: "desc"})
	require.Contains(t, tps, "output_tokens * 1000.0")
	require.Contains(t, tps, "COALESCE(first_token_ms, 0)")
	require.True(t, strings.HasSuffix(tps, "DESC NULLS LAST, id DESC"), tps)

	cache := usageLogOrderBy(pagination.PaginationParams{SortBy: "cache_hit_rate", SortOrder: "asc"})
	require.Contains(t, cache, "cache_read_tokens::double precision / NULLIF(input_tokens + cache_read_tokens + cache_creation_tokens, 0)")
	require.True(t, strings.HasSuffix(cache, "ASC NULLS LAST, id ASC"), cache)

	// 其他排序键行为不变。
	require.Equal(t, "created_at DESC, id DESC", usageLogOrderBy(pagination.PaginationParams{SortBy: "created_at", SortOrder: "desc"}))
	require.Equal(t, "id DESC", usageLogOrderBy(pagination.PaginationParams{SortBy: "unknown; DROP TABLE x", SortOrder: "desc"}))
}

func TestUsageLogMetricSQLUsesSharedThresholds(t *testing.T) {
	expr := usageLogOutputTPSSQL("ul")
	require.Contains(t, expr, "ul.output_tokens >= 20")
	require.Contains(t, expr, "(ul.duration_ms - COALESCE(ul.first_token_ms, 0)) >= 100")
	require.Contains(t, expr, "COALESCE(ul.billing_mode, '') <> 'video'")
	require.Equal(t, "(COALESCE(ul.account_stats_cost, ul.total_cost) * COALESCE(ul.account_rate_multiplier, 1))", usageLogUpstreamCostSQL("ul"))
	require.Equal(t,
		"(SUM(cache_read_tokens)::double precision / NULLIF(SUM(input_tokens + cache_read_tokens + cache_creation_tokens), 0))",
		usageLogAggregateCacheHitRateSQL(""))
}

func TestBuildUsageAnalyticsQueryByDimension(t *testing.T) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 0, 7)

	q, args := buildUsageAnalyticsQuery(usagestats.UsageAnalyticsFilters{
		Dimension: usagestats.UsageAnalyticsDimensionAccount, StartTime: start, EndTime: end, GroupID: 3, Model: "claude-sonnet-4-5",
	})
	require.Equal(t, []any{start, end, int64(3), "claude-sonnet-4-5"}, args)
	require.Contains(t, q, "ul.account_id::text AS dim_key")
	require.Contains(t, q, "ul.group_id = $3")
	require.Contains(t, q, "COALESCE(NULLIF(TRIM(ul.requested_model), ''), ul.model) = $4")
	require.Contains(t, q, "GROUP BY GROUPING SETS ((dim_key), ())")
	require.Contains(t, q, "percentile_cont(0.95) WITHIN GROUP (ORDER BY first_token_ms)")
	require.Contains(t, q, "percentile_cont(0.5) WITHIN GROUP (ORDER BY output_tps)")
	require.Contains(t, q, "LEFT JOIN accounts a")

	q, args = buildUsageAnalyticsQuery(usagestats.UsageAnalyticsFilters{
		Dimension: usagestats.UsageAnalyticsDimensionGroup, StartTime: start, EndTime: end, AccountID: 9,
	})
	require.Equal(t, []any{start, end, int64(9)}, args)
	require.Contains(t, q, "COALESCE(ul.group_id, 0)::text AS dim_key")
	require.Contains(t, q, "LEFT JOIN groups g")

	q, _ = buildUsageAnalyticsQuery(usagestats.UsageAnalyticsFilters{
		Dimension: usagestats.UsageAnalyticsDimensionModel, StartTime: start, EndTime: end,
	})
	require.Contains(t, q, "COALESCE(NULLIF(TRIM(ul.requested_model), ''), ul.model) AS dim_key")
	require.NotContains(t, q, "LEFT JOIN")

	eq, eargs := buildUsageAnalyticsErrorQuery(usagestats.UsageAnalyticsFilters{
		Dimension: usagestats.UsageAnalyticsDimensionAccount, StartTime: start, EndTime: end, GroupID: 3,
	})
	require.Equal(t, []any{start, end, int64(3)}, eargs)
	require.Contains(t, eq, "is_count_tokens = FALSE")
	require.Contains(t, eq, "COALESCE(metric_errors.status_code, 0) >= 400")
	require.Contains(t, eq, "GROUP BY GROUPING SETS ((metric_errors.account_id::text), ())")
}

func TestGetUsageAnalyticsMergesErrorsAndComputesMargin(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := newUsageLogRepositoryWithSQL(nil, db)
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 0, 1)

	mock.ExpectQuery(regexp.QuoteMeta("GROUP BY GROUPING SETS ((dim_key), ())")).
		WithArgs(start, end).
		WillReturnRows(sqlmock.NewRows(usageAnalyticsColumns).
			AddRow(1, "", "", "", int64(30), int64(1000), int64(500), int64(100), int64(900), 0.45, 800.0, 2500.0, 40.0, 90.0, 12.0, 9.0).
			AddRow(0, "7", "acc-a", "anthropic", int64(20), int64(600), int64(300), int64(100), int64(900), 0.5625, 700.0, 2000.0, 45.0, 95.0, 10.0, 6.0).
			AddRow(0, "8", "acc-b", "openai", int64(10), int64(400), int64(200), int64(0), int64(0), nil, nil, nil, nil, nil, 2.0, 3.0))
	mock.ExpectQuery(regexp.QuoteMeta("FROM ops_error_logs current_error")).
		WithArgs(start, end).
		WillReturnRows(sqlmock.NewRows([]string{"is_total", "dim_key", "error_count"}).
			AddRow(1, "", int64(5)).
			AddRow(0, "7", int64(2)).
			AddRow(0, "", int64(3)))

	res, err := repo.GetUsageAnalytics(context.Background(), usagestats.UsageAnalyticsFilters{
		Dimension: "account", StartTime: start, EndTime: end,
	})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())

	require.True(t, res.ErrorCountAvailable)
	require.False(t, res.Truncated)
	require.Equal(t, int64(30), res.Totals.Requests)
	require.Equal(t, int64(5), *res.Totals.ErrorCount)
	require.InDelta(t, 3.0, res.Totals.GrossMargin, 1e-9)
	require.InDelta(t, 0.25, *res.Totals.MarginPct, 1e-9)

	require.Len(t, res.Rows, 2)
	a := res.Rows[0]
	require.Equal(t, int64(7), *a.ID)
	require.Equal(t, "acc-a", a.Name)
	require.Equal(t, "anthropic", a.Platform)
	require.Equal(t, int64(2), *a.ErrorCount)
	require.Equal(t, int64(1900), a.TotalTokens)
	require.InDelta(t, 4.0, a.GrossMargin, 1e-9)
	require.InDelta(t, 0.4, *a.MarginPct, 1e-9)

	b := res.Rows[1]
	require.Equal(t, int64(0), *b.ErrorCount)
	require.Nil(t, b.CacheHitRate)
	require.Nil(t, b.OutputTPSP50)
	require.InDelta(t, -1.0, b.GrossMargin, 1e-9)
	require.InDelta(t, -0.5, *b.MarginPct, 1e-9)
}

func TestGetUsageAnalyticsToleratesErrorQueryFailureAndTruncates(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := newUsageLogRepositoryWithSQL(nil, db)
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 0, 1)

	mock.ExpectQuery(regexp.QuoteMeta("GROUP BY GROUPING SETS ((dim_key), ())")).
		WillReturnRows(sqlmock.NewRows(usageAnalyticsColumns).
			AddRow(1, "", "", "", int64(3), int64(0), int64(0), int64(0), int64(0), nil, nil, nil, nil, nil, 0.0, 0.0).
			AddRow(0, "m1", "m1", "", int64(2), int64(0), int64(0), int64(0), int64(0), nil, nil, nil, nil, nil, 0.0, 0.0).
			AddRow(0, "m2", "m2", "", int64(1), int64(0), int64(0), int64(0), int64(0), nil, nil, nil, nil, nil, 0.0, 0.0))
	mock.ExpectQuery(regexp.QuoteMeta("FROM ops_error_logs current_error")).
		WillReturnError(errors.New("relation does not exist"))

	res, err := repo.GetUsageAnalytics(context.Background(), usagestats.UsageAnalyticsFilters{
		Dimension: "model", StartTime: start, EndTime: end, Limit: 1,
	})
	require.NoError(t, err)
	require.False(t, res.ErrorCountAvailable)
	require.True(t, res.Truncated)
	require.Len(t, res.Rows, 1)
	require.Nil(t, res.Rows[0].ID, "model dimension has no numeric id")
	require.Nil(t, res.Rows[0].ErrorCount)
	require.Nil(t, res.Totals.MarginPct, "zero revenue has no margin percentage")
}

func TestGetUsageAnalyticsRejectsUnknownDimension(t *testing.T) {
	db, _ := newSQLMock(t)
	repo := newUsageLogRepositoryWithSQL(nil, db)
	_, err := repo.GetUsageAnalytics(context.Background(), usagestats.UsageAnalyticsFilters{Dimension: "user"})
	require.Error(t, err)
}
