package kb

import "errors"

var (
	ErrNotIndexed     = errors.New("knowledge base not indexed yet")
	ErrIndexStale     = errors.New("index was built with an older anchor scheme; re-run /index")
	ErrVectorsIgnored = errors.New("vector index was built with a different embedding model; re-run /index")
	ErrEmptyQuery     = errors.New("query is required")
	ErrInvalidSection = errors.New("invalid section")
)
