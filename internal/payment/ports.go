package payment

import (
	"context"

	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/shared"
)

// Repo persists Payment aggregates.
//
//go:generate mockgen -source=ports.go -destination=mocks/mock_ports.go -package=mocks
type Repo interface {
	Save(ctx context.Context, p *Payment) error
	Update(ctx context.Context, p *Payment) error
	FindByID(ctx context.Context, id shared.PaymentID) (*Payment, error)
	FindByOrderID(ctx context.Context, orderID shared.OrderID) (*Payment, error)
}

// Gateway is the outbound port to a payment provider (Stripe, Adyen, on-chain
// wallet, etc.). Decoupled so we can swap providers without touching service
// code.
type Gateway interface {
	Charge(ctx context.Context, req ChargeRequest) (ChargeResult, error)
}

// ChargeRequest is the gateway-agnostic charge payload.
type ChargeRequest struct {
	PaymentID shared.PaymentID
	UserID    shared.UserID
	Amount    shared.Money
}

// ChargeResult is what the gateway returns on a charge attempt.
type ChargeResult struct {
	ProviderRef string // external transaction id for reconciliation
	Approved    bool
}
