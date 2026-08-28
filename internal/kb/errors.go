package kb

import "errors"

// AnonymousOwner identifies an explicitly unauthenticated local transport.
const AnonymousOwner = "anonymous"

var (
	ErrNotIndexed                 = errors.New("knowledge base not indexed yet")
	ErrIndexStale                 = errors.New("index was built with an older anchor scheme; re-run /index")
	ErrIndexAccessAuditFailed     = errors.New("persisted index failed access audit")
	ErrVectorCacheFormat          = errors.New("vector cache format is not readable; run /index to rebuild it")
	ErrVectorsIgnored             = errors.New("vector cache was discarded (format or embedding identity changed); re-run /index")
	ErrEmptyQuery                 = errors.New("query is required")
	ErrInvalidSection             = errors.New("invalid section")
	ErrInvalidSectionAccess       = errors.New("invalid section access metadata")
	ErrSectionAccessDrift         = errors.New("section access metadata drift")
	ErrSessionOwnerMismatch       = errors.New("session owner mismatch")
	ErrSessionOwnerRequired       = errors.New("session owner is required")
	ErrDistilledReferenceNotFound = errors.New("distilled reference not found")
	ErrDistilledReferenceInvalid  = errors.New("invalid distilled reference")
)
