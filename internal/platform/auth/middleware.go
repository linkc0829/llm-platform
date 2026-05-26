package auth

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

const (
	ctxUserIDKey = "auth.userID"
	bearerPrefix = "Bearer "
)

// Middleware returns a Gin middleware that enforces JWT auth and stores the
// user id in the gin.Context for handlers to read via UserIDFromContext.
func Middleware(m *Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		if !strings.HasPrefix(header, bearerPrefix) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing bearer token"})
			return
		}
		raw := strings.TrimPrefix(header, bearerPrefix)
		claims, err := m.Verify(raw)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
			return
		}
		c.Set(ctxUserIDKey, claims.Subject)
		c.Next()
	}
}

// UserIDFromContext returns the authenticated user id, or empty string if none.
// Handlers should treat empty string as unauthenticated.
func UserIDFromContext(c *gin.Context) string {
	v, ok := c.Get(ctxUserIDKey)
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}
