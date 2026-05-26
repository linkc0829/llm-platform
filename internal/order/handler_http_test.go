package order

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/shared"
)

// ----------------------------------------------------------------------------
// Mock service
// ----------------------------------------------------------------------------

type mockSvc struct {
	createOrder *Order
	createErr   error
	getOrder    *Order
	getErr      error
	listOrders  []*Order
	listTotal   int64
	listErr     error
}

func (m *mockSvc) Create(_ context.Context, _ CreateInput) (*Order, error) {
	return m.createOrder, m.createErr
}
func (m *mockSvc) Pay(_ context.Context, _ shared.OrderID, _ shared.UserID) (*Order, error) {
	return m.getOrder, m.getErr
}
func (m *mockSvc) Cancel(_ context.Context, _ shared.OrderID, _ shared.UserID) (*Order, error) {
	return m.getOrder, m.getErr
}
func (m *mockSvc) Get(_ context.Context, _ shared.OrderID, _ shared.UserID) (*Order, error) {
	return m.getOrder, m.getErr
}
func (m *mockSvc) List(_ context.Context, _ shared.UserID, _ shared.Pagination) ([]*Order, int64, error) {
	return m.listOrders, m.listTotal, m.listErr
}

// withAuth injects a fake authenticated user id into the gin context — same key
// the platform/auth middleware uses.
func withAuth(uid shared.UserID) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set("auth.userID", uid.String())
		c.Next()
	}
}

func newTestRouter(t *testing.T, svc service, uid shared.UserID) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := &Handler{svc: svc}
	g := r.Group("/api/v1/orders")
	g.Use(withAuth(uid))
	{
		g.POST("", h.create)
		g.GET("", h.list)
		g.GET("/:id", h.get)
		g.POST("/:id/pay", h.pay)
		g.POST("/:id/cancel", h.cancel)
	}
	return r
}

// ----------------------------------------------------------------------------
// Tests
// ----------------------------------------------------------------------------

func TestHandler_CreateOrder(t *testing.T) {
	uid := shared.NewUserID()
	o, _ := NewOrder(uid, shared.MustNewMoney(2500, "USD"))
	r := newTestRouter(t, &mockSvc{createOrder: o}, uid)

	body, _ := json.Marshal(CreateOrderRequest{Amount: 2500, Currency: "USD"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/orders", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusCreated, w.Code)
	var resp OrderResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, int64(2500), resp.Amount)
	assert.Equal(t, "USD", resp.Currency)
	assert.Equal(t, StatusPending, resp.Status)
}

func TestHandler_CreateOrder_BadRequest(t *testing.T) {
	uid := shared.NewUserID()
	r := newTestRouter(t, &mockSvc{}, uid)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/orders",
		bytes.NewReader([]byte(`{"amount":-1,"currency":"USD"}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandler_PayOrder_PaymentRequired(t *testing.T) {
	uid := shared.NewUserID()
	r := newTestRouter(t, &mockSvc{getErr: ErrPaymentFailed}, uid)

	oid := shared.NewOrderID()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/orders/"+oid.String()+"/pay", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusPaymentRequired, w.Code)
}

func TestHandler_GetOrder_NotFound(t *testing.T) {
	uid := shared.NewUserID()
	r := newTestRouter(t, &mockSvc{getErr: ErrOrderNotFound}, uid)

	oid := shared.NewOrderID()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/orders/"+oid.String(), nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}
