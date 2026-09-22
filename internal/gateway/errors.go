package gateway

import "errors"

var (
	// ErrUnauthorized indicates missing or invalid bearer token credentials.
	ErrUnauthorized = errors.New("unauthorized")
	// ErrUserLimit indicates the effective user has exceeded their concurrent inflight quota.
	ErrUserLimit = errors.New("user concurrency limit exceeded")
	// ErrGlobalLimit indicates the entire gateway has reached maximum concurrent capacity.
	ErrGlobalLimit = errors.New("global concurrency limit exceeded")
	// ErrPayloadTooLarge indicates the request body exceeded maximum permissible size.
	ErrPayloadTooLarge = errors.New("payload too large")
)
