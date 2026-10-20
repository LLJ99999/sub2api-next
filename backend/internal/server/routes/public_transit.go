package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"

	"github.com/gin-gonic/gin"
)

// RegisterPublicTransitRoutes registers the anonymous transit endpoints. They
// sit outside /api/v1 because the paths are a cross-site convention. Both use
// the public per-IP panel limiter; the handler answers 404 while disabled.
func RegisterPublicTransitRoutes(r *gin.Engine, h *handler.Handlers, panelRateLimiter *middleware.PanelRateLimiter) {
	if h == nil || h.PublicTransit == nil {
		return
	}
	limit := panelRateLimiter.PublicIP()
	r.GET("/.well-known/ai-transit.json", limit, h.PublicTransit.Discovery)
	r.GET("/api/public/transit/v1/snapshot", limit, h.PublicTransit.Snapshot)
}
