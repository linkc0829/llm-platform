package auth

import (
	"context"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/linkc0829/llm-platform/internal/shared"
)

const principalContextKey = "auth.principal"

// Resolver verifies a bearer token and returns its immutable principal.
type Resolver interface {
	Resolve(ctx context.Context, token string) (shared.Principal, error)
}

// RequirePrincipal authenticates the Authorization bearer token and stores the
// resulting principal in the Gin request context. It never emits an OAuth
// challenge because this service does not implement OAuth discovery or flow.
func RequirePrincipal(resolver Resolver) gin.HandlerFunc {
	return func(c *gin.Context) {
		token, ok := bearerToken(c.GetHeader("Authorization"))
		if !ok || resolver == nil {
			abortUnauthorized(c)
			return
		}

		principal, err := resolver.Resolve(c.Request.Context(), token)
		if err != nil {
			abortUnauthorized(c)
			return
		}
		c.Set(principalContextKey, principal)
		c.Next()
	}
}

// RequireIndexer allows only principals explicitly granted the indexer
// capability. Admin does not imply indexer.
func RequireIndexer() gin.HandlerFunc {
	return requireCapability(func(principal shared.Principal) bool {
		return principal.Indexer
	})
}

// RequireAdmin allows only principals explicitly granted the admin capability.
func RequireAdmin() gin.HandlerFunc {
	return requireCapability(func(principal shared.Principal) bool {
		return principal.Admin
	})
}

// PrincipalFromContext returns the authenticated principal stored by
// RequirePrincipal, if the request passed through that middleware.
func PrincipalFromContext(c *gin.Context) (shared.Principal, bool) {
	value, ok := c.Get(principalContextKey)
	if !ok {
		return shared.Principal{}, false
	}
	principal, ok := value.(shared.Principal)
	return principal, ok
}

// PrincipalIDFromContext returns the immutable ID of the authenticated
// principal, or an empty ID for the explicit local unauthenticated mode.
func PrincipalIDFromContext(c *gin.Context) string {
	principal, ok := PrincipalFromContext(c)
	if !ok {
		return ""
	}
	return principal.ID
}

func requireCapability(allowed func(shared.Principal) bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		principal, ok := PrincipalFromContext(c)
		if !ok {
			abortUnauthorized(c)
			return
		}
		if !allowed(principal) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
			return
		}
		c.Next()
	}
}

func bearerToken(header string) (string, bool) {
	fields := strings.Fields(header)
	if len(fields) != 2 || !strings.EqualFold(fields[0], "bearer") {
		return "", false
	}
	return fields[1], true
}

func abortUnauthorized(c *gin.Context) {
	c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
}
