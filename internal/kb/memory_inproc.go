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

func (s *InProcStore) Get(ctx context.Context, id string) []Turn {
	if ctx.Err() != nil || id == "" {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	s.sweepExpired(now)
	entry := s.sessions[id]
	if entry == nil {
		return nil
	}
	entry.lastSeen = now
	return append([]Turn(nil), entry.turns...)
}

func (s *InProcStore) Append(ctx context.Context, id string, turn Turn) {
	if ctx.Err() != nil || id == "" {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	s.sweepExpired(now)
	entry := s.sessions[id]
	if entry == nil {
		entry = &sessionEntry{}
		s.sessions[id] = entry
	}
	entry.turns = append(entry.turns, turn)
	if len(entry.turns) > s.maxTurns {
		entry.turns = append([]Turn(nil), entry.turns[len(entry.turns)-s.maxTurns:]...)
	}
	entry.lastSeen = now
}

func (s *InProcStore) sweepExpired(now time.Time) {
	for id, entry := range s.sessions {
		if now.Sub(entry.lastSeen) > s.idleTTL {
			delete(s.sessions, id)
		}
	}
}
