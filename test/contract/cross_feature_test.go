// Package contract holds tests that lock in cross-feature wiring contracts.
//
// These are not integration tests — they don't touch a real database. They
// exist to catch *signature drift* across feature boundaries. The whole point
// of the cross-feature port pattern (see ADR 0001) is that feature A defines
// the shape of what it needs in its own ports.go, and feature B's service
// must structurally satisfy that shape. If someone changes B's signature
// without realizing A depends on it, the wiring breaks at startup — that
// kind of failure should be a test failure, not a 3 AM bug report.
//
// What's tested here:
//
//  1. payment.Service satisfies order.PaymentCharger structurally (compile-time).
//  2. The userLookupAdapter pattern (B.Service → A.Port) works end-to-end.
//  3. The order.Create → order.Pay flow drives both cross-feature ports
//     correctly with stub repos / gateway.
//
// Lives under test/ (not internal/) so it can legitimately import multiple
// feature packages — the only place in the repo that does.
package contract

import (
	"context"
	"errors"
	"testing"

	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/order"
	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/payment"
	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/shared"
	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/user"
)

// ---------------------------------------------------------------------------
// 1. Compile-time interface satisfaction.
//
// If any of these stops compiling, a feature's exported API changed in a way
// that breaks the cross-feature contract. The fix is either:
//   - revert the signature change, or
//   - update the consuming feature's port AND its tests.
// ---------------------------------------------------------------------------

var _ order.PaymentCharger = (*payment.Service)(nil)

// user.Service.GetByID returns *User, but order.UserLookup wants a bool. The
// adapter in bootstrap/wire.go bridges these. We re-declare the adapter here
// rather than importing it because `bootstrap` pulls in pgx/redis/gin and
// makes this test heavy. The duplication is deliberate but has a cost: if
// the production adapter grows logging or tracing, this copy won't reflect
// that. The contract being locked in is the *signature compatibility*, not
// the production adapter's full behavior — that's all this test claims to
// verify.
type userLookupAdapter struct{ svc *user.Service }

func (a userLookupAdapter) Exists(ctx context.Context, id shared.UserID) (bool, error) {
	u, err := a.svc.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, user.ErrUserNotFound) {
			return false, nil
		}
		return false, err
	}
	return u != nil, nil
}

var _ order.UserLookup = userLookupAdapter{}

// ---------------------------------------------------------------------------
// 2. End-to-end happy path with in-memory stubs.
//
// Order.Create must call UserLookup; Order.Pay must call PaymentCharger.
// Both ports are wired to real feature services (with stub I/O underneath).
// ---------------------------------------------------------------------------

func TestOrderFlow_CrossFeatureWiring(t *testing.T) {
	ctx := context.Background()

	// --- User feature wired to in-memory repo + fake hasher/tokens --------
	userRepo := newInMemoryUserRepo()
	userSvc := user.NewService(userRepo, fakeHasher{}, fakeTokenIssuer{})

	registered, _, err := userSvc.Register(ctx, user.RegisterInput{
		Email:       "alice@example.com",
		Password:    "password123",
		DisplayName: "Alice",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	// --- Payment feature wired to in-memory repo + approving gateway -----
	paymentRepo := newInMemoryPaymentRepo()
	gateway := approvingGateway{}
	paymentSvc := payment.NewService(paymentRepo, gateway)

	// --- Order feature wired to in-memory repo + the two cross-feature ports
	orderRepo := newInMemoryOrderRepo()
	orderSvc := order.NewService(orderRepo, userLookupAdapter{svc: userSvc}, paymentSvc)

	// --- Flow ------------------------------------------------------------
	o, err := orderSvc.Create(ctx, order.CreateInput{
		UserID:   registered.ID(),
		Amount:   2500,
		Currency: "USD",
	})
	if err != nil {
		t.Fatalf("create order: %v", err)
	}
	if o.UserID() != registered.ID() {
		t.Errorf("order user id mismatch: got %v want %v", o.UserID(), registered.ID())
	}

	paid, err := orderSvc.Pay(ctx, o.ID(), registered.ID())
	if err != nil {
		t.Fatalf("pay order: %v", err)
	}
	if paid.Status() != order.StatusPaid {
		t.Errorf("expected order status %q, got %q", order.StatusPaid, paid.Status())
	}

	// Sanity: a payment row was created via the cross-feature charge.
	p, err := paymentSvc.GetByOrder(ctx, o.ID())
	if err != nil {
		t.Fatalf("get payment by order: %v", err)
	}
	if p.Status() != payment.StatusSucceeded {
		t.Errorf("expected payment status %q, got %q", payment.StatusSucceeded, p.Status())
	}
}

// TestOrderFlow_UnknownUser asserts the cross-feature UserLookup actually
// gates order creation. If wiring regresses (e.g. someone hardcodes
// Exists → true), this test fails.
func TestOrderFlow_UnknownUser(t *testing.T) {
	ctx := context.Background()

	userRepo := newInMemoryUserRepo()
	userSvc := user.NewService(userRepo, fakeHasher{}, fakeTokenIssuer{})

	paymentRepo := newInMemoryPaymentRepo()
	paymentSvc := payment.NewService(paymentRepo, approvingGateway{})

	orderRepo := newInMemoryOrderRepo()
	orderSvc := order.NewService(orderRepo, userLookupAdapter{svc: userSvc}, paymentSvc)

	_, err := orderSvc.Create(ctx, order.CreateInput{
		UserID:   shared.NewUserID(), // never registered
		Amount:   100,
		Currency: "USD",
	})
	if !errors.Is(err, order.ErrUserNotFound) {
		t.Fatalf("expected ErrUserNotFound, got %v", err)
	}
}
