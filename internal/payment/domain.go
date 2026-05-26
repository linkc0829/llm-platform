// Package payment is a feature slice for charging payments. In production this
// would talk to Stripe / Adyen / a crypto gateway via the Gateway port; here
// we ship a stub gateway that always succeeds, so the template runs locally.
package payment

import (
	"time"

	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/shared"
)

type Status string

const (
	StatusPending   Status = "pending"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
)

// Payment is the aggregate root.
type Payment struct {
	id        shared.PaymentID
	userID    shared.UserID
	orderID   shared.OrderID
	amount    shared.Money
	status    Status
	createdAt time.Time
	updatedAt time.Time
}

func NewPayment(userID shared.UserID, orderID shared.OrderID, amount shared.Money) (*Payment, error) {
	if userID.IsZero() || orderID.IsZero() {
		return nil, ErrInvalidInput
	}
	if amount.IsZero() {
		return nil, ErrInvalidAmount
	}
	now := time.Now().UTC()
	return &Payment{
		id:        shared.NewPaymentID(),
		userID:    userID,
		orderID:   orderID,
		amount:    amount,
		status:    StatusPending,
		createdAt: now,
		updatedAt: now,
	}, nil
}

func rehydrate(id shared.PaymentID, userID shared.UserID, orderID shared.OrderID,
	amount shared.Money, status Status, createdAt, updatedAt time.Time) *Payment {
	return &Payment{
		id: id, userID: userID, orderID: orderID, amount: amount, status: status,
		createdAt: createdAt, updatedAt: updatedAt,
	}
}

func (p *Payment) MarkSucceeded() {
	p.status = StatusSucceeded
	p.updatedAt = time.Now().UTC()
}

func (p *Payment) MarkFailed() {
	p.status = StatusFailed
	p.updatedAt = time.Now().UTC()
}

func (p *Payment) ID() shared.PaymentID    { return p.id }
func (p *Payment) UserID() shared.UserID   { return p.userID }
func (p *Payment) OrderID() shared.OrderID { return p.orderID }
func (p *Payment) Amount() shared.Money    { return p.amount }
func (p *Payment) Status() Status          { return p.status }
func (p *Payment) CreatedAt() time.Time    { return p.createdAt }
func (p *Payment) UpdatedAt() time.Time    { return p.updatedAt }
