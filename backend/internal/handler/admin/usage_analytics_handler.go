package admin

import (
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"

	"github.com/gin-gonic/gin"
)

// Analytics 按上游账号 / 分组 / 模型聚合用量与毛利。
// GET /api/v1/admin/usage/analytics
//
// Query:
//   - dimension: account（默认）| group | model
//   - start_date, end_date: YYYY-MM-DD（必填，按 timezone 解析，end_date 含当天；跨度 <= 93 天）
//   - timezone: IANA 时区，默认服务器时区
//   - group_id, account_id: 可选过滤
//   - model: 可选，按请求模型过滤
//   - limit: 返回行数上限，默认 200，最大 1000（合计行始终为全量）
func (h *UsageHandler) Analytics(c *gin.Context) {
	dimension, ok := usagestats.NormalizeUsageAnalyticsDimension(c.Query("dimension"))
	if !ok {
		response.BadRequest(c, "Invalid dimension, use account, group or model")
		return
	}

	userTZ := c.Query("timezone")
	startDateStr := strings.TrimSpace(c.Query("start_date"))
	endDateStr := strings.TrimSpace(c.Query("end_date"))
	if startDateStr == "" || endDateStr == "" {
		response.BadRequest(c, "start_date and end_date are required")
		return
	}
	startTime, err := timezone.ParseInUserLocation("2006-01-02", startDateStr, userTZ)
	if err != nil {
		response.BadRequest(c, "Invalid start_date format, use YYYY-MM-DD")
		return
	}
	endTime, err := timezone.ParseInUserLocation("2006-01-02", endDateStr, userTZ)
	if err != nil {
		response.BadRequest(c, "Invalid end_date format, use YYYY-MM-DD")
		return
	}
	// 半开区间 [start, end)：end_date 当天整天计入（DST-safe）。
	endTime = endTime.AddDate(0, 0, 1)

	filters := usagestats.UsageAnalyticsFilters{
		Dimension: dimension,
		StartTime: startTime,
		EndTime:   endTime,
		Model:     strings.TrimSpace(c.Query("model")),
	}
	if filters.GroupID, ok = parseOptionalPositiveInt64(c, "group_id"); !ok {
		return
	}
	if filters.AccountID, ok = parseOptionalPositiveInt64(c, "account_id"); !ok {
		return
	}
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit <= 0 {
			response.BadRequest(c, "Invalid limit")
			return
		}
		filters.Limit = limit
	}

	result, err := h.usageService.GetUsageAnalytics(c.Request.Context(), filters)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

func parseOptionalPositiveInt64(c *gin.Context, key string) (int64, bool) {
	raw := strings.TrimSpace(c.Query(key))
	if raw == "" {
		return 0, true
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 {
		response.BadRequest(c, "Invalid "+key)
		return 0, false
	}
	return value, true
}
