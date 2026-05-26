package user

import (
	"github.com/gin-gonic/gin"

	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/platform/auth"
)

// RegisterRoutes wires user endpoints into the given router group.
//
// Public:  POST /auth/register, POST /auth/login
// Auth'd:  GET  /users/me
func RegisterRoutes(rg *gin.RouterGroup, h *Handler, authMW *auth.Manager) {
	authGroup := rg.Group("/auth")
	{
		authGroup.POST("/register", h.register)
		authGroup.POST("/login", h.login)
	}

	usersGroup := rg.Group("/users")
	usersGroup.Use(auth.Middleware(authMW))
	{
		usersGroup.GET("/me", h.me)
	}
}
