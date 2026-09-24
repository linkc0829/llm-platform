package kb

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestInProcStoreConcurrentFirstClaimAllowsOnlyOneOwner(t *testing.T) {
	store := NewInProcStore()
	start := make(chan struct{})
	results := make(chan struct {
		owner string
		err   error
	}, 2)

	var wg sync.WaitGroup
	for _, owner := range []string{"principal-a", "principal-b"} {
		wg.Add(1)
		go func(owner string) {
			defer wg.Done()
			<-start
			_, err := store.Claim(context.Background(), "shared-session", owner)
			results <- struct {
				owner string
				err   error
			}{owner: owner, err: err}
		}(owner)
	}

	close(start)
	wg.Wait()

	successes := 0
	mismatches := 0
	for i := 0; i < 2; i++ {
		result := <-results
		switch {
		case result.err == nil:
			successes++
		case errors.Is(result.err, ErrSessionOwnerMismatch):
			mismatches++
		default:
			t.Errorf("Claim(shared-session, %s) error = %v, want nil or ErrSessionOwnerMismatch", result.owner, result.err)
		}
	}
	if successes != 1 || mismatches != 1 {
		t.Errorf("concurrent first Claim successes/mismatches = %d/%d, want 1/1", successes, mismatches)
	}
}

func TestInProcStoreRecreatedPrincipalIDCannotInheritSession(t *testing.T) {
	store := NewInProcStore()
	if _, err := store.Claim(context.Background(), "same-session", "principal-old"); err != nil {
		t.Fatalf("Claim(same-session, principal-old) error = %v, want nil", err)
	}

	_, err := store.Claim(context.Background(), "same-session", "principal-new")
	if !errors.Is(err, ErrSessionOwnerMismatch) {
		t.Errorf("Claim(same-session, principal-new) error = %v, want ErrSessionOwnerMismatch", err)
	}
}

func TestInProcStoreAppendRejectsOwnerAfterSessionReclaimed(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	store := NewInProcStore()
	store.now = func() time.Time { return now }

	if _, err := store.Claim(context.Background(), "reclaimable-session", "principal-old"); err != nil {
		t.Fatalf("Claim(reclaimable-session, principal-old) error = %v, want nil", err)
	}
	now = now.Add(store.idleTTL + time.Nanosecond)
	if _, err := store.Claim(context.Background(), "reclaimable-session", "principal-new"); err != nil {
		t.Fatalf("Claim(reclaimable-session, principal-new) after expiry error = %v, want nil", err)
	}

	err := store.Append(context.Background(), "reclaimable-session", "principal-old", Turn{Query: "old", Answer: "must not append"})
	if !errors.Is(err, ErrSessionOwnerMismatch) {
		t.Errorf("Append(reclaimable-session, principal-old) after reclaim error = %v, want ErrSessionOwnerMismatch", err)
	}
}

func TestServiceClaimsOwnerBeforeRetrieval(t *testing.T) {
	ctx := context.Background()
	sections := mustSampleSections(t)
	store := NewInProcStore()
	if _, err := store.Claim(ctx, "shared-session", "principal-a"); err != nil {
		t.Fatalf("Claim(shared-session, principal-a) error = %v, want nil", err)
	}
	embedder := &fakeEmbedder{vectors: map[string][]float32{"unused": {1, 0}}}
	llm := &fakeLLM{answer: "must not answer"}
	svc := NewService(&fakeSectionStore{}, llm, embedder, nil, store, "test-model")
	svc.storeIndexSnapshot(sections, BuildCorpus(sections), map[string][]float32{sections[0].Citation(): {1, 0}}, true)

	_, _, _, err := svc.ChatWithMetrics(ctx, FullAccessPrincipal("principal-b"), "How long do refunds take?", "shared-session")
	if !errors.Is(err, ErrSessionOwnerMismatch) {
		t.Fatalf("Service.ChatWithMetrics() error = %v, want ErrSessionOwnerMismatch", err)
	}
	if embedder.calls != 0 {
		t.Errorf("Service.ChatWithMetrics() embedder calls = %d, want 0 before owner claim succeeds", embedder.calls)
	}
	if llm.calls != 0 {
		t.Errorf("Service.ChatWithMetrics() LLM calls = %d, want 0 before owner claim succeeds", llm.calls)
	}
}

func TestServicePropagatesAppendMismatchAfterAnswer(t *testing.T) {
	sections := mustSampleSections(t)
	llm := &fakeLLM{answer: "grounded answer"}
	store := &appendErrorSessionStore{appendErr: ErrSessionOwnerMismatch}
	svc := NewService(&fakeSectionStore{}, llm, nil, nil, store, "test-model")
	svc.storeIndexSnapshot(sections, BuildCorpus(sections), map[string][]float32{}, true)

	answer, _, _, err := svc.ChatWithMetrics(context.Background(), FullAccessPrincipal("principal-a"), "How long do refunds take?", "session")
	if !errors.Is(err, ErrSessionOwnerMismatch) {
		t.Fatalf("Service.ChatWithMetrics() answer-path error = %v, want ErrSessionOwnerMismatch", err)
	}
	if answer.Text() != "" {
		t.Errorf("Service.ChatWithMetrics() answer after append mismatch = %q, want empty answer", answer.Text())
	}
	if llm.calls != 1 {
		t.Errorf("Service.ChatWithMetrics() LLM calls = %d, want 1 before append mismatch", llm.calls)
	}
	if store.appendCalls != 1 {
		t.Errorf("session Append calls = %d, want 1", store.appendCalls)
	}
}

