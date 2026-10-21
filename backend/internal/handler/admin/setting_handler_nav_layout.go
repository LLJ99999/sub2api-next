package admin

import (
	"encoding/json"
	"errors"
	"io"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// GetAdminNavLayout returns the admin sidebar layout.
// GET /api/v1/admin/settings/nav-layout
func (h *SettingHandler) GetAdminNavLayout(c *gin.Context) {
	layout, err := h.settingService.GetAdminNavLayout(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, layout)
}

// UpdateAdminNavLayout replaces the admin sidebar layout. Send {} to reset.
// PUT /api/v1/admin/settings/nav-layout
func (h *SettingHandler) UpdateAdminNavLayout(c *gin.Context) {
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, service.MaxAdminNavLayoutBytes+1))
	if err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if err := h.settingService.SetAdminNavLayout(c.Request.Context(), json.RawMessage(body)); err != nil {
		if errors.Is(err, service.ErrInvalidAdminNavLayout) {
			response.BadRequest(c, "nav layout must be a JSON object of at most 64KB")
			return
		}
		response.ErrorFrom(c, err)
		return
	}
	layout, err := h.settingService.GetAdminNavLayout(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, layout)
}
