package gateway

import (
	"testing"
)

func TestLimiter_PerUserLimit(t *testing.T) {
	limiter := NewLimiter(10, 2)

	r1, ok, reason := limiter.TryAcquire("alice")
	if !ok {
		t.Fatalf("first acquire failed: %s", reason)
	}
	defer r1()

	r2, ok, reason := limiter.TryAcquire("alice")
	if !ok {
		t.Fatalf("second acquire failed: %s", reason)
	}
	defer r2()

	_, ok, reason = limiter.TryAcquire("alice")
	if ok {
		t.Fatal("third acquire for alice should fail")
	}
	if reason != "user_concurrency_limit" {
		t.Errorf("expected reason user_concurrency_limit, got %q", reason)
	}

	// Another user should still be able to acquire
	rBob, ok, reason := limiter.TryAcquire("bob")
	if !ok {
		t.Fatalf("bob acquire failed: %s", reason)
	}
	defer rBob()
}

func TestLimiter_GlobalLimit(t *testing.T) {
	limiter := NewLimiter(2, 5)

	r1, ok, reason := limiter.TryAcquire("u1")
	if !ok {
		t.Fatalf("u1 acquire failed: %s", reason)
	}
	defer r1()

	r2, ok, reason := limiter.TryAcquire("u2")
	if !ok {
		t.Fatalf("u2 acquire failed: %s", reason)
	}
	defer r2()

	_, ok, reason = limiter.TryAcquire("u3")
	if ok {
		t.Fatal("u3 acquire should fail when global limit reached")
	}
	if reason != "global_concurrency_limit" {
		t.Errorf("expected reason global_concurrency_limit, got %q", reason)
	}
}

func TestLimiter_ReleaseRestoresCapacity(t *testing.T) {
	limiter := NewLimiter(5, 1)

	r1, ok, _ := limiter.TryAcquire("alice")
	if !ok {
		t.Fatal("initial acquire failed")
	}

	_, ok, _ = limiter.TryAcquire("alice")
	if ok {
		t.Fatal("second acquire while in-flight should fail")
	}

	r1() // Release capacity

	r2, ok, _ := limiter.TryAcquire("alice")
	if !ok {
		t.Fatal("acquire after release failed")
	}
	defer r2()
}

func TestLimiter_IdempotentReleaseDoesNotUnderflow(t *testing.T) {
	limiter := NewLimiter(5, 2)

	r1, ok, _ := limiter.TryAcquire("alice")
	if !ok {
		t.Fatal("acquire failed")
	}

	// Repeated releases must be idempotent
	r1()
	r1()
	r1()

	global, users := limiter.Snapshot()
	if global < 0 {
		t.Fatalf("global count underflowed to %d (allows bypassing limits)", global)
	}
	if users["alice"] < 0 {
		t.Fatalf("user count underflowed to %d (allows bypassing limits)", users["alice"])
	}
}

func TestLimiter_MapCleanupOnZero(t *testing.T) {
	limiter := NewLimiter(5, 2)

	r1, ok, _ := limiter.TryAcquire("alice")
	if !ok {
		t.Fatal("acquire failed")
	}

	r1()

	_, users := limiter.Snapshot()
	if _, exists := users["alice"]; exists {
		t.Fatal("expected alice to be removed from map when count hits 0 to prevent memory leak")
	}
}
