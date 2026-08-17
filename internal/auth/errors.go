package auth

import "errors"

// ErrInvalidToken indicates that no stored principal matches the bearer token.
var ErrInvalidToken = errors.New("invalid token")

// Errors returned by token management operations.
var (
	ErrPrincipalNameExists    = errors.New("principal name already exists")
	ErrPrincipalNotFound      = errors.New("principal not found")
	ErrPrincipalLimit         = errors.New("principal limit exceeded")
	ErrAuthSnapshotTooLarge   = errors.New("auth snapshot exceeds size limit")
	ErrAdminTokenProtected    = errors.New("admin principal cannot be changed by the API")
	ErrNotAdminPrincipal      = errors.New("principal is not an admin")
	ErrLastAdminRequiresForce = errors.New("revoking the last admin requires force")
)
