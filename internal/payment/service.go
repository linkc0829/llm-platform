package payment

import (
	"context"
	"errors"
	"fmt"

	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/shared"
)

type Service struct {
	repo    Repo
	gateway Gateway
}

func NewService(repo Repo, gateway Gateway) *Service {
	return &Service{repo: repo, gateway: gateway}
}

// Charge satisfies order.PaymentCharger structurally — `order` never imports
// this package; `bootstrap` injects this method as the implementation.
//
// Flow: create payment row (pending) → call gateway → mark succeeded/failed
// → persist final state. In production this should be wrapped in an outbox
// for at-least-once delivery; the template keeps it simple.
func (s *Service) Charge(ctx context.Context, userID shared.UserID, orderID shared.OrderID, amount shared.Money) error {
	p, err := NewPayment(userID, orderID, amount)
	if err != nil {
		return fmt.Errorf("build payment: %w", err)
	}
	if err := s.repo.Save(ctx, p); err != nil {
		return fmt.Errorf("save payment: %w", err)
	}

	res, err := s.gateway.Charge(ctx, ChargeRequest{
		PaymentID: p.ID(), UserID: userID, Amount: amount,
	})
	if err != nil || !res.Approved {
		p.MarkFailed()
		_ = s.repo.Update(ctx, p) // best-effort; the row stays as failed
		if err != nil {
			return fmt.Errorf("gateway charge: %w", err)
		}
		return ErrGatewayDeclined
	}

	p.MarkSucceeded()
	if err := s.repo.Update(ctx, p); err != nil {
		return fmt.Errorf("update payment: %w", err)
	}
	return nil
}

// Get returns a single payment by id.
func (s *Service) Get(ctx context.Context, id shared.PaymentID) (*Payment, error) {
	return s.repo.FindByID(ctx, id)
}

// GetByOrder returns the payment for a given order, or ErrPaymentNotFound.
func (s *Service) GetByOrder(ctx context.Context, orderID shared.OrderID) (*Payment, error) {
	p, err := s.repo.FindByOrderID(ctx, orderID)
	if err != nil {
		if errors.Is(err, ErrPaymentNotFound) {
			return nil, ErrPaymentNotFound
		}
		return nil, fmt.Errorf("find payment by order: %w", err)
	}
	return p, nil
}
