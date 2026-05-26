package payment

import (
	"context"

	"github.com/google/uuid"
)

// StubGateway is a development-only Gateway that always approves charges.
// Replace with a real implementation (Stripe, on-chain, etc.) for production.
//
// CLAUDE.md: when adding a real gateway, create a new file like
// `gateway_stripe.go`, do NOT modify this stub.
type StubGateway struct{}

func NewStubGateway() *StubGateway { return &StubGateway{} }

func (g *StubGateway) Charge(_ context.Context, req ChargeRequest) (ChargeResult, error) {
	return ChargeResult{
		ProviderRef: "stub_" + uuid.NewString(),
		Approved:    true,
	}, nil
}
