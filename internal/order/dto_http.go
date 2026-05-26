package order

import (
	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/shared"
)

// ----------------------------------------------------------------------------
// Requests
// ----------------------------------------------------------------------------

type CreateOrderRequest struct {
	Amount   int64  `json:"amount" binding:"required,gt=0"`
	Currency string `json:"currency" binding:"required,len=3|min=3,max=10"` // USD, BTC, USDT...
}

type ListOrdersRequest struct {
	Limit  int `form:"limit"`
	Offset int `form:"offset"`
}

// ----------------------------------------------------------------------------
// Responses
// ----------------------------------------------------------------------------

type OrderResponse struct {
	ID        shared.OrderID `json:"id"`
	UserID    shared.UserID  `json:"user_id"`
	Amount    int64          `json:"amount"`
	Currency  string         `json:"currency"`
	Status    Status         `json:"status"`
	CreatedAt string         `json:"created_at"`
	UpdatedAt string         `json:"updated_at"`
}

type ListOrdersResponse = shared.Page[OrderResponse]

func toOrderResponse(o *Order) OrderResponse {
	return OrderResponse{
		ID:        o.ID(),
		UserID:    o.UserID(),
		Amount:    o.Amount().Amount(),
		Currency:  o.Amount().Currency(),
		Status:    o.Status(),
		CreatedAt: o.CreatedAt().Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt: o.UpdatedAt().Format("2006-01-02T15:04:05Z07:00"),
	}
}

func toOrderResponses(os []*Order) []OrderResponse {
	out := make([]OrderResponse, len(os))
	for i, o := range os {
		out[i] = toOrderResponse(o)
	}
	return out
}
