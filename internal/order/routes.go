package order

import (
	"github.com/gin-gonic/gin"

	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/platform/auth"
)

// RegisterRoutes wires order endpoints. All routes require auth.
func RegisterRoutes(rg *gin.RouterGroup, h *Handler, authMW *auth.Manager) {
	g := rg.Group("/orders")
	g.Use(auth.Middleware(authMW))
	{
		g.POST("", h.create)
		g.GET("", h.list)
		g.GET("/:id", h.get)
		g.POST("/:id/pay", h.pay)
		g.POST("/:id/cancel", h.cancel)
	}
}
