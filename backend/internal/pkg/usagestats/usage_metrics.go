package usagestats

import (
	"math"
	"strings"
)

// 用量记录派生指标（缓存命中率、输出 TPS、上游成本、毛利）的唯一定义。
//
// 前端 frontend/src/utils/usageCacheRate.ts、frontend/src/utils/usageTps.ts 与
// repository 中的 SQL 表达式（usage_log_metrics_sql.go）必须与这里保持一致。
// 修改任何阈值时请同步三处并更新 docs/usage-analytics.md。

const (
	// OutputTPSMinOutputTokens 输出 token 少于该值时不计算 TPS：
	// 极短输出（中断流的占位 token、单个工具调用标记）没有吞吐意义。
	OutputTPSMinOutputTokens = 20
	// OutputTPSMinGenerationMs 生成窗口（duration_ms - first_token_ms）短于该值时不计算 TPS。
	// 上游把整段输出缓冲到最后一刻才下发时，生成窗口只有 1~10ms，
	// 直接相除会得到 7 万~10 万 t/s 的虚假尖峰；这类记录没有可信的生成速度。
	OutputTPSMinGenerationMs = 100
)

// CacheHitRate 计算单条或聚合记录的提示缓存命中率（0~1）。
//
// usage_logs 中三类提示 token 是互斥桶：
//   - input_tokens：未命中缓存的普通输入；
//   - cache_read_tokens：缓存命中；
//   - cache_creation_tokens：缓存写入。
//
// Anthropic 协议的 input_tokens 本身不含缓存 token；OpenAI 协议的 prompt_tokens /
// input_tokens 含 cached_tokens，网关在落库前已减去缓存读写（见
// service.OpenAIGatewayService 记录用量处），Gemini 的 promptTokenCount 同样在转换时减去
// cachedContentTokenCount。因此统一公式为：
//
//	cache_read / (input + cache_read + cache_creation)
//
// 分母为 0（无提示 token，如纯图片请求）或任一桶为负时返回 ok=false。
func CacheHitRate(inputTokens, cacheReadTokens, cacheCreationTokens int64) (rate float64, ok bool) {
	if inputTokens < 0 || cacheReadTokens < 0 || cacheCreationTokens < 0 {
		return 0, false
	}
	denominator := inputTokens + cacheReadTokens + cacheCreationTokens
	if denominator <= 0 {
		return 0, false
	}
	rate = float64(cacheReadTokens) / float64(denominator)
	if math.IsNaN(rate) || math.IsInf(rate, 0) {
		return 0, false
	}
	return rate, true
}

// OutputTPSInput 计算输出 TPS 所需的单条记录字段。
type OutputTPSInput struct {
	OutputTokens int64
	DurationMs   *int64
	FirstTokenMs *int64
	// IsMedia 为 true 表示图片/视频生成请求：输出 token 不代表文本生成速度，不计算 TPS。
	IsMedia bool
}

// OutputTPS 计算生成阶段的输出吞吐（tokens/s）：
//
//	output_tokens / ((duration_ms - first_token_ms) / 1000)
//
// first_token_ms 缺失（非流式请求）时按 0 处理，即用总耗时作为生成窗口。
// 以下情况返回 ok=false，由调用方显示占位符：
//   - 图片/视频请求；
//   - output_tokens < OutputTPSMinOutputTokens；
//   - duration_ms 缺失，或生成窗口 < OutputTPSMinGenerationMs（含 <= 0）。
func OutputTPS(in OutputTPSInput) (tps float64, ok bool) {
	if in.IsMedia || in.OutputTokens < OutputTPSMinOutputTokens || in.DurationMs == nil {
		return 0, false
	}
	var firstToken int64
	if in.FirstTokenMs != nil {
		firstToken = *in.FirstTokenMs
	}
	if firstToken < 0 {
		return 0, false
	}
	generationMs := *in.DurationMs - firstToken
	if generationMs <= 0 || generationMs < OutputTPSMinGenerationMs {
		return 0, false
	}
	tps = float64(in.OutputTokens) * 1000 / float64(generationMs)
	if math.IsNaN(tps) || math.IsInf(tps, 0) {
		return 0, false
	}
	return tps, true
}

// UpstreamCost 计算上游成本：(account_stats_cost ?? total_cost) × (account_rate_multiplier ?? 1)。
// account_stats_cost 是账号统计定价的预计算值；为空时退回标准费用 total_cost。
func UpstreamCost(totalCost float64, accountStatsCost, accountRateMultiplier *float64) float64 {
	base := totalCost
	if accountStatsCost != nil {
		base = *accountStatsCost
	}
	multiplier := 1.0
	if accountRateMultiplier != nil {
		multiplier = *accountRateMultiplier
	}
	cost := base * multiplier
	if math.IsNaN(cost) || math.IsInf(cost, 0) {
		return 0
	}
	return cost
}

// GrossMargin 返回毛利（revenue - upstreamCost）与毛利率（毛利 / revenue）。
// revenue <= 0 时毛利率无意义，marginPctOK=false。
func GrossMargin(revenue, upstreamCost float64) (margin float64, marginPct float64, marginPctOK bool) {
	margin = revenue - upstreamCost
	if revenue <= 0 {
		return margin, 0, false
	}
	return margin, margin / revenue, true
}

// 用量明细的派生指标排序键。按表达式排序无法走索引，调用方必须同时限定时间范围。
const (
	UsageLogSortOutputTPS    = "output_tps"
	UsageLogSortCacheHitRate = "cache_hit_rate"
)

// IsDerivedUsageLogSort 判断排序键是否为需要限定时间范围的派生指标。
func IsDerivedUsageLogSort(sortBy string) bool {
	switch strings.ToLower(strings.TrimSpace(sortBy)) {
	case UsageLogSortOutputTPS, UsageLogSortCacheHitRate:
		return true
	default:
		return false
	}
}
