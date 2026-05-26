package payment

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/shared"
)

type service interface {
	Get(ctx context.Context, id shared.PaymentID) (*Payment, error)
	GetByOrder(ctx context.Context, orderID shared.OrderID) (*Payment, error)
}

type Handler struct {
	svc service
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// GET /api/v1/payments/:id
func (h *Handler) get(c *gin.Context) {
	id, err := shared.ParsePaymentID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid payment id"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()

	p, err := h.svc.Get(ctx, id)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, toPaymentResponse(p))
}

// GET /api/v1/payments/by-order/:order_id
func (h *Handler) getByOrder(c *gin.Context) {
	oid, err := shared.ParseOrderID(c.Param("order_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid order id"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()

	p, err := h.svc.GetByOrder(ctx, oid)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, toPaymentResponse(p))
}

func writeError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrPaymentNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "payment not found"})
	case errors.Is(err, ErrInvalidInput), errors.Is(err, ErrInvalidAmount):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case errors.Is(err, ErrGatewayDeclined):
		c.JSON(http.StatusPaymentRequired, gin.H{"error": "payment declined"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
	}
}
