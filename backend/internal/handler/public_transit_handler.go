package handler

import (
	"errors"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// PublicTransitHandler serves the anonymous transit discovery document and
// snapshot. Responses are plain JSON (no envelope) so other sites and
// aggregators can read them directly. Both answer 404 while disabled.
type PublicTransitHandler struct {
	svc *service.PublicTransitService
}

func NewPublicTransitHandler(svc *service.PublicTransitService) *PublicTransitHandler {
	return &PublicTransitHandler{svc: svc}
}

func (h *PublicTransitHandler) writeDisabled(c *gin.Context, err error) {
	if errors.Is(err, service.ErrPublicTransitDisabled) || h == nil || h.svc == nil {
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusServiceUnavailable, gin.H{"error": "snapshot_unavailable"})
}

// Discovery handles GET /.well-known/ai-transit.json.
func (h *PublicTransitHandler) Discovery(c *gin.Context) {
	if h == nil || h.svc == nil {
		h.writeDisabled(c, service.ErrPublicTransitDisabled)
		return
	}
	doc, err := h.svc.Discovery(c.Request.Context())
	if err != nil {
		h.writeDisabled(c, err)
		return
	}
	c.Header("Cache-Control", "public, max-age=300")
	c.Header("Access-Control-Allow-Origin", "*")
	c.JSON(http.StatusOK, doc)
}

// Snapshot handles GET /api/public/transit/v1/snapshot.
func (h *PublicTransitHandler) Snapshot(c *gin.Context) {
	if h == nil || h.svc == nil {
		h.writeDisabled(c, service.ErrPublicTransitDisabled)
		return
	}
	snap, err := h.svc.Snapshot(c.Request.Context())
	if err != nil {
		h.writeDisabled(c, err)
		return
	}
	c.Header("Cache-Control", "public, max-age=60")
	c.Header("Access-Control-Allow-Origin", "*")
	c.JSON(http.StatusOK, snap)
}
