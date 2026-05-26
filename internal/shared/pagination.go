package shared

// Pagination is a cursor-less offset/limit pagination input used across features.
// For high-volume endpoints, prefer cursor-based pagination at the feature level.
type Pagination struct {
	Limit  int
	Offset int
}

const (
	defaultLimit = 20
	maxLimit     = 100
)

// NewPagination clamps limit and offset to safe bounds.
func NewPagination(limit, offset int) Pagination {
	if limit <= 0 {
		limit = defaultLimit
	}
	if limit > maxLimit {
		limit = maxLimit
	}
	if offset < 0 {
		offset = 0
	}
	return Pagination{Limit: limit, Offset: offset}
}

// Page is a generic page result wrapper.
type Page[T any] struct {
	Items  []T   `json:"items"`
	Total  int64 `json:"total"`
	Limit  int   `json:"limit"`
	Offset int   `json:"offset"`
}
