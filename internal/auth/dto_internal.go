package auth

import (
	"time"

	"github.com/linkc0829/llm-platform/internal/shared"
)

// Record is the on-disk representation of one principal. The token itself is
// never persisted; only its SHA-256 digest is stored.
type Record struct {
	shared.Principal
	CreatedAt   time.Time `json:"created_at,omitempty"`
	TokenSHA256 string    `json:"token_sha256"`
}

// TokenSpec contains capabilities that the network management API is allowed
// to grant. Admin is intentionally absent: only the local CLI can create an
// admin principal.
type TokenSpec struct {
	Name        string
	Teams       []string
	AllTeams    bool
	Engineering bool
	Indexer     bool
	Workload    string
}

type fileDocument struct {
	Principals []Record `json:"principals"`
}
