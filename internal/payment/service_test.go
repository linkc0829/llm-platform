package payment

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/shared"
)

type fakeRepo struct {
	saveErr   error
	updateErr error
	findErr   error
	saved     *Payment
	updated   *Payment
}

func (f *fakeRepo) Save(_ context.Context, p *Payment) error {
	f.saved = p
	return f.saveErr
}
func (f *fakeRepo) Update(_ context.Context, p *Payment) error {
	f.updated = p
	return f.updateErr
}
func (f *fakeRepo) FindByID(_ context.Context, _ shared.PaymentID) (*Payment, error) {
	return f.saved, f.findErr
}
func (f *fakeRepo) FindByOrderID(_ context.Context, _ shared.OrderID) (*Payment, error) {
	return f.saved, f.findErr
}

type fakeGateway struct {
	approved bool
	err      error
}

func (f *fakeGateway) Charge(_ context.Context, _ ChargeRequest) (ChargeResult, error) {
	return ChargeResult{Approved: f.approved, ProviderRef: "ref"}, f.err
}

func TestService_Charge(t *testing.T) {
	uid := shared.NewUserID()
	oid := shared.NewOrderID()
	amount := shared.MustNewMoney(1000, "USD")

	tests := []struct {
		name        string
		gateway     *fakeGateway
		repoSaveErr error
		wantErr     error
		wantStatus  Status
	}{
		{
			name:       "approved",
			gateway:    &fakeGateway{approved: true},
			wantStatus: StatusSucceeded,
		},
		{
			name:       "declined",
			gateway:    &fakeGateway{approved: false},
			wantErr:    ErrGatewayDeclined,
			wantStatus: StatusFailed,
		},
		{
			name:       "gateway_error",
			gateway:    &fakeGateway{err: errors.New("network")},
			wantErr:    errors.New("network"),
			wantStatus: StatusFailed,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepo{saveErr: tt.repoSaveErr}
			svc := NewService(repo, tt.gateway)

			err := svc.Charge(context.Background(), uid, oid, amount)
			if tt.wantErr != nil {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantStatus, repo.updated.Status())
		})
	}
}
