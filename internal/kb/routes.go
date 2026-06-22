package kb

import "github.com/gin-gonic/gin"

// RegisterRoutes wires kb endpoints. All routes are public.
func RegisterRoutes(rg *gin.RouterGroup, h *Handler) {
	rg.GET("/health", h.health)
}
