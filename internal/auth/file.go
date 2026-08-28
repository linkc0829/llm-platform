package auth

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"sync"

	"go.uber.org/zap"

	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/shared"
)

const (
	// MaxAuthFileSize bounds startup parsing before JSON is decoded.
	MaxAuthFileSize int64 = 1 << 20
	// MaxPrincipals bounds the in-memory authorization snapshot.
	MaxPrincipals = 1000
)

// Store resolves bearer tokens against a validated in-memory snapshot. The
// snapshot is safe for concurrent Resolve calls and is intentionally not
// exposed for mutation.
type Store struct {
	mu         sync.RWMutex
	principals []Record
	path       string
	logger     *zap.Logger
}

// LoadFile reads and validates an auth snapshot. A missing path, malformed
// file, invalid principal, empty principal set, or oversized file is an error;
// callers must not start a network listener with an invalid snapshot.
func LoadFile(path string, logger *zap.Logger) (*Store, error) {
	return loadFile(path, logger, false)
}

// LoadBootstrapFile validates an auth file for the local bootstrap command and
// permits an empty principal set. The HTTP service must use LoadFile instead.
func LoadBootstrapFile(path string, logger *zap.Logger) (*Store, error) {
	return loadFile(path, logger, true)
}

func loadFile(path string, logger *zap.Logger, allowEmpty bool) (*Store, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("auth file path is required")
	}

	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat auth file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("auth file is not a regular file: %s", path)
	}
	if info.Size() > MaxAuthFileSize {
		return nil, fmt.Errorf("auth file exceeds %d bytes: %s", MaxAuthFileSize, path)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 && logger != nil {
		// Unix mode bits provide no ACL signal on Windows, so this warning is
		// intentionally limited to Unix-like platforms.
		logger.Warn("auth file permissions are broader than 0600", zap.String("path", path))
	}

	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read auth file: %w", err)
	}
	if int64(len(contents)) > MaxAuthFileSize {
		return nil, fmt.Errorf("auth file exceeds %d bytes: %s", MaxAuthFileSize, path)
	}

	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	var document fileDocument
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("parse auth file: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return nil, errors.New("parse auth file: multiple JSON values")
		}
		return nil, fmt.Errorf("parse auth file: trailing data: %w", err)
	}
	if !allowEmpty && len(document.Principals) == 0 {
		return nil, errors.New("auth file must contain at least one principal")
	}
	if len(document.Principals) > MaxPrincipals {
		return nil, fmt.Errorf("auth file contains more than %d principals", MaxPrincipals)
	}

	principals := make([]Record, len(document.Principals))
	seenIDs := make(map[string]struct{}, len(document.Principals))
	seenNames := make(map[string]struct{}, len(document.Principals))
	for i, record := range document.Principals {
		if strings.TrimSpace(record.ID) == "" {
			return nil, fmt.Errorf("auth principal %d: id is required", i)
		}
		if _, exists := seenIDs[record.ID]; exists {
			return nil, fmt.Errorf("auth principal %d: duplicate id %q", i, record.ID)
		}
		seenIDs[record.ID] = struct{}{}
		if err := ValidateName(record.Name); err != nil {
			return nil, fmt.Errorf("auth principal %d: name: %w", i, err)
		}
		if _, exists := seenNames[record.Name]; exists {
			return nil, fmt.Errorf("auth principal %d: duplicate name %q", i, record.Name)
		}
		seenNames[record.Name] = struct{}{}
		if !validTokenHash(record.TokenSHA256) {
			return nil, fmt.Errorf("auth principal %d: token_sha256 must be a SHA-256 hex digest", i)
		}
		principals[i] = cloneRecord(record)
	}

	return &Store{principals: principals, path: path, logger: logger}, nil
}

// NewBootstrapStore creates an empty store for the explicit local bootstrap
// command. The HTTP service must always use LoadFile, which rejects empty
// principal sets at startup.
func NewBootstrapStore(path string, logger *zap.Logger) (*Store, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("auth file path is required")
	}
	return &Store{path: path, logger: logger}, nil
}

// Resolve returns the principal whose stored digest matches token.
func (s *Store) Resolve(ctx context.Context, token string) (shared.Principal, error) {
	if err := ctx.Err(); err != nil {
		return shared.Principal{}, err
	}

	digest := tokenDigest(token)
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, record := range s.principals {
		if verifyTokenDigest(digest, record.TokenSHA256) {
			return clonePrincipal(record.Principal), nil
		}
	}
	return shared.Principal{}, ErrInvalidToken
}

func validTokenHash(hash string) bool {
	if len(hash) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(hash)
	return err == nil && len(decoded) == 32
}

func cloneRecord(record Record) Record {
	record.Principal = clonePrincipal(record.Principal)
	return record
}

func cloneRecords(records []Record) []Record {
	cloned := make([]Record, len(records))
	for i, record := range records {
		cloned[i] = cloneRecord(record)
	}
	return cloned
}

func clonePrincipal(principal shared.Principal) shared.Principal {
	principal.Teams = append([]string(nil), principal.Teams...)
	return principal
}
