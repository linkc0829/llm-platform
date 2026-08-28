package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/shared"
)

func TestRequirePrincipal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := middlewareTestStore()
	tests := []struct {
		name       string
		authorize  string
		wantStatus int
		wantID     string
	}{
		{name: "missing_token", wantStatus: http.StatusUnauthorized},
		{name: "malformed_authorization", authorize: "Basic kb_regular", wantStatus: http.StatusUnauthorized},
		{name: "invalid_token", authorize: "Bearer kb_unknown", wantStatus: http.StatusUnauthorized},
		{name: "valid_token", authorize: "Bearer kb_regular", wantStatus: http.StatusOK, wantID: "p_regular"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := gin.New()
			r.GET("/", RequirePrincipal(store), func(c *gin.Context) {
				principal, ok := PrincipalFromContext(c)
				if !ok {
					c.Status(http.StatusInternalServerError)
					return
				}
				c.JSON(http.StatusOK, gin.H{"id": principal.ID})
			})

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.authorize != "" {
				req.Header.Set("Authorization", tt.authorize)
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Fatalf("RequirePrincipal(%q) status = %d, want %d", tt.authorize, w.Code, tt.wantStatus)
			}
			if tt.wantID != "" && w.Body.String() != `{"id":"p_regular"}` {
				t.Errorf("RequirePrincipal(%q) body = %s, want principal id %q", tt.authorize, w.Body.String(), tt.wantID)
			}
			if tt.wantStatus == http.StatusUnauthorized && w.Header().Get("WWW-Authenticate") != "" {
				t.Errorf("RequirePrincipal(%q) WWW-Authenticate = %q, want empty", tt.authorize, w.Header().Get("WWW-Authenticate"))
			}
		})
	}
}

func TestRequireCapabilitiesAreIndependent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &Store{principals: []Record{
		{Principal: shared.Principal{ID: "p_indexer", Name: "indexer", Indexer: true}, TokenSHA256: HashToken("kb_indexer")},
		{Principal: shared.Principal{ID: "p_admin", Name: "admin", Admin: true}, TokenSHA256: HashToken("kb_admin")},
	}}
	tests := []struct {
		name       string
		token      string
		capability gin.HandlerFunc
		wantStatus int
	}{
		{name: "indexer_can_index", token: "kb_indexer", capability: RequireIndexer(), wantStatus: http.StatusNoContent},
		{name: "admin_cannot_index", token: "kb_admin", capability: RequireIndexer(), wantStatus: http.StatusForbidden},
		{name: "indexer_cannot_admin", token: "kb_indexer", capability: RequireAdmin(), wantStatus: http.StatusForbidden},
		{name: "admin_can_admin", token: "kb_admin", capability: RequireAdmin(), wantStatus: http.StatusNoContent},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := gin.New()
			r.GET("/", RequirePrincipal(store), tt.capability, func(c *gin.Context) {
				c.Status(http.StatusNoContent)
			})

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set("Authorization", "Bearer "+tt.token)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("capability(%q) status = %d, want %d", tt.token, w.Code, tt.wantStatus)
			}
		})
	}
}

func middlewareTestStore() *Store {
	return &Store{principals: []Record{
		{Principal: shared.Principal{ID: "p_regular", Name: "regular"}, TokenSHA256: HashToken("kb_regular")},
	}}
}
