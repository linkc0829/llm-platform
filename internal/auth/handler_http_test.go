package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestTokenRoutesIgnoreAdminRequestField(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, _ := newBootstrapStore(t)
	admin, adminToken, err := store.CreateAdminToken(context.Background(), "cli", "admin")
	if err != nil {
		t.Fatalf("CreateAdminToken() error = %v, want nil", err)
	}

	r := gin.New()
	registerTokenTestRoutes(r, store)
	req := httptest.NewRequest(http.MethodPost, "/admin/tokens", strings.NewReader(`{"name":"alice","teams":["Store.POS"],"engineering":true,"indexer":true,"admin":true}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+adminToken)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("POST /admin/tokens status = %d, want 201; body = %s", w.Code, w.Body.String())
	}
	var created CreateTokenResponse
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("POST /admin/tokens response decode error = %v", err)
	}
	principal, err := store.Resolve(context.Background(), created.Token)
	if err != nil {
		t.Fatalf("Resolve(created token) error = %v, want nil", err)
	}
	if principal.Admin {
		t.Error("Resolve(created token).Admin = true, want body admin field ignored")
	}
	if !principal.Indexer || principal.ID == admin.ID {
		t.Errorf("Resolve(created token) = %+v, want non-admin indexer principal", principal)
	}

	req = httptest.NewRequest(http.MethodGet, "/admin/tokens", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /admin/tokens status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "token_sha256") || strings.Contains(w.Body.String(), created.Token) {
		t.Errorf("GET /admin/tokens body exposes secret material: %s", w.Body.String())
	}
}

func TestTokenRoutesProtectAdminAndRequireAdminCapability(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, _ := newBootstrapStore(t)
	admin, adminToken, err := store.CreateAdminToken(context.Background(), "cli", "admin")
	if err != nil {
		t.Fatalf("CreateAdminToken() error = %v, want nil", err)
	}
	regular, regularToken, err := store.CreateToken(context.Background(), admin.ID, TokenSpec{Name: "alice"})
	if err != nil {
		t.Fatalf("CreateToken() error = %v, want nil", err)
	}
	r := gin.New()
	registerTokenTestRoutes(r, store)

	req := httptest.NewRequest(http.MethodGet, "/admin/tokens", nil)
	req.Header.Set("Authorization", "Bearer "+regularToken)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("GET /admin/tokens with regular token status = %d, want 403", w.Code)
	}

	req = httptest.NewRequest(http.MethodDelete, "/admin/tokens/"+admin.ID, nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("DELETE admin status = %d, want 403", w.Code)
	}

	req = httptest.NewRequest(http.MethodDelete, "/admin/tokens/"+regular.ID, nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("DELETE regular status = %d, want 204; body = %s", w.Code, w.Body.String())
	}
	if _, err := store.Resolve(context.Background(), regularToken); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("Resolve(deleted regular token) error = %v, want ErrInvalidToken", err)
	}
}

func TestRegisterTokenRoutesRejectsIncompleteGuards(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name   string
		guards TokenRouteGuards
	}{
		{name: "missing_authenticate", guards: TokenRouteGuards{RequireAdmin: gin.HandlerFunc(func(*gin.Context) {})}},
		{name: "missing_admin", guards: TokenRouteGuards{Authenticate: gin.HandlerFunc(func(*gin.Context) {})}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("RegisterTokenRoutes() did not panic on incomplete guard wiring")
				}
			}()
			r := gin.New()
			RegisterTokenRoutes(r.Group(""), NewHandler(&Store{}, nil), tc.guards)
		})
	}
}

func TestRegisterTokenRoutesRejectsMissingActorIDProvider(t *testing.T) {
	gin.SetMode(gin.TestMode)
	defer func() {
		if recover() == nil {
			t.Fatal("RegisterTokenRoutes() did not panic on missing actor ID provider")
		}
	}()

	r := gin.New()
	RegisterTokenRoutes(r.Group(""), NewHandler(&Store{}, nil), TokenRouteGuards{
		Authenticate: func(c *gin.Context) { c.Next() },
		RequireAdmin: func(c *gin.Context) { c.Next() },
	})
}

func TestTokenRoutesMapOversizedSnapshotToConflict(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := NewHandler(tokenManagerStub{createErr: ErrAuthSnapshotTooLarge}, nil)
	r.POST("/admin/tokens", h.createToken)

	req := httptest.NewRequest(http.MethodPost, "/admin/tokens", strings.NewReader(`{"name":"alice"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("POST /admin/tokens oversized snapshot status = %d, want 409; body = %s", w.Code, w.Body.String())
	}
}

func registerTokenTestRoutes(r *gin.Engine, store *Store) {
	RegisterTokenRoutes(r.Group(""), NewHandler(store, PrincipalIDFromContext), TokenRouteGuards{
		Authenticate: RequirePrincipal(store),
		RequireAdmin: RequireAdmin(),
	})
}

type tokenManagerStub struct {
	createErr error
}

func (s tokenManagerStub) ListTokens(context.Context) ([]Record, error) {
	return nil, nil
}

func (s tokenManagerStub) CreateToken(context.Context, string, TokenSpec) (Record, string, error) {
	return Record{}, "", s.createErr
}

func (s tokenManagerStub) DeleteToken(context.Context, string, string) error {
	return nil
}
