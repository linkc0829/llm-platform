// Package httperr centralizes the HTTP error response envelope so every
// handler writes the same shape: {"error": "<message>"}.
//
// Domain → HTTP status mapping stays in each feature's handler (the mapping
// is feature-specific and must not leak into the platform layer). This
// package only owns the response envelope and a few convenience helpers
// for common statuses.
//
// # Aborts the gin chain
//
// Every helper in this package calls c.AbortWithStatusJSON, which prevents
// subsequent handlers in the gin chain from running. This is the desired
// behavior for error responses — you don't want a downstream handler to
// write a second body or override the status. But it does differ from a
// plain c.JSON call: middleware registered after the handler still runs
// (logging, recovery, etc.) and sees IsAborted()==true. Tracing or audit
// middleware that branches on abort status should be aware of this.
//
// Typical usage from a handler:
//
//	switch {
//	case errors.Is(err, ErrOrderNotFound):
//	    httperr.NotFound(c, "order not found")
//	case errors.Is(err, ErrInvalidAmount):
//	    httperr.BadRequest(c, err.Error())
//	default:
//	    httperr.Internal(c, "internal error")
//	}
package httperr

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Response is the canonical error envelope returned by every handler.
type Response struct {
	Error string `json:"error"`
	Code  string `json:"code,omitempty"`
}

// JSON writes an arbitrary status with the canonical envelope.
func JSON(c *gin.Context, status int, msg string) {
	c.AbortWithStatusJSON(status, Response{Error: msg})
}

// JSONWithCode writes an arbitrary status with a machine-readable code in
// addition to the human message.
func JSONWithCode(c *gin.Context, status int, code, msg string) {
	c.AbortWithStatusJSON(status, Response{Error: msg, Code: code})
}

func BadRequest(c *gin.Context, msg string)     { JSON(c, http.StatusBadRequest, msg) }
func Unauthorized(c *gin.Context, msg string)   { JSON(c, http.StatusUnauthorized, msg) }
func Forbidden(c *gin.Context, msg string)      { JSON(c, http.StatusForbidden, msg) }
func NotFound(c *gin.Context, msg string)       { JSON(c, http.StatusNotFound, msg) }
func Conflict(c *gin.Context, msg string)       { JSON(c, http.StatusConflict, msg) }
func PaymentRequired(c *gin.Context, msg string){ JSON(c, http.StatusPaymentRequired, msg) }
func Internal(c *gin.Context, msg string)       { JSON(c, http.StatusInternalServerError, msg) }