func TestServicePropagatesAppendMismatchAfterDeniedAnswer(t *testing.T) {
	sections := mustSampleSections(t)
	llm := &fakeLLM{answer: "must not answer"}
	store := &appendErrorSessionStore{appendErr: ErrSessionOwnerMismatch}
	svc := NewService(&fakeSectionStore{}, llm, nil, nil, store, "test-model")
	svc.storeIndexSnapshot(sections, BuildCorpus(sections), map[string][]float32{}, true)

	answer, _, _, err := svc.ChatWithMetrics(context.Background(), FullAccessPrincipal("principal-a"), "Which restaurants are nearby?", "session")
	if !errors.Is(err, ErrSessionOwnerMismatch) {
		t.Fatalf("Service.ChatWithMetrics() deny-path error = %v, want ErrSessionOwnerMismatch", err)
	}
	if answer.Text() != "" {
		t.Errorf("Service.ChatWithMetrics() denied answer after append mismatch = %q, want empty answer", answer.Text())
	}
	if llm.calls != 0 {
		t.Errorf("Service.ChatWithMetrics() deny-path LLM calls = %d, want 0", llm.calls)
	}
	if store.appendCalls != 1 {
		t.Errorf("session Append calls = %d, want 1", store.appendCalls)
	}
}

type appendErrorSessionStore struct {
	appendErr   error
	appendCalls int
	claimCalls  int
}

func (s *appendErrorSessionStore) Claim(context.Context, string, string) ([]Turn, error) {
	s.claimCalls++
	return nil, nil
}

func (s *appendErrorSessionStore) Append(_ context.Context, _, _ string, _ Turn) error {
	s.appendCalls++
	return s.appendErr
}

func TestServiceFailsIfSessionIsReclaimedBeforeAppend(t *testing.T) {
	sections := mustSampleSections(t)
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	store := NewInProcStore()
	store.now = func() time.Time { return now }
	llm := &reclaimingLLM{store: store, now: &now, sessionID: "session", ownerID: "principal-new"}
	svc := NewService(&fakeSectionStore{}, llm, nil, nil, store, "test-model")
	svc.storeIndexSnapshot(sections, BuildCorpus(sections), map[string][]float32{}, true)

	answer, _, _, err := svc.ChatWithMetrics(context.Background(), FullAccessPrincipal("principal-old"), "How long do refunds take?", "session")
	if !errors.Is(err, ErrSessionOwnerMismatch) {
		t.Fatalf("Service.ChatWithMetrics() reclaimed-session error = %v, want ErrSessionOwnerMismatch", err)
	}
	if llm.claimErr != nil {
		t.Fatalf("reclaiming LLM Claim() error = %v, want nil", llm.claimErr)
	}
	if answer.Text() != "" {
		t.Errorf("Service.ChatWithMetrics() answer after session reclaim = %q, want empty answer", answer.Text())
	}
	if llm.calls != 1 {
		t.Errorf("Service.ChatWithMetrics() LLM calls = %d, want 1 before append mismatch", llm.calls)
	}
}

type reclaimingLLM struct {
	store     *InProcStore
	now       *time.Time
	sessionID string
	ownerID   string
	calls     int
	claimErr  error
}

func (l *reclaimingLLM) Answer(ctx context.Context, _ string, _ []Section, _ []Turn) (Completion, error) {
	l.calls++
	*l.now = l.now.Add(l.store.idleTTL + time.Nanosecond)
	_, l.claimErr = l.store.Claim(ctx, l.sessionID, l.ownerID)
	return Completion{Text: "answer"}, nil
}

func TestServiceRejectsEmptyOwnerBeforeClaim(t *testing.T) {
	sections := mustSampleSections(t)
	store := &appendErrorSessionStore{}
	svc := NewService(&fakeSectionStore{}, &fakeLLM{answer: "must not answer"}, nil, nil, store, "test-model")
	svc.storeIndexSnapshot(sections, BuildCorpus(sections), map[string][]float32{}, true)

	_, _, _, err := svc.ChatWithMetrics(context.Background(), FullAccessPrincipal(""), "How long do refunds take?", "session")
	if !errors.Is(err, ErrSessionOwnerRequired) {
		t.Fatalf("Service.ChatWithMetrics(empty owner) error = %v, want ErrSessionOwnerRequired", err)
	}
	if store.claimCalls != 0 {
		t.Errorf("SessionStore.Claim(empty owner) calls = %d, want 0", store.claimCalls)
	}
}

func TestServiceAcceptsExplicitAnonymousOwner(t *testing.T) {
	sections := mustSampleSections(t)
	svc := NewService(&fakeSectionStore{}, &fakeLLM{answer: "local answer"}, nil, nil, NewInProcStore(), "test-model")
	svc.storeIndexSnapshot(sections, BuildCorpus(sections), map[string][]float32{}, true)

	answer, _, _, err := svc.ChatWithMetrics(context.Background(), FullAccessPrincipal(AnonymousOwner), "How long do refunds take?", "session")
	if err != nil {
		t.Fatalf("Service.ChatWithMetrics(%q owner) error = %v, want nil", AnonymousOwner, err)
	}
	if answer.Text() != "local answer" {
		t.Errorf("Service.ChatWithMetrics(%q owner) answer = %q, want %q", AnonymousOwner, answer.Text(), "local answer")
	}
}
