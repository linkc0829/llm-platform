package contract

import (
	"context"
	"sync"

	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/order"
	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/payment"
	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/shared"
	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/user"
)

// ---------------------------------------------------------------------------
// User stubs
// ---------------------------------------------------------------------------

type inMemoryUserRepo struct {
	mu      sync.Mutex
	byID    map[shared.UserID]*user.User
	byEmail map[string]*user.User
}

func newInMemoryUserRepo() *inMemoryUserRepo {
	return &inMemoryUserRepo{
		byID:    map[shared.UserID]*user.User{},
		byEmail: map[string]*user.User{},
	}
}

func (r *inMemoryUserRepo) Save(_ context.Context, u *user.User) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byID[u.ID()] = u
	r.byEmail[u.Email()] = u
	return nil
}

func (r *inMemoryUserRepo) FindByID(_ context.Context, id shared.UserID) (*user.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	u, ok := r.byID[id]
	if !ok {
		return nil, user.ErrUserNotFound
	}
	return u, nil
}

func (r *inMemoryUserRepo) FindByEmail(_ context.Context, email string) (*user.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	u, ok := r.byEmail[email]
	if !ok {
		return nil, user.ErrUserNotFound
	}
	return u, nil
}

type fakeHasher struct{}

func (fakeHasher) Hash(plain string) (string, error) { return "hash:" + plain, nil }
func (fakeHasher) Compare(hashed, plain string) error {
	if hashed != "hash:"+plain {
		return user.ErrInvalidCredentials
	}
	return nil
}

type fakeTokenIssuer struct{}

func (fakeTokenIssuer) Issue(subject string) (string, error) { return "token:" + subject, nil }

// ---------------------------------------------------------------------------
// Order stubs
// ---------------------------------------------------------------------------

type inMemoryOrderRepo struct {
	mu sync.Mutex
	m  map[shared.OrderID]*order.Order
}

func newInMemoryOrderRepo() *inMemoryOrderRepo {
	return &inMemoryOrderRepo{m: map[shared.OrderID]*order.Order{}}
}

func (r *inMemoryOrderRepo) Save(_ context.Context, o *order.Order) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.m[o.ID()] = o
	return nil
}

func (r *inMemoryOrderRepo) Update(_ context.Context, o *order.Order) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.m[o.ID()] = o
	return nil
}

func (r *inMemoryOrderRepo) FindByID(_ context.Context, id shared.OrderID) (*order.Order, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	o, ok := r.m[id]
	if !ok {
		return nil, order.ErrOrderNotFound
	}
	return o, nil
}

func (r *inMemoryOrderRepo) ListByUser(_ context.Context, userID shared.UserID, _ shared.Pagination) ([]*order.Order, int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*order.Order
	for _, o := range r.m {
		if o.UserID() == userID {
			out = append(out, o)
		}
	}
	return out, int64(len(out)), nil
}

// ---------------------------------------------------------------------------
// Payment stubs
// ---------------------------------------------------------------------------

type inMemoryPaymentRepo struct {
	mu      sync.Mutex
	byID    map[shared.PaymentID]*payment.Payment
	byOrder map[shared.OrderID]*payment.Payment
}

func newInMemoryPaymentRepo() *inMemoryPaymentRepo {
	return &inMemoryPaymentRepo{
		byID:    map[shared.PaymentID]*payment.Payment{},
		byOrder: map[shared.OrderID]*payment.Payment{},
	}
}

func (r *inMemoryPaymentRepo) Save(_ context.Context, p *payment.Payment) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byID[p.ID()] = p
	r.byOrder[p.OrderID()] = p
	return nil
}

func (r *inMemoryPaymentRepo) Update(_ context.Context, p *payment.Payment) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byID[p.ID()] = p
	r.byOrder[p.OrderID()] = p
	return nil
}

func (r *inMemoryPaymentRepo) FindByID(_ context.Context, id shared.PaymentID) (*payment.Payment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.byID[id]
	if !ok {
		return nil, payment.ErrPaymentNotFound
	}
	return p, nil
}

func (r *inMemoryPaymentRepo) FindByOrderID(_ context.Context, orderID shared.OrderID) (*payment.Payment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.byOrder[orderID]
	if !ok {
		return nil, payment.ErrPaymentNotFound
	}
	return p, nil
}

type approvingGateway struct{}

func (approvingGateway) Charge(_ context.Context, req payment.ChargeRequest) (payment.ChargeResult, error) {
	return payment.ChargeResult{ProviderRef: "stub:" + req.PaymentID.String(), Approved: true}, nil
}
