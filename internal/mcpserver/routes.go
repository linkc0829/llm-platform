package mcpserver

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"go.uber.org/zap"

	"github.com/linkc0829/llm-platform/internal/auth"
)

// RegisterStreamableHTTPRoutes registers the MCP HTTP endpoints and owns the
// authentication decision for them. When auth is enabled, a missing resolver
// is a composition error and causes startup to panic.
func RegisterStreamableHTTPRoutes(engine *gin.Engine, svc searcher, log *zap.Logger, authDisabled bool, resolver auth.Resolver) {
	var handler http.Handler
	if authDisabled {
		handler = NewUnauthenticatedStreamableHTTPHandler(svc, log)
	} else {
		if resolver == nil {
			panic("mcpserver: resolver is required when HTTP auth is enabled")
		}
		handler = auth.RequireMCPBearerToken(resolver, NewStreamableHTTPHandler(svc, log))
	}

	route := gin.WrapH(handler)
	engine.GET("/mcp", route)
	engine.POST("/mcp", route)
	engine.DELETE("/mcp", route)
}
