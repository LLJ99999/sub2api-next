package admin

import (
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// UpstreamOpsHandler serves upstream change history/alerts, upstream quality
// probes and the public transit snapshot settings. All routes are admin-only.
type UpstreamOpsHandler struct {
	changes *service.UpstreamChangeService
	quality *service.UpstreamQualityProbeService
	transit *service.PublicTransitService
}

func NewUpstreamOpsHandler(changes *service.UpstreamChangeService, quality *service.UpstreamQualityProbeService, transit *service.PublicTransitService) *UpstreamOpsHandler {
	return &UpstreamOpsHandler{changes: changes, quality: quality, transit: transit}
}

func parsePositiveID(c *gin.Context, value string) (int64, bool) {
	id, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid account id")
		return 0, false
	}
	return id, true
}

func parseOptionalTime(c *gin.Context, key string) (*time.Time, bool) {
	raw := strings.TrimSpace(c.Query(key))
	if raw == "" {
		return nil, true
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		response.BadRequest(c, "Invalid "+key+": use RFC3339")
		return nil, false
	}
	return &t, true
}

// ---- upstream change alerts ----

func (h *UpstreamOpsHandler) GetChangeSettings(c *gin.Context) {
	settings, err := h.changes.GetSettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, settings)
}

func (h *UpstreamOpsHandler) UpdateChangeSettings(c *gin.Context) {
	var req service.UpstreamChangeAlertSettings
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	settings, err := h.changes.UpdateSettings(c.Request.Context(), req)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, settings)
}

func (h *UpstreamOpsHandler) ListChangeEvents(c *gin.Context) {
	filter := service.UpstreamChangeEventFilter{
		Site: strings.TrimSpace(c.Query("site")), Kind: strings.TrimSpace(c.Query("kind")), Direction: strings.TrimSpace(c.Query("direction")),
	}
	if raw := strings.TrimSpace(c.Query("account_id")); raw != "" {
		id, ok := parsePositiveID(c, raw)
		if !ok {
			return
		}
		filter.AccountID = id
	}
	var ok bool
	if filter.From, ok = parseOptionalTime(c, "from"); !ok {
		return
	}
	if filter.To, ok = parseOptionalTime(c, "to"); !ok {
		return
	}
	filter.Page, _ = strconv.Atoi(c.DefaultQuery("page", "1"))
	filter.PageSize, _ = strconv.Atoi(c.DefaultQuery("page_size", "50"))
	page, err := h.changes.ListEvents(c.Request.Context(), filter)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, page)
}

func (h *UpstreamOpsHandler) ListChangeSnapshots(c *gin.Context) {
	id, ok := parsePositiveID(c, c.Query("account_id"))
	if !ok {
		return
	}
	days, _ := strconv.Atoi(c.DefaultQuery("days", "7"))
	items, err := h.changes.ListSnapshots(c.Request.Context(), id, days)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"items": items})
}

func (h *UpstreamOpsHandler) ListChangeSites(c *gin.Context) {
	sites, err := h.changes.ListSites(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"items": sites})
}

// ---- upstream quality probes ----

func (h *UpstreamOpsHandler) GetQualitySettings(c *gin.Context) {
	settings, err := h.quality.GetSettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, settings)
}

func (h *UpstreamOpsHandler) UpdateQualitySettings(c *gin.Context) {
	var req service.UpstreamQualityProbeSettings
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	settings, err := h.quality.UpdateSettings(c.Request.Context(), req)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, settings)
}

func (h *UpstreamOpsHandler) ListQualityConfigs(c *gin.Context) {
	items, err := h.quality.ListConfigs(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"items": items})
}

func (h *UpstreamOpsHandler) GetQualityAccount(c *gin.Context) {
	id, ok := parsePositiveID(c, c.Param("id"))
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	view, err := h.quality.AccountView(c.Request.Context(), id, limit)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, view)
}

type upstreamQualityConfigRequest struct {
	Enabled         bool   `json:"enabled"`
	Model           string `json:"model"`
	IntervalMinutes int    `json:"interval_minutes"`
	// Prompts: omitted keeps the saved prompts; [] clears them.
	Prompts []string `json:"prompts"`
}

func (h *UpstreamOpsHandler) SaveQualityAccount(c *gin.Context) {
	id, ok := parsePositiveID(c, c.Param("id"))
	if !ok {
		return
	}
	var req upstreamQualityConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	cfg, err := h.quality.SaveConfig(c.Request.Context(), service.UpstreamQualityProbeConfig{
		AccountID: id, Enabled: req.Enabled, Model: req.Model, IntervalMinutes: req.IntervalMinutes, Prompts: req.Prompts,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, cfg)
}

func (h *UpstreamOpsHandler) DeleteQualityAccount(c *gin.Context) {
	id, ok := parsePositiveID(c, c.Param("id"))
	if !ok {
		return
	}
	if err := h.quality.DeleteConfig(c.Request.Context(), id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"deleted": true})
}

type upstreamQualityRunRequest struct {
	Model string `json:"model"`
}

func (h *UpstreamOpsHandler) RunQualityAccount(c *gin.Context) {
	id, ok := parsePositiveID(c, c.Param("id"))
	if !ok {
		return
	}
	var req upstreamQualityRunRequest
	_ = c.ShouldBindJSON(&req)
	result, err := h.quality.RunNow(c.Request.Context(), id, req.Model)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

// ---- public transit settings ----

func (h *UpstreamOpsHandler) GetTransitSettings(c *gin.Context) {
	settings, err := h.transit.GetSettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, settings)
}

func (h *UpstreamOpsHandler) UpdateTransitSettings(c *gin.Context) {
	var req service.PublicTransitSettings
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	settings, err := h.transit.UpdateSettings(c.Request.Context(), req)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, settings)
}

type upstreamQualityBatchRequest struct {
	AccountIDs      []int64 `json:"account_ids" binding:"required"`
	Enabled         *bool   `json:"enabled" binding:"required"`
	Model           string  `json:"model"`
	IntervalMinutes int     `json:"interval_minutes"`
}

// BatchQualityAccounts turns 上游质量探测 on/off for many accounts (one per base URL
// when enabling); saved model, interval and prompts are kept.
// POST /api/v1/admin/upstream-quality-probes/accounts/batch
func (h *UpstreamOpsHandler) BatchQualityAccounts(c *gin.Context) {
	var req upstreamQualityBatchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	results, err := h.quality.BatchSetEnabled(c.Request.Context(), req.AccountIDs, *req.Enabled, req.Model, req.IntervalMinutes)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"results": results})
}
