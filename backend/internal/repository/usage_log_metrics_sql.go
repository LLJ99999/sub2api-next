package repository

import (
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
)

// 用量派生指标的 SQL 表达式。定义必须与 usagestats.CacheHitRate / OutputTPS /
// UpstreamCost 一致；阈值直接引用 usagestats 常量，避免两处漂移。

func usageLogColumn(alias, name string) string {
	if alias == "" {
		return name
	}
	return alias + "." + name
}

// usageLogCacheHitRateSQL 单行缓存命中率；无提示 token 时为 NULL。
func usageLogCacheHitRateSQL(alias string) string {
	input := usageLogColumn(alias, "input_tokens")
	read := usageLogColumn(alias, "cache_read_tokens")
	write := usageLogColumn(alias, "cache_creation_tokens")
	return fmt.Sprintf(
		"(%[2]s::double precision / NULLIF(%[1]s + %[2]s + %[3]s, 0))",
		input, read, write,
	)
}

// usageLogAggregateCacheHitRateSQL 聚合缓存命中率：SUM(read) / SUM(input + read + write)。
// 注意不是逐行命中率的平均值，大请求权重更高，与前端汇总口径一致。
func usageLogAggregateCacheHitRateSQL(alias string) string {
	input := usageLogColumn(alias, "input_tokens")
	read := usageLogColumn(alias, "cache_read_tokens")
	write := usageLogColumn(alias, "cache_creation_tokens")
	return fmt.Sprintf(
		"(SUM(%[2]s)::double precision / NULLIF(SUM(%[1]s + %[2]s + %[3]s), 0))",
		input, read, write,
	)
}

// usageLogOutputTPSSQL 单行生成阶段输出 TPS；不满足条件时为 NULL。
func usageLogOutputTPSSQL(alias string) string {
	output := usageLogColumn(alias, "output_tokens")
	duration := usageLogColumn(alias, "duration_ms")
	firstToken := usageLogColumn(alias, "first_token_ms")
	imageCount := usageLogColumn(alias, "image_count")
	imageOutput := usageLogColumn(alias, "image_output_tokens")
	billingMode := usageLogColumn(alias, "billing_mode")
	generation := fmt.Sprintf("(%s - COALESCE(%s, 0))", duration, firstToken)
	return fmt.Sprintf(
		"(CASE WHEN %[1]s >= %[2]d AND %[3]s IS NOT NULL AND COALESCE(%[4]s, 0) >= 0 AND %[5]s >= %[6]d"+
			" AND COALESCE(%[7]s, 0) = 0 AND COALESCE(%[8]s, 0) = 0 AND COALESCE(%[9]s, '') <> 'video'"+
			" THEN %[1]s * 1000.0 / %[5]s END)",
		output, usagestats.OutputTPSMinOutputTokens,
		duration, firstToken,
		generation, usagestats.OutputTPSMinGenerationMs,
		imageCount, imageOutput, billingMode,
	)
}

// usageLogUpstreamCostSQL 单行上游成本：(account_stats_cost ?? total_cost) × (account_rate_multiplier ?? 1)。
func usageLogUpstreamCostSQL(alias string) string {
	return fmt.Sprintf(
		"(COALESCE(%s, %s) * COALESCE(%s, 1))",
		usageLogColumn(alias, "account_stats_cost"),
		usageLogColumn(alias, "total_cost"),
		usageLogColumn(alias, "account_rate_multiplier"),
	)
}
