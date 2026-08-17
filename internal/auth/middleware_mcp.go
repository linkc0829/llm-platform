package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
)

const (
	principalExtraKey   = "principal"
	staticTokenLifetime = 100 * 365 * 24 * time.Hour
)

// NewMCPTokenVerifier adapts the shared resolver to the MCP SDK's bearer
// verifier. The static auth file has no token expiry, so the SDK receives a
// far-future expiration only to satisfy its required TokenInfo contract.
func NewMCPTokenVerifier(resolver Resolver) sdkauth.TokenVerifier {
	return func(ctx context.Context, token string, _ *http.Request) (*sdkauth.TokenInfo, error) {
		if resolver == nil {
			return nil, sdkauth.ErrInvalidToken
		}

		principal, err := resolver.Resolve(ctx, token)
		if err != nil {
			if errors.Is(err, ErrInvalidToken) {
				return nil, sdkauth.ErrInvalidToken
			}
			return nil, fmt.Errorf("resolve bearer token: %w", err)
		}

		return &sdkauth.TokenInfo{
			Expiration: time.Now().Add(staticTokenLifetime),
			UserID:     principal.ID,
			Extra: map[string]any{
				principalExtraKey: principal,
			},
		}, nil
	}
}

// RequireMCPBearerToken wraps an MCP HTTP handler with the same resolver used
// by the Gin routes. Options stay nil so invalid tokens return 401 without an
// OAuth challenge or protected-resource metadata flow.
func RequireMCPBearerToken(resolver Resolver, handler http.Handler) http.Handler {
	return sdkauth.RequireBearerToken(NewMCPTokenVerifier(resolver), nil)(handler)
}
