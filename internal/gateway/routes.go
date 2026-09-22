package gateway

import (
	"github.com/gin-gonic/gin"
)

// RegisterRoutes mounts reverse proxy and health probe routes.
func RegisterRoutes(rg *gin.RouterGroup, h *Handler) {
	rg.GET("/healthz", h.Healthz)
	rg.Any("/v1/*path", h.Proxy)
}
