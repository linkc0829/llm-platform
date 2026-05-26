package order

import (
	"context"
	"fmt"

	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/shared"
)

type Service struct {
	repo    Repo
	users   UserLookup
	payment PaymentCharger
}

func NewService(repo Repo, users UserLookup, payment PaymentCharger) *Service {
	return &Service{repo: repo, users: users, payment: payment}
}

// CreateInput is the input to CreateOrder. Amount comes pre-validated
// (smallest currency unit, currency code).
type CreateInput struct {
	UserID   shared.UserID
	Amount   int64
	Currency string
}

// Create places an order in pending state. Caller must Pay() afterward.
func (s *Service) Create(ctx context.Context, in CreateInput) (*Order, error) {
	exists, err := s.users.Exists(ctx, in.UserID)
	if err != nil {
		return nil, fmt.Errorf("check user: %w", err)
	}
	if !exists {
		return nil, ErrUserNotFound
	}

	amount, err := shared.NewMoney(in.Amount, in.Currency)
	if err != nil {
		return nil, fmt.Errorf("build amount: %w", err)
	}

	o, err := NewOrder(in.UserID, amount)
	if err != nil {
		return nil, err
	}

	if err := s.repo.Save(ctx, o); err != nil {
		return nil, fmt.Errorf("save order: %w", err)
	}
	return o, nil
}

// Pay charges the user and marks the order paid. This is intentionally NOT
// transactional across services — production code would use the outbox
// pattern (see docs/adr/0002-outbox-roadmap.md when you add it).
func (s *Service) Pay(ctx context.Context, id shared.OrderID, requesterID shared.UserID) (*Order, error) {
	o, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if o.UserID() != requesterID {
		return nil, ErrOrderNotFound // do not leak existence to other users
	}

	if err := s.payment.Charge(ctx, o.UserID(), o.ID(), o.Amount()); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrPaymentFailed, err)
	}

	if err := o.MarkPaid(); err != nil {
		return nil, err
	}
	if err := s.repo.Update(ctx, o); err != nil {
		return nil, fmt.Errorf("update order: %w", err)
	}
	return o, nil
}

// Cancel cancels a pending order.
func (s *Service) Cancel(ctx context.Context, id shared.OrderID, requesterID shared.UserID) (*Order, error) {
	o, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if o.UserID() != requesterID {
		return nil, ErrOrderNotFound
	}
	if err := o.Cancel(); err != nil {
		return nil, err
	}
	if err := s.repo.Update(ctx, o); err != nil {
		return nil, fmt.Errorf("update order: %w", err)
	}
	return o, nil
}

// Get returns a single order, scoped to its owner.
func (s *Service) Get(ctx context.Context, id shared.OrderID, requesterID shared.UserID) (*Order, error) {
	o, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if o.UserID() != requesterID {
		return nil, ErrOrderNotFound
	}
	return o, nil
}

// List returns orders for the given user.
func (s *Service) List(ctx context.Context, userID shared.UserID, p shared.Pagination) ([]*Order, int64, error) {
	orders, total, err := s.repo.ListByUser(ctx, userID, p)
	if err != nil {
		return nil, 0, fmt.Errorf("list orders: %w", err)
	}
	return orders, total, nil
}
