package payment

import (
	"github.com/gin-gonic/gin"

	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/platform/auth"
)

// RegisterRoutes wires payment endpoints. All routes require auth.
func RegisterRoutes(rg *gin.RouterGroup, h *Handler, authMW *auth.Manager) {
	g := rg.Group("/payments")
	g.Use(auth.Middleware(authMW))
	{
		g.GET("/:id", h.get)
		g.GET("/by-order/:order_id", h.getByOrder)
	}
}
