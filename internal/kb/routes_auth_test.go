package kb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/linkc0829/llm-platform/internal/auth"
	"github.com/linkc0829/llm-platform/internal/shared"
)

type routeResolver struct {
	principals map[string]shared.Principal
}

func (r routeResolver) Resolve(_ context.Context, token string) (shared.Principal, error) {
	principal, ok := r.principals[token]
	if !ok {
		return shared.Principal{}, auth.ErrInvalidToken
	}
	return principal, nil
}

func authenticatedPrincipal(c *gin.Context) shared.Principal {
	principal, _ := auth.PrincipalFromContext(c)
	return principal
}

func TestRegisterRoutesGuardsProtectedEndpoints(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	resolver := routeResolver{principals: map[string]shared.Principal{
		"regular": {ID: "p_regular", Name: "regular"},
		"admin":   {ID: "p_admin", Name: "admin", Admin: true},
		"indexer": {ID: "p_indexer", Name: "indexer", Indexer: true},
	}}
	RegisterRoutes(r.Group(""), &Handler{svc: &fakeHandlerService{indexFiles: 1, indexSections: 1}, principal: authenticatedPrincipal}, RouteGuards{
		Authenticate:   auth.RequirePrincipal(resolver),
		RequireIndexer: auth.RequireIndexer(),
	})

	tests := []struct {
		name       string
		method     string
		path       string
		body       string
		token      string
		wantStatus int
	}{
		{name: "health_is_public", method: http.MethodGet, path: "/health", wantStatus: http.StatusOK},
		{name: "ready_is_public", method: http.MethodGet, path: "/ready", wantStatus: http.StatusOK},
		{name: "chat_without_token", method: http.MethodPost, path: "/chat", body: `{"query":"hello"}`, wantStatus: http.StatusUnauthorized},
		{name: "index_without_token", method: http.MethodPost, path: "/index", wantStatus: http.StatusUnauthorized},
		{name: "chat_with_regular_token", method: http.MethodPost, path: "/chat", body: `{"query":"hello"}`, token: "regular", wantStatus: http.StatusOK},
		{name: "index_with_regular_token", method: http.MethodPost, path: "/index", token: "regular", wantStatus: http.StatusForbidden},
		{name: "index_with_admin_token", method: http.MethodPost, path: "/index", token: "admin", wantStatus: http.StatusForbidden},
		{name: "index_with_indexer_token", method: http.MethodPost, path: "/index", token: "indexer", wantStatus: http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			if tt.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			if tt.token != "" {
				req.Header.Set("Authorization", "Bearer "+tt.token)
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("%s %s with token %q status = %d, want %d; body = %s", tt.method, tt.path, tt.token, w.Code, tt.wantStatus, w.Body.String())
			}
		})
	}
}

func TestRegisterRoutesRejectsIncompleteGuards(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name   string
		guards RouteGuards
	}{
		{
			name: "zero_value",
		},
		{
			name: "missing_authenticate",
			guards: RouteGuards{
				RequireIndexer: gin.HandlerFunc(func(*gin.Context) {}),
			},
		},
		{
			name: "missing_indexer",
			guards: RouteGuards{
				Authenticate: gin.HandlerFunc(func(*gin.Context) {}),
			},
		},
		{
			name: "unauthenticated_flag_with_guard",
			guards: RouteGuards{
				AllowUnauthenticated: true,
				Authenticate:         gin.HandlerFunc(func(*gin.Context) {}),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("RegisterRoutes did not reject invalid guard wiring")
				}
			}()
			r := gin.New()
			RegisterRoutes(r.Group(""), &Handler{svc: &fakeHandlerService{}, principal: AnonymousPrincipal}, tt.guards)
		})
	}
}

func TestRegisterRoutesPassesPrincipalIDToChat(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resolver := routeResolver{principals: map[string]shared.Principal{
		"regular": {ID: "p_regular", Name: "regular"},
	}}
	svc := &fakeHandlerService{chatAnswer: NewAnswer("answer", nil, "", nil, true), chatSessionID: "s1"}
	r := gin.New()
	RegisterRoutes(r.Group(""), &Handler{svc: svc, principal: authenticatedPrincipal}, RouteGuards{
		Authenticate:   auth.RequirePrincipal(resolver),
		RequireIndexer: auth.RequireIndexer(),
	})

	req := httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader("{\"query\":\"hello\",\"session_id\":\"s1\"}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer regular")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("POST /chat status = %d body = %s, want 200", w.Code, w.Body.String())
	}
	if svc.chatOwnerID != "p_regular" {
		t.Errorf("ChatWithMetrics() ownerID = %q, want immutable principal ID p_regular", svc.chatOwnerID)
	}
}
