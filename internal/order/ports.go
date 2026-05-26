package order

import (
	"context"

	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/shared"
)

// ----------------------------------------------------------------------------
// Outbound port: persistence
// ----------------------------------------------------------------------------

//go:generate mockgen -source=ports.go -destination=mocks/mock_ports.go -package=mocks
type Repo interface {
	Save(ctx context.Context, o *Order) error
	Update(ctx context.Context, o *Order) error
	FindByID(ctx context.Context, id shared.OrderID) (*Order, error)
	ListByUser(ctx context.Context, userID shared.UserID, p shared.Pagination) ([]*Order, int64, error)
}

// ----------------------------------------------------------------------------
// Cross-feature ports
//
// These are how `order` consumes capabilities from `user` and `payment`
// without importing those packages. The bootstrap layer wires the other
// features' services into these ports via Go's structural typing.
// ----------------------------------------------------------------------------

// UserLookup is satisfied by user.Service.GetByID. We only need to know the
// user exists — no field access — so the interface returns no data.
type UserLookup interface {
	Exists(ctx context.Context, id shared.UserID) (bool, error)
}

// PaymentCharger is satisfied by payment.Service.Charge. The order package
// does not know that "payment" exists as a feature — it only knows that
// something can charge money.
type PaymentCharger interface {
	Charge(ctx context.Context, userID shared.UserID, orderID shared.OrderID, amount shared.Money) error
}
