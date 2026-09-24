package kb

import "github.com/gin-gonic/gin"

// RouteGuards contains the middleware applied to protected KB endpoints.
type RouteGuards struct {
	Authenticate         gin.HandlerFunc
	RequireIndexer       gin.HandlerFunc
	AllowUnauthenticated bool
}

// RegisterRoutes wires the public health endpoint and guarded KB endpoints.
func RegisterRoutes(rg *gin.RouterGroup, h *Handler, guards RouteGuards) {
	if rg == nil {
		panic("kb: router group is required")
	}
	if h == nil || h.svc == nil || h.principal == nil {
		panic("kb: handler with principal provider is required")
	}
	validateRouteGuards(guards)
	rg.GET("/health", h.health)
	rg.GET("/ready", h.ready)
	rg.POST("/index", routeHandlers(guards, true, h.index)...)
	rg.POST("/chat", routeHandlers(guards, false, h.chat)...)
}

func validateRouteGuards(guards RouteGuards) {
	if guards.AllowUnauthenticated {
		if guards.Authenticate != nil || guards.RequireIndexer != nil {
			panic("kb: unauthenticated routes cannot include auth guards")
		}
		return
	}
	if guards.Authenticate == nil {
		panic("kb: authenticate guard is required")
	}
	if guards.RequireIndexer == nil {
		panic("kb: indexer guard is required")
	}
}

func routeHandlers(guards RouteGuards, index bool, handler gin.HandlerFunc) []gin.HandlerFunc {
	if guards.AllowUnauthenticated {
		return []gin.HandlerFunc{handler}
	}
	handlers := []gin.HandlerFunc{guards.Authenticate}
	if index {
		handlers = append(handlers, guards.RequireIndexer)
	}
	return append(handlers, handler)
}
