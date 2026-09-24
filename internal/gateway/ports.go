package gateway

import (
	"context"

	"github.com/linkc0829/llm-platform/internal/shared"
)

// TokenResolver verifies a bearer token and resolves its associated principal.
type TokenResolver interface {
	Resolve(ctx context.Context, token string) (shared.Principal, error)
	// Lookup finds a principal by ID; used to classify X-On-Behalf-Of users.
	Lookup(id string) (shared.Principal, bool)
}
