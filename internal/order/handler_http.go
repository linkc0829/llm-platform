package order

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/platform/auth"
	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/shared"
)

type service interface {
	Create(ctx context.Context, in CreateInput) (*Order, error)
	Pay(ctx context.Context, id shared.OrderID, requesterID shared.UserID) (*Order, error)
	Cancel(ctx context.Context, id shared.OrderID, requesterID shared.UserID) (*Order, error)
	Get(ctx context.Context, id shared.OrderID, requesterID shared.UserID) (*Order, error)
	List(ctx context.Context, userID shared.UserID, p shared.Pagination) ([]*Order, int64, error)
}

type Handler struct {
	svc service
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// ----------------------------------------------------------------------------
// POST /api/v1/orders
// ----------------------------------------------------------------------------

func (h *Handler) create(c *gin.Context) {
	uid, ok := mustUserID(c)
	if !ok {
		return
	}

	var req CreateOrderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	o, err := h.svc.Create(ctx, CreateInput{
		UserID: uid, Amount: req.Amount, Currency: req.Currency,
	})
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusCreated, toOrderResponse(o))
}

// ----------------------------------------------------------------------------
// POST /api/v1/orders/:id/pay
// ----------------------------------------------------------------------------

func (h *Handler) pay(c *gin.Context) {
	uid, ok := mustUserID(c)
	if !ok {
		return
	}
	oid, err := shared.ParseOrderID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid order id"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	o, err := h.svc.Pay(ctx, oid, uid)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, toOrderResponse(o))
}

// ----------------------------------------------------------------------------
// POST /api/v1/orders/:id/cancel
// ----------------------------------------------------------------------------

func (h *Handler) cancel(c *gin.Context) {
	uid, ok := mustUserID(c)
	if !ok {
		return
	}
	oid, err := shared.ParseOrderID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid order id"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	o, err := h.svc.Cancel(ctx, oid, uid)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, toOrderResponse(o))
}

// ----------------------------------------------------------------------------
// GET /api/v1/orders/:id
// ----------------------------------------------------------------------------

func (h *Handler) get(c *gin.Context) {
	uid, ok := mustUserID(c)
	if !ok {
		return
	}
	oid, err := shared.ParseOrderID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid order id"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()

	o, err := h.svc.Get(ctx, oid, uid)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, toOrderResponse(o))
}

// ----------------------------------------------------------------------------
// GET /api/v1/orders
// ----------------------------------------------------------------------------

func (h *Handler) list(c *gin.Context) {
	uid, ok := mustUserID(c)
	if !ok {
		return
	}
	var q ListOrdersRequest
	_ = c.ShouldBindQuery(&q)

	p := shared.NewPagination(q.Limit, q.Offset)

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	orders, total, err := h.svc.List(ctx, uid, p)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, ListOrdersResponse{
		Items:  toOrderResponses(orders),
		Total:  total,
		Limit:  p.Limit,
		Offset: p.Offset,
	})
}

// ----------------------------------------------------------------------------
// Helpers
// ----------------------------------------------------------------------------

func mustUserID(c *gin.Context) (shared.UserID, bool) {
	sub := auth.UserIDFromContext(c)
	if sub == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
		return shared.UserID{}, false
	}
	id, err := shared.ParseUserID(sub)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid subject"})
		return shared.UserID{}, false
	}
	return id, true
}

func writeError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrOrderNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "order not found"})
	case errors.Is(err, ErrUserNotFound):
		c.JSON(http.StatusBadRequest, gin.H{"error": "user not found"})
	case errors.Is(err, ErrInvalidAmount),
		errors.Is(err, ErrInvalidUserID),
		errors.Is(err, ErrInvalidStatusTransition):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case errors.Is(err, ErrPaymentFailed):
		c.JSON(http.StatusPaymentRequired, gin.H{"error": "payment failed"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
	}
}
