package auth

import "context"

// TokenManager is the capability needed by the admin HTTP adapter.
type TokenManager interface {
	ListTokens(ctx context.Context) ([]Record, error)
	CreateToken(ctx context.Context, actorID string, spec TokenSpec) (Record, string, error)
	DeleteToken(ctx context.Context, actorID, principalID string) error
}

var _ Resolver = (*Store)(nil)
var _ TokenManager = (*Store)(nil)
