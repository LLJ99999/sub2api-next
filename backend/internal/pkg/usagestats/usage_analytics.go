package usagestats

import (
	"strings"
	"time"
)

// 用量聚合分析（按上游账号 / 分组 / 模型）的维度与结果结构。

const (
	UsageAnalyticsDimensionAccount = "account"
	UsageAnalyticsDimensionGroup   = "group"
	UsageAnalyticsDimensionModel   = "model"

	// UsageAnalyticsMaxRange 聚合查询允许的最大时间跨度。
	// 聚合需要扫描范围内的全部 usage_logs 行（percentile_cont 无法增量计算），必须限幅。
	UsageAnalyticsMaxRange = 93 * 24 * time.Hour
	// UsageAnalyticsDefaultLimit / UsageAnalyticsMaxLimit 返回行数（按收入降序截断，合计行不受影响）。
	UsageAnalyticsDefaultLimit = 200
	UsageAnalyticsMaxLimit     = 1000
)

// NormalizeUsageAnalyticsDimension 返回规范化维度；非法值返回 ok=false。
func NormalizeUsageAnalyticsDimension(dimension string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(dimension)) {
	case "", UsageAnalyticsDimensionAccount:
		return UsageAnalyticsDimensionAccount, true
	case UsageAnalyticsDimensionGroup:
		return UsageAnalyticsDimensionGroup, true
	case UsageAnalyticsDimensionModel:
		return UsageAnalyticsDimensionModel, true
	default:
		return "", false
	}
}

// UsageAnalyticsFilters 聚合查询条件。StartTime/EndTime 为半开区间 [start, end)。
type UsageAnalyticsFilters struct {
	Dimension string
	StartTime time.Time
	EndTime   time.Time
	GroupID   int64
	AccountID int64
	// Model 按请求模型（requested_model，缺省回退 model）过滤。
	Model string
	Limit int
}

// UsageAnalyticsRow 一个维度值（或合计）的聚合结果。
type UsageAnalyticsRow struct {
	// Key 维度值：账号 ID / 分组 ID（无分组为 "0"）/ 请求模型名；合计行为空。
	Key string `json:"key"`
	// ID 账号或分组 ID；模型维度与合计行为 nil。
	ID *int64 `json:"id,omitempty"`
	// Name 账号名 / 分组名 / 模型名。
	Name string `json:"name"`
	// Platform 账号平台（仅账号维度）。
	Platform string `json:"platform,omitempty"`

	Requests int64 `json:"requests"`
	// ErrorCount 来自 ops_error_logs（状态码 >= 400，按请求去重）；查询失败或不可用时为 nil。
	ErrorCount *int64 `json:"error_count"`

	InputTokens         int64 `json:"input_tokens"`
	OutputTokens        int64 `json:"output_tokens"`
	CacheCreationTokens int64 `json:"cache_creation_tokens"`
	CacheReadTokens     int64 `json:"cache_read_tokens"`
	TotalTokens         int64 `json:"total_tokens"`

	// CacheHitRate = SUM(cache_read) / SUM(input + cache_read + cache_creation)，0~1。
	CacheHitRate *float64 `json:"cache_hit_rate"`

	FirstTokenP50Ms *float64 `json:"first_token_p50_ms"`
	FirstTokenP95Ms *float64 `json:"first_token_p95_ms"`
	OutputTPSP50    *float64 `json:"output_tps_p50"`
	OutputTPSP95    *float64 `json:"output_tps_p95"`

	// Revenue 向用户实际收取的费用 SUM(actual_cost)。
	Revenue float64 `json:"revenue"`
	// UpstreamCost SUM((account_stats_cost ?? total_cost) × (account_rate_multiplier ?? 1))。
	UpstreamCost float64 `json:"upstream_cost"`
	// GrossMargin = Revenue - UpstreamCost。
	GrossMargin float64 `json:"gross_margin"`
	// MarginPct = GrossMargin / Revenue（0~1，可为负）；Revenue <= 0 时为 nil。
	MarginPct *float64 `json:"margin_pct"`
}

// FinalizeMargin 根据 Revenue / UpstreamCost 填充毛利字段。
func (r *UsageAnalyticsRow) FinalizeMargin() {
	margin, pct, ok := GrossMargin(r.Revenue, r.UpstreamCost)
	r.GrossMargin = margin
	if ok {
		r.MarginPct = &pct
	} else {
		r.MarginPct = nil
	}
}

// UsageAnalyticsResult 聚合查询结果。
type UsageAnalyticsResult struct {
	Dimension string              `json:"dimension"`
	StartTime time.Time           `json:"start_time"`
	EndTime   time.Time           `json:"end_time"`
	Rows      []UsageAnalyticsRow `json:"rows"`
	Totals    UsageAnalyticsRow   `json:"totals"`
	// Truncated 为 true 表示行数超过 limit，Rows 只保留收入最高的前 limit 行（Totals 仍为全量）。
	Truncated bool `json:"truncated"`
	// ErrorCountAvailable 为 false 表示错误数查询失败，各行 ErrorCount 均为 nil。
	ErrorCountAvailable bool `json:"error_count_available"`
}
