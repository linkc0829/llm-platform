package kb

import "errors"

// AnonymousOwner identifies an explicitly unauthenticated local transport.
const AnonymousOwner = "anonymous"

var (
	ErrNotIndexed             = errors.New("knowledge base not indexed yet")
	ErrIndexStale             = errors.New("index was built with an older anchor scheme; re-run /index")
	ErrIndexAccessAuditFailed = errors.New("persisted index failed access audit")
	ErrVectorsIgnored         = errors.New("vector index was built with a different embedding model; re-run /index")
	ErrEmptyQuery             = errors.New("query is required")
	ErrInvalidSection         = errors.New("invalid section")
	ErrInvalidSectionAccess   = errors.New("invalid section access metadata")
	ErrSectionAccessDrift     = errors.New("section access metadata drift")
	ErrSessionOwnerMismatch   = errors.New("session owner mismatch")
	ErrSessionOwnerRequired   = errors.New("session owner is required")
)
