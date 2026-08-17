package mcpserver

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func TestRegisterStreamableHTTPRoutesRejectsMissingResolver(t *testing.T) {
	gin.SetMode(gin.TestMode)
	defer func() {
		if recover() == nil {
			t.Fatal("RegisterStreamableHTTPRoutes did not reject enabled auth without a resolver")
		}
	}()

	RegisterStreamableHTTPRoutes(gin.New(), &fakeSearcher{}, zap.NewNop(), false, nil)
}

func TestRegisterStreamableHTTPRoutesProtectsMCPWhenEnabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	RegisterStreamableHTTPRoutes(engine, &fakeSearcher{}, zap.NewNop(), false, mcpTestResolver{})

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("registered MCP route without a token status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestRegisterStreamableHTTPRoutesLeavesMCPUnprotectedWhenDisabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	RegisterStreamableHTTPRoutes(engine, &fakeSearcher{}, zap.NewNop(), true, nil)

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code == http.StatusUnauthorized {
		t.Fatal("registered MCP route returned unauthorized when auth was explicitly disabled")
	}
}
