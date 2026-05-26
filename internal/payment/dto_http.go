package payment

import "github.com/linkc0829/go-knowledge-base-qa-bot/internal/shared"

type PaymentResponse struct {
	ID        shared.PaymentID `json:"id"`
	UserID    shared.UserID    `json:"user_id"`
	OrderID   shared.OrderID   `json:"order_id"`
	Amount    int64            `json:"amount"`
	Currency  string           `json:"currency"`
	Status    Status           `json:"status"`
	CreatedAt string           `json:"created_at"`
	UpdatedAt string           `json:"updated_at"`
}

func toPaymentResponse(p *Payment) PaymentResponse {
	return PaymentResponse{
		ID:        p.ID(),
		UserID:    p.UserID(),
		OrderID:   p.OrderID(),
		Amount:    p.Amount().Amount(),
		Currency:  p.Amount().Currency(),
		Status:    p.Status(),
		CreatedAt: p.CreatedAt().Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt: p.UpdatedAt().Format("2006-01-02T15:04:05Z07:00"),
	}
}
