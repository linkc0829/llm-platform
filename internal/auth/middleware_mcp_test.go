package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"

	"github.com/linkc0829/llm-platform/internal/shared"
)

func TestNewMCPTokenVerifier(t *testing.T) {
	store := middlewareTestStore()
	verifier := NewMCPTokenVerifier(store)

	info, err := verifier(context.Background(), "kb_regular", httptest.NewRequest(http.MethodPost, "/mcp", nil))
	if err != nil {
		t.Fatalf("NewMCPTokenVerifier(valid token) error = %v, want nil", err)
	}
	if info.UserID != "p_regular" {
		t.Errorf("NewMCPTokenVerifier(valid token).UserID = %q, want %q", info.UserID, "p_regular")
	}
	principal, ok := info.Extra[principalExtraKey].(shared.Principal)
	if !ok {
		t.Fatalf("NewMCPTokenVerifier(valid token).Extra[%q] = %T, want shared.Principal", principalExtraKey, info.Extra[principalExtraKey])
	}
	if principal.ID != "p_regular" {
		t.Errorf("NewMCPTokenVerifier(valid token).Extra[%q].ID = %q, want %q", principalExtraKey, principal.ID, "p_regular")
	}
	if !info.Expiration.After(time.Now()) {
		t.Errorf("NewMCPTokenVerifier(valid token).Expiration = %v, want future expiration", info.Expiration)
	}

	_, err = verifier(context.Background(), "kb_unknown", httptest.NewRequest(http.MethodPost, "/mcp", nil))
	if !errors.Is(err, sdkauth.ErrInvalidToken) {
		t.Errorf("NewMCPTokenVerifier(invalid token) error = %v, want sdk ErrInvalidToken", err)
	}
}

func TestRequireMCPBearerToken(t *testing.T) {
	store := middlewareTestStore()
	handler := RequireMCPBearerToken(store, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	tests := []struct {
		name       string
		authorize  string
		wantStatus int
	}{
		{name: "missing_token", wantStatus: http.StatusUnauthorized},
		{name: "invalid_token", authorize: "Bearer kb_unknown", wantStatus: http.StatusUnauthorized},
		{name: "valid_token", authorize: "Bearer kb_regular", wantStatus: http.StatusNoContent},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
			if tt.authorize != "" {
				req.Header.Set("Authorization", tt.authorize)
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Fatalf("RequireMCPBearerToken(%q) status = %d, want %d", tt.authorize, w.Code, tt.wantStatus)
			}
			if tt.wantStatus == http.StatusUnauthorized && w.Header().Get("WWW-Authenticate") != "" {
				t.Errorf("RequireMCPBearerToken(%q) WWW-Authenticate = %q, want empty", tt.authorize, w.Header().Get("WWW-Authenticate"))
			}
		})
	}
}
