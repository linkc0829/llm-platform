package auth

import "github.com/gin-gonic/gin"

// TokenRouteGuards makes protected admin route wiring explicit and fail loud.
type TokenRouteGuards struct {
	Authenticate gin.HandlerFunc
	RequireAdmin gin.HandlerFunc
}

// RegisterTokenRoutes registers token CRUD under /admin/tokens. The API
// cannot be registered without both authentication and admin capability
// guards.
func RegisterTokenRoutes(rg *gin.RouterGroup, h *Handler, guards TokenRouteGuards) {
	if rg == nil {
		panic("auth: token routes require a router group")
	}
	if h == nil || h.manager == nil {
		panic("auth: token routes require a token manager")
	}
	if h.actorID == nil {
		panic("auth: token routes require an actor ID provider")
	}
	if guards.Authenticate == nil {
		panic("auth: token routes require authenticate guard")
	}
	if guards.RequireAdmin == nil {
		panic("auth: token routes require admin guard")
	}
	handlers := []gin.HandlerFunc{guards.Authenticate, guards.RequireAdmin}
	rg.GET("/admin/tokens", appendHandlers(handlers, h.listTokens)...)
	rg.POST("/admin/tokens", appendHandlers(handlers, h.createToken)...)
	rg.DELETE("/admin/tokens/:id", appendHandlers(handlers, h.deleteToken)...)
}

func appendHandlers(guards []gin.HandlerFunc, handler gin.HandlerFunc) []gin.HandlerFunc {
	result := make([]gin.HandlerFunc, 0, len(guards)+1)
	result = append(result, guards...)
	return append(result, handler)
}
