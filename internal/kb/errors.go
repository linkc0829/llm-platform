package kb

import "errors"

var (
	ErrNotIndexed = errors.New("knowledge base not indexed yet")
	ErrEmptyQuery = errors.New("query is required")
)
