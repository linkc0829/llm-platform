package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/platform/atomicfile"
	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/platform/lockfile"
	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/shared"
)

// ListTokens returns a defensive copy of the current in-memory snapshot.
func (s *Store) ListTokens(ctx context.Context) ([]Record, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneRecords(s.principals), nil
}

// CreateToken creates a non-admin principal and persists the complete new
// snapshot before publishing it to Resolve.
func (s *Store) CreateToken(ctx context.Context, actorID string, spec TokenSpec) (Record, string, error) {
	return s.create(ctx, actorID, spec, false)
}

// CreateAdminToken is reserved for the local kbtoken CLI. Network handlers
// cannot call this method through TokenManager.
func (s *Store) CreateAdminToken(ctx context.Context, actorID, name string) (Record, string, error) {
	return s.create(ctx, actorID, TokenSpec{Name: name}, true)
}

func (s *Store) create(ctx context.Context, actorID string, spec TokenSpec, admin bool) (Record, string, error) {
	if err := ctx.Err(); err != nil {
		return Record{}, "", err
	}
	if err := ValidateName(spec.Name); err != nil {
		return Record{}, "", err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.principals) >= MaxPrincipals {
		return Record{}, "", ErrPrincipalLimit
	}
	for _, existing := range s.principals {
		if existing.Name == spec.Name {
			return Record{}, "", ErrPrincipalNameExists
		}
	}

	var id string
	var err error
	for {
		id, err = GeneratePrincipalID()
		if err != nil {
			return Record{}, "", err
		}
		if !principalIDExists(s.principals, id) {
			break
		}
	}
	token, err := GenerateToken()
	if err != nil {
		return Record{}, "", err
	}
	record := Record{
		Principal: shared.Principal{
			ID:          id,
			Name:        spec.Name,
			Teams:       append([]string(nil), spec.Teams...),
			AllTeams:    spec.AllTeams,
			Engineering: spec.Engineering,
			Indexer:     spec.Indexer,
			Admin:       admin,
		},
		CreatedAt:   time.Now().UTC(),
		TokenSHA256: HashToken(token),
	}
	next := append(cloneRecords(s.principals), record)
	if err := s.persistLocked(next); err != nil {
		return Record{}, "", err
	}
	s.audit("create", actorID, record.ID)
	return cloneRecord(record), token, nil
}

// DeleteToken removes a non-admin principal by immutable ID. Admin
// principals are deliberately protected from the network API.
func (s *Store) DeleteToken(ctx context.Context, actorID, principalID string) error {
	return s.delete(ctx, actorID, principalID, false, false)
}

// DeleteAdminToken is reserved for the local CLI and requires force when the
// target is the last remaining admin.
func (s *Store) DeleteAdminToken(ctx context.Context, actorID, principalID string, force bool) error {
	return s.delete(ctx, actorID, principalID, true, force)
}

func (s *Store) delete(ctx context.Context, actorID, principalID string, adminOnly, force bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	principalID = strings.TrimSpace(principalID)
	if principalID == "" {
		return ErrPrincipalNotFound
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	index := -1
	for i, record := range s.principals {
		if record.ID == principalID {
			index = i
			if adminOnly && !record.Admin {
				return ErrNotAdminPrincipal
			}
			if !adminOnly && record.Admin {
				return ErrAdminTokenProtected
			}
			break
		}
	}
	if index < 0 {
		return ErrPrincipalNotFound
	}
	if adminOnly && !force && countAdmins(s.principals) == 1 {
		return ErrLastAdminRequiresForce
	}

	next := make([]Record, 0, len(s.principals)-1)
	next = append(next, s.principals[:index]...)
	next = append(next, s.principals[index+1:]...)
	if err := s.persistLocked(next); err != nil {
		return err
	}
	s.audit("delete", actorID, principalID)
	return nil
}

func countAdmins(records []Record) int {
	count := 0
	for _, record := range records {
		if record.Admin {
			count++
		}
	}
	return count
}

func principalIDExists(records []Record, id string) bool {
	for _, record := range records {
		if record.ID == id {
			return true
		}
	}
	return false
}

func (s *Store) persistLocked(next []Record) error {
	if s.path == "" {
		return errors.New("auth store file path is required")
	}
	contents, err := json.MarshalIndent(fileDocument{Principals: next}, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal auth snapshot: %w", err)
	}
	contents = append(contents, '\n')
	if int64(len(contents)) > MaxAuthFileSize {
		return fmt.Errorf("%w: %d bytes exceeds %d", ErrAuthSnapshotTooLarge, len(contents), MaxAuthFileSize)
	}

	fileLock, err := lockfile.Acquire(lockfile.Path(s.path))
	if err != nil {
		return err
	}
	if err := atomicfile.Replace(s.path, contents, 0o600); err != nil {
		_ = fileLock.Release()
		return fmt.Errorf("persist auth snapshot: %w", err)
	}
	// The disk snapshot is now atomically visible. Publish the exact same
	// complete list only after the rename, never before it. Replace syncs the
	// temporary file, but parent-directory crash durability is platform- and
	// filesystem-dependent.
	s.principals = cloneRecords(next)
	if err := fileLock.Release(); err != nil {
		if s.logger != nil {
			s.logger.Error("auth file lock release failed after commit",
				zap.Error(err),
				zap.String("path", lockfile.Path(s.path)),
			)
		}
	}
	return nil
}

func (s *Store) audit(action, actorID, principalID string) {
	if s.logger == nil {
		return
	}
	s.logger.Info("auth principal changed",
		zap.String("actor_id", actorID),
		zap.String("action", action),
		zap.String("principal_id", principalID),
	)
}
