package order

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/shared"
)

// ----------------------------------------------------------------------------
// Fakes
// ----------------------------------------------------------------------------

type fakeRepo struct {
	saveErr     error
	updateErr   error
	findErr     error
	findOrder   *Order
	listOrders  []*Order
	listTotal   int64
	listErr     error
	saveCalls   int
	updateCalls int
}

func (f *fakeRepo) Save(_ context.Context, _ *Order) error {
	f.saveCalls++
	return f.saveErr
}
func (f *fakeRepo) Update(_ context.Context, _ *Order) error {
	f.updateCalls++
	return f.updateErr
}
func (f *fakeRepo) FindByID(_ context.Context, _ shared.OrderID) (*Order, error) {
	return f.findOrder, f.findErr
}
func (f *fakeRepo) ListByUser(_ context.Context, _ shared.UserID, _ shared.Pagination) ([]*Order, int64, error) {
	return f.listOrders, f.listTotal, f.listErr
}

type fakeUserLookup struct {
	exists bool
	err    error
}

func (f *fakeUserLookup) Exists(_ context.Context, _ shared.UserID) (bool, error) {
	return f.exists, f.err
}

type fakeCharger struct {
	chargeErr  error
	chargeHits int
}

func (f *fakeCharger) Charge(_ context.Context, _ shared.UserID, _ shared.OrderID, _ shared.Money) error {
	f.chargeHits++
	return f.chargeErr
}

// ----------------------------------------------------------------------------
// Create
// ----------------------------------------------------------------------------

func TestService_Create(t *testing.T) {
	uid := shared.NewUserID()

	tests := []struct {
		name    string
		input   CreateInput
		setup   func(*fakeRepo, *fakeUserLookup)
		wantErr error
	}{
		{
			name:  "happy_path",
			input: CreateInput{UserID: uid, Amount: 1000, Currency: "USD"},
			setup: func(_ *fakeRepo, u *fakeUserLookup) { u.exists = true },
		},
		{
			name:    "unknown_user",
			input:   CreateInput{UserID: uid, Amount: 1000, Currency: "USD"},
			setup:   func(_ *fakeRepo, u *fakeUserLookup) { u.exists = false },
			wantErr: ErrUserNotFound,
		},
		{
			name:    "zero_amount_rejected",
			input:   CreateInput{UserID: uid, Amount: 0, Currency: "USD"},
			setup:   func(_ *fakeRepo, u *fakeUserLookup) { u.exists = true },
			wantErr: shared.ErrInvalidAmount,
		},
		{
			name:    "empty_currency_rejected",
			input:   CreateInput{UserID: uid, Amount: 100, Currency: ""},
			setup:   func(_ *fakeRepo, u *fakeUserLookup) { u.exists = true },
			wantErr: shared.ErrEmptyCurrency,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepo{}
			users := &fakeUserLookup{}
			tt.setup(repo, users)
			svc := NewService(repo, users, &fakeCharger{})

			o, err := svc.Create(context.Background(), tt.input)
			if tt.wantErr != nil {
				require.Error(t, err)
				assert.True(t, errors.Is(err, tt.wantErr), "want %v, got %v", tt.wantErr, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, StatusPending, o.Status())
			assert.Equal(t, 1, repo.saveCalls)
		})
	}
}

// ----------------------------------------------------------------------------
// Pay
// ----------------------------------------------------------------------------

func TestService_Pay(t *testing.T) {
	uid := shared.NewUserID()
	otherUID := shared.NewUserID()
	amount := shared.MustNewMoney(1000, "USD")
	pendingOrder, _ := NewOrder(uid, amount)

	tests := []struct {
		name        string
		setup       func(*fakeRepo, *fakeCharger)
		requesterID shared.UserID
		wantErr     error
		wantStatus  Status
	}{
		{
			name: "happy_path",
			setup: func(r *fakeRepo, _ *fakeCharger) {
				r.findOrder = pendingOrder
			},
			requesterID: uid,
			wantStatus:  StatusPaid,
		},
		{
			name: "not_owner_returns_not_found",
			setup: func(r *fakeRepo, _ *fakeCharger) {
				r.findOrder = pendingOrder
			},
			requesterID: otherUID,
			wantErr:     ErrOrderNotFound,
		},
		{
			name: "payment_fails",
			setup: func(r *fakeRepo, c *fakeCharger) {
				o, _ := NewOrder(uid, amount) // fresh pending order
				r.findOrder = o
				c.chargeErr = errors.New("gateway down")
			},
			requesterID: uid,
			wantErr:     ErrPaymentFailed,
		},
		{
			name: "find_returns_not_found",
			setup: func(r *fakeRepo, _ *fakeCharger) {
				r.findErr = ErrOrderNotFound
			},
			requesterID: uid,
			wantErr:     ErrOrderNotFound,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepo{}
			charger := &fakeCharger{}
			tt.setup(repo, charger)
			svc := NewService(repo, &fakeUserLookup{exists: true}, charger)

			o, err := svc.Pay(context.Background(), shared.NewOrderID(), tt.requesterID)
			if tt.wantErr != nil {
				require.Error(t, err)
				assert.True(t, errors.Is(err, tt.wantErr), "want %v, got %v", tt.wantErr, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantStatus, o.Status())
		})
	}
}

// ----------------------------------------------------------------------------
// Domain transitions
// ----------------------------------------------------------------------------

func TestOrder_StatusTransitions(t *testing.T) {
	uid := shared.NewUserID()
	amount := shared.MustNewMoney(1000, "USD")

	t.Run("pending_to_paid_ok", func(t *testing.T) {
		o, _ := NewOrder(uid, amount)
		require.NoError(t, o.MarkPaid())
		assert.Equal(t, StatusPaid, o.Status())
	})

	t.Run("cannot_pay_canceled", func(t *testing.T) {
		o, _ := NewOrder(uid, amount)
		require.NoError(t, o.Cancel())
		assert.ErrorIs(t, o.MarkPaid(), ErrInvalidStatusTransition)
	})

	t.Run("cannot_cancel_paid", func(t *testing.T) {
		o, _ := NewOrder(uid, amount)
		require.NoError(t, o.MarkPaid())
		assert.ErrorIs(t, o.Cancel(), ErrInvalidStatusTransition)
	})
}
