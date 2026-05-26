package user

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/shared"
)

// ----------------------------------------------------------------------------
// Mock service satisfying the handler's `service` interface.
// ----------------------------------------------------------------------------

type mockSvc struct {
	registerErr error
	loginErr    error
	getByIDErr  error
	user        *User
	token       string
}

func (m *mockSvc) Register(_ context.Context, _ RegisterInput) (*User, string, error) {
	return m.user, m.token, m.registerErr
}
func (m *mockSvc) Login(_ context.Context, _ LoginInput) (*User, string, error) {
	return m.user, m.token, m.loginErr
}
func (m *mockSvc) GetByID(_ context.Context, _ shared.UserID) (*User, error) {
	return m.user, m.getByIDErr
}

func newTestHandler(t *testing.T, svc service) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := &Handler{svc: svc}
	api := r.Group("/api/v1")
	api.POST("/auth/register", h.register)
	api.POST("/auth/login", h.login)
	return r
}

// ----------------------------------------------------------------------------
// Tests
// ----------------------------------------------------------------------------

func TestHandler_Register_Created(t *testing.T) {
	u, _ := NewUser("alice@example.com", "hashed", "Alice")
	r := newTestHandler(t, &mockSvc{user: u, token: "tok"})

	body, _ := json.Marshal(RegisterRequest{
		Email: "alice@example.com", Password: "pa55word!", DisplayName: "Alice",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	var resp AuthResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "tok", resp.Token)
	assert.Equal(t, "alice@example.com", resp.User.Email)
}

func TestHandler_Register_Conflict(t *testing.T) {
	r := newTestHandler(t, &mockSvc{registerErr: ErrEmailAlreadyExists})

	body, _ := json.Marshal(RegisterRequest{
		Email: "alice@example.com", Password: "pa55word!", DisplayName: "Alice",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestHandler_Register_BadRequest(t *testing.T) {
	r := newTestHandler(t, &mockSvc{})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register",
		bytes.NewReader([]byte(`{"email":"not-an-email"}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandler_Login_Unauthorized(t *testing.T) {
	r := newTestHandler(t, &mockSvc{loginErr: ErrInvalidCredentials})

	body, _ := json.Marshal(LoginRequest{Email: "alice@example.com", Password: "wrong"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestWriteError_AllBranches(t *testing.T) {
	cases := []struct {
		err    error
		status int
	}{
		{ErrInvalidCredentials, http.StatusUnauthorized},
		{ErrEmailAlreadyExists, http.StatusConflict},
		{ErrUserNotFound, http.StatusNotFound},
		{ErrInvalidEmail, http.StatusBadRequest},
		{errors.New("boom"), http.StatusInternalServerError},
	}
	for _, tc := range cases {
		gin.SetMode(gin.TestMode)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		writeError(c, tc.err)
		assert.Equal(t, tc.status, w.Code, "err=%v", tc.err)
	}
}
