package gateway

import (
	"sync"
)

// Limiter coordinates inflight request limits globally and per effective user.
// It implements zero-queue admission control: requests that cannot acquire a
// slot immediately are rejected.
//
// ponytail: in-memory counter, restart resets to zero (A2 verifies this). Multi-instance needs Redis.
type Limiter struct {
	mu             sync.Mutex
	globalInflight int
	maxGlobal      int
	userInflight   map[string]int
	maxPerUser     int
}

// NewLimiter initializes an admission limiter with global and per-user bounds.
// A limit <= 0 means unlimited.
func NewLimiter(maxGlobal, maxPerUser int) *Limiter {
	return &Limiter{
		maxGlobal:    maxGlobal,
		maxPerUser:   maxPerUser,
		userInflight: make(map[string]int),
	}
}

// TryAcquire attempts to claim one concurrent slot for user. If either the
// per-user or global limit is reached, it returns ok=false and the rejection
// reason ("user_concurrency_limit" or "global_concurrency_limit").
// When ok=true, release is guaranteed to be idempotent and thread-safe via sync.Once.
func (l *Limiter) TryAcquire(user string) (release func(), ok bool, reason string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.maxPerUser > 0 && l.userInflight[user] >= l.maxPerUser {
		return nil, false, "user_concurrency_limit"
	}
	if l.maxGlobal > 0 && l.globalInflight >= l.maxGlobal {
		return nil, false, "global_concurrency_limit"
	}

	l.globalInflight++
	l.userInflight[user]++

	var once sync.Once
	release = func() {
		once.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			if l.globalInflight > 0 {
				l.globalInflight--
			}
			if count, exists := l.userInflight[user]; exists {
				if count <= 1 {
					delete(l.userInflight, user)
				} else {
					l.userInflight[user]--
				}
			}
		})
	}
	return release, true, ""
}

// Snapshot returns the current inflight counts for diagnostics.
func (l *Limiter) Snapshot() (global int, users map[string]int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	users = make(map[string]int, len(l.userInflight))
	for k, v := range l.userInflight {
		users[k] = v
	}
	return l.globalInflight, users
}
