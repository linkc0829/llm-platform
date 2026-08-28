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

	// ErrLLMRateLimited and ErrLLMUnavailable separate "come back later" from
	// "this request is broken". Collapsing both into a bare 500 cost a
	// 606-question acceptance run 30 skips: the upstream returned 429 for two
	// hours while the handler reported 500 with no Retry-After, so the eval
	// runner fell back to 1s/2s exponential backoff and burned all three
	// attempts inside the same exhausted per-minute quota window. 500 was
	// already retryable there; what was missing was the upstream's own hint
	// about how long to wait. The distinction exists for that caller, not for us.
	ErrLLMRateLimited = errors.New("llm upstream rate limited")
	ErrLLMUnavailable = errors.New("llm upstream temporarily unavailable")
)

// LLMTransientError marks an LLM failure worth retrying and carries the
// upstream backoff hint so the HTTP layer can echo it verbatim. RetryAfter is
// empty when the upstream sent none — an absent header is honest, a guessed
// number is not.
type LLMTransientError struct {
	Kind       error // ErrLLMRateLimited or ErrLLMUnavailable
	RetryAfter string
	cause      error
}

func NewLLMTransientError(kind error, retryAfter string, cause error) *LLMTransientError {
	return &LLMTransientError{Kind: kind, RetryAfter: retryAfter, cause: cause}
}

func (e *LLMTransientError) Error() string { return e.cause.Error() }

// Unwrap returns both the class sentinel and the original cause so errors.Is
// matches the sentinel while logs keep the upstream detail.
func (e *LLMTransientError) Unwrap() []error { return []error{e.Kind, e.cause} }

// RetryAfterOf returns the upstream backoff hint carried by err, if any.
func RetryAfterOf(err error) string {
	var transient *LLMTransientError
	if errors.As(err, &transient) {
		return transient.RetryAfter
	}
	return ""
}
