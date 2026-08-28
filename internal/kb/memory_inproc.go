package kb

import (
	"context"
	"sync"
	"time"
)

// InProcStore is a per-session ring of recent turns. It is safe for concurrent use.
type InProcStore struct {
	mu       sync.Mutex
	sessions map[string]*sessionEntry
	maxTurns int
	idleTTL  time.Duration
	now      func() time.Time
}

type sessionEntry struct {
	ownerID  string
	turns    []Turn
	lastSeen time.Time
}

func NewInProcStore() *InProcStore {
	return &InProcStore{
		sessions: map[string]*sessionEntry{},
		maxTurns: 5,
		idleTTL:  30 * time.Minute,
		now:      time.Now,
	}
}

// Claim atomically creates a session for ownerID or returns its existing history.
func (s *InProcStore) Claim(ctx context.Context, id, ownerID string) ([]Turn, error) {
	if err := ctx.Err(); err != nil || id == "" {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	s.sweepExpired(now)
	entry := s.sessions[id]
	if entry == nil {
		s.sessions[id] = &sessionEntry{ownerID: ownerID, lastSeen: now}
		return nil, nil
	}
	if entry.ownerID != ownerID {
		return nil, ErrSessionOwnerMismatch
	}
	entry.lastSeen = now
	return append([]Turn(nil), entry.turns...), nil
}

func (s *InProcStore) Append(ctx context.Context, id, ownerID string, turn Turn) error {
	if err := ctx.Err(); err != nil || id == "" {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	s.sweepExpired(now)
	entry := s.sessions[id]
	if entry == nil || entry.ownerID != ownerID {
		return ErrSessionOwnerMismatch
	}
	entry.turns = append(entry.turns, turn)
	if len(entry.turns) > s.maxTurns {
		entry.turns = append([]Turn(nil), entry.turns[len(entry.turns)-s.maxTurns:]...)
	}
	entry.lastSeen = now
	return nil
}

func (s *InProcStore) sweepExpired(now time.Time) {
	for id, entry := range s.sessions {
		if now.Sub(entry.lastSeen) > s.idleTTL {
			delete(s.sessions, id)
		}
	}
}
