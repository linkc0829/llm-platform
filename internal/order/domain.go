// Package order is a feature slice for order management.
package order

import (
	"time"

	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/shared"
)

// Status is the lifecycle state of an order.
type Status string

const (
	StatusPending  Status = "pending"
	StatusPaid     Status = "paid"
	StatusCanceled Status = "canceled"
)

// Order is the aggregate root.
type Order struct {
	id        shared.OrderID
	userID    shared.UserID
	amount    shared.Money
	status    Status
	createdAt time.Time
	updatedAt time.Time
}

// NewOrder creates a pending order. Amount must be > 0 (free orders should
// not exist in this domain — change here if your business says otherwise).
func NewOrder(userID shared.UserID, amount shared.Money) (*Order, error) {
	if userID.IsZero() {
		return nil, ErrInvalidUserID
	}
	if amount.IsZero() {
		return nil, ErrInvalidAmount
	}
	now := time.Now().UTC()
	return &Order{
		id:        shared.NewOrderID(),
		userID:    userID,
		amount:    amount,
		status:    StatusPending,
		createdAt: now,
		updatedAt: now,
	}, nil
}

// rehydrate is for repo use only.
func rehydrate(id shared.OrderID, userID shared.UserID, amount shared.Money,
	status Status, createdAt, updatedAt time.Time) *Order {
	return &Order{
		id: id, userID: userID, amount: amount, status: status,
		createdAt: createdAt, updatedAt: updatedAt,
	}
}

// State transitions are domain methods — invariants live here, not in service.

func (o *Order) MarkPaid() error {
	if o.status != StatusPending {
		return ErrInvalidStatusTransition
	}
	o.status = StatusPaid
	o.updatedAt = time.Now().UTC()
	return nil
}

func (o *Order) Cancel() error {
	if o.status == StatusPaid {
		return ErrInvalidStatusTransition
	}
	o.status = StatusCanceled
	o.updatedAt = time.Now().UTC()
	return nil
}

func (o *Order) ID() shared.OrderID    { return o.id }
func (o *Order) UserID() shared.UserID { return o.userID }
func (o *Order) Amount() shared.Money  { return o.amount }
func (o *Order) Status() Status        { return o.status }
func (o *Order) CreatedAt() time.Time  { return o.createdAt }
func (o *Order) UpdatedAt() time.Time  { return o.updatedAt }
