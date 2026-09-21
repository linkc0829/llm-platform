package kb

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

type fakeSectionStore struct {
	parseSections []Section
	parseFiles    int
	parseErr      error
	saveErr       error
	loadSections  []Section
	loadErr       error
	saved         []Section
}

func (f *fakeSectionStore) Parse(_ context.Context) ([]Section, int, error) {
	return f.parseSections, f.parseFiles, f.parseErr
}

func (f *fakeSectionStore) Save(_ context.Context, sections []Section) error {
	f.saved = append([]Section(nil), sections...)
	return f.saveErr
}

func (f *fakeSectionStore) Load(_ context.Context) ([]Section, error) {
	return f.loadSections, f.loadErr
}

type fakeLLM struct {
	answer   string
	err      error
	calls    int
	query    string
	sections []Section
	history  []Turn
}

func (f *fakeLLM) Answer(_ context.Context, query string, sections []Section, history []Turn) (string, error) {
	f.calls++
	f.query = query
	f.sections = append([]Section(nil), sections...)
	f.history = append([]Turn(nil), history...)
	if f.err != nil {
		return "", f.err
	}
	return f.answer, nil
}

type fakeEmbedder struct {
	vectors  map[string][]float32
	err      error
	calls    int
	texts    []string
	identity string
}

func (f *fakeEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	f.calls++
	f.texts = append([]string(nil), texts...)
	if f.err != nil {
		return nil, f.err
	}
	out := make([][]float32, 0, len(texts))
	for _, text := range texts {
		out = append(out, append([]float32(nil), f.vectors[text]...))
	}
	return out, nil
}

func (f *fakeEmbedder) EmbedIdentity() string {
	return f.identity
}

type fakeVectorStore struct {
	loadModel   string
	loadVectors map[string][]float32
	loadErr     error
	saveErr     error
	savedModel  string
	saved       map[string][]float32
}

func (f *fakeVectorStore) Load(_ context.Context) (string, map[string][]float32, error) {
	return f.loadModel, f.loadVectors, f.loadErr
}

func (f *fakeVectorStore) Save(_ context.Context, identity string, vectors map[string][]float32) error {
	f.savedModel = identity
	f.saved = vectors
	if f.saveErr == nil {
		f.loadModel = identity
		f.loadVectors = vectors
	}
	return f.saveErr
}

func TestServiceIndexBuildsAndPersistsIndex(t *testing.T) {
	section, err := NewSection("refund_policy.md", "Refund Timeline", "Refunds take 5-7 business days.", map[string]string{"doc_type": "procedure"}, nil)
	if err != nil {
		t.Fatalf("NewSection() error = %v, want nil", err)
	}
	store := &fakeSectionStore{parseSections: []Section{section}, parseFiles: 3}
	embedder := &fakeEmbedder{vectors: map[string][]float32{section.Body(): {1, 0}}}
	vectors := &fakeVectorStore{}
	svc := NewService(store, nil, embedder, vectors, nil, "configured-model")

	files, sections, err := svc.Index(context.Background())
	if err != nil {
		t.Fatalf("Service.Index() error = %v, want nil", err)
	}
	if files != 3 || sections != 1 {
		t.Errorf("Service.Index() = files %d sections %d, want files 3 sections 1", files, sections)
	}
	if len(store.saved) != 1 || store.saved[0].Citation() != section.Citation() {
		t.Errorf("Service.Index() saved = %#v, want parsed section", store.saved)
	}
	if vectors.savedModel != "configured-model" || len(vectors.saved) != 1 {
		t.Errorf("Service.Index() saved vectors model/count = %q/%d, want configured-model/1", vectors.savedModel, len(vectors.saved))
	}
	_, corpus, indexedVectors, ready := svc.indexSnapshot()
	if !ready || corpus.N != 1 || len(indexedVectors) != 1 {
		t.Errorf("Service.Index() ready/corpus/vectors = %v/%d/%d, want true/1/1", ready, corpus.N, len(indexedVectors))
	}
}

func TestServiceLoadOnStartupHandlesMissingIndex(t *testing.T) {
	svc := NewService(&fakeSectionStore{loadErr: ErrNotIndexed}, nil, nil, nil, nil, "test-model")
	err := svc.LoadOnStartup(context.Background())
	if !errors.Is(err, ErrNotIndexed) {
		t.Errorf("Service.LoadOnStartup() error = %v, want ErrNotIndexed", err)
	}
}

func TestServiceLoadOnStartupIgnoresMismatchedVectorModel(t *testing.T) {
	sections := mustSampleSections(t)
	llm := &fakeLLM{answer: "answer"}
	vectors := &fakeVectorStore{loadModel: "old-model", loadVectors: map[string][]float32{sections[0].Citation(): {1, 0}}}
	svc := NewService(&fakeSectionStore{loadSections: sections}, llm, nil, vectors, NewInProcStore(), "new-model")

	err := svc.LoadOnStartup(context.Background())
	if !errors.Is(err, ErrVectorsIgnored) {
		t.Fatalf("Service.LoadOnStartup() error = %v, want ErrVectorsIgnored", err)
	}
	answer, _, _, err := svc.ChatWithMetrics(context.Background(), FullAccessPrincipal("test-owner"), "How long do refunds take?", "session")
	if err != nil {
		t.Fatalf("Service.Chat() error = %v, want nil", err)
	}
	if answer.Strategy() != "markdown" {
		t.Errorf("Service.Chat() strategy = %q, want markdown", answer.Strategy())
	}
	if !answer.Grounded() {
		t.Error("Service.Chat() grounded = false, want true for a plain answer")
	}
}

// TestServiceChatStripsUngroundedSentinel locks the sentinel contract: when the
// model leads a refusal with ungroundedSentinel despite retrieved context, the
// service must report grounded=false and strip the marker, leaving the reason.
func TestServiceChatStripsUngroundedSentinel(t *testing.T) {
	sections := mustSampleSections(t)
	llm := &fakeLLM{answer: ungroundedSentinel + " only a control list, no recorded steps"}
	svc := NewService(&fakeSectionStore{loadSections: sections}, llm, nil, nil, NewInProcStore(), "test-model")
	if err := svc.LoadOnStartup(context.Background()); err != nil {
		t.Fatalf("Service.LoadOnStartup() error = %v, want nil", err)
	}

	answer, _, _, err := svc.ChatWithMetrics(context.Background(), FullAccessPrincipal("test-owner"), "How long do refunds take?", "session")
	if err != nil {
		t.Fatalf("Service.Chat() error = %v, want nil", err)
	}
	if answer.Grounded() {
		t.Error("Service.Chat() grounded = true, want false when the model leads with the ungrounded sentinel")
	}
	if got := answer.Text(); got != "only a control list, no recorded steps" {
		t.Errorf("Service.Chat() answer = %q, want the sentinel stripped to its reason", got)
	}
	if len(answer.Sources()) != 0 {
		t.Errorf("Service.Chat() sources = %v, want none — a refusal cites nothing", answer.Sources())
	}
}

// TestServiceChatDetectsCorruptedSentinel covers what a weak model actually
// emits. Asked for an exact token, llama3.1:8b answered "[UNEQUIPPED] ..." for a
// refusal; the old exact prefix match missed it and reported grounded=true with
// citations attached. Detection must tolerate the token being mangled or
// decorated, while a real answer must never be mistaken for a refusal.
func TestServiceChatDetectsCorruptedSentinel(t *testing.T) {
	tests := []struct {
		name         string
		answer       string
		wantGrounded bool
		wantText     string
	}{
		{"exact_sentinel", "[UNGROUNDED] no recorded steps", false, "no recorded steps"},
		{"observed_corruption_unequipped", "[UNEQUIPPED] the context lists controls only", false, "the context lists controls only"},
		{"markdown_emphasis", "**[UNGROUNDED]** no steps recorded", false, "no steps recorded"},
		{"answer_lead_in", "Answer: [UNGROUNDED] nothing to go on", false, "nothing to go on"},
		{"trailing_sentinel", "no recorded steps\n\n[UNGROUNDED]", false, "no recorded steps"},
		{"trailing_sentinel_inline", "no recorded steps. [UNGROUNDED]", false, "no recorded steps."},
		{"trailing_corruption_unequipped", "the context lists controls only\n[UNEQUIPPED]", false, "the context lists controls only"},
		{"trailing_markdown_emphasis", "no steps recorded\n\n**[UNGROUNDED]**", false, "no steps recorded"},
		{"grounded_answer_untouched", "Click Settings, then Printers.", true, "Click Settings, then Printers."},
		{"bracket_in_prose_is_not_a_refusal", "Use the [UNIT] field on the form.", true, "Use the [UNIT] field on the form."},
		// Every grounded answer ends in a citation, so the suffix match has to
		// leave one alone or the whole corpus reads as refusals.
		{"trailing_citation_is_not_a_refusal", "Press Enter [Store.POS/procedures/POS_Login/Login-procedure.md#登入-步驟-1]", true, "Press Enter [Store.POS/procedures/POS_Login/Login-procedure.md#登入-步驟-1]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sections := mustSampleSections(t)
			svc := NewService(&fakeSectionStore{loadSections: sections}, &fakeLLM{answer: tt.answer}, nil, nil, NewInProcStore(), "test-model")
			if err := svc.LoadOnStartup(context.Background()); err != nil {
				t.Fatalf("Service.LoadOnStartup() error = %v, want nil", err)
			}
			answer, _, _, err := svc.ChatWithMetrics(context.Background(), FullAccessPrincipal("test-owner"), "How long do refunds take?", "session")
			if err != nil {
				t.Fatalf("Service.Chat() error = %v, want nil", err)
			}
			if answer.Grounded() != tt.wantGrounded {
				t.Errorf("Service.Chat() grounded = %v, want %v for %q", answer.Grounded(), tt.wantGrounded, tt.answer)
			}
			if got := answer.Text(); got != tt.wantText {
				t.Errorf("Service.Chat() answer = %q, want %q", got, tt.wantText)
			}
			if !tt.wantGrounded && len(answer.Sources()) != 0 {
				t.Errorf("Service.Chat() sources = %v, want none for a refusal", answer.Sources())
			}
		})
	}
}

func TestServiceConcurrentIndexAndChatUsesConsistentSnapshot(t *testing.T) {
	sections := mustSampleSections(t)
	store := &fakeSectionStore{parseSections: sections, parseFiles: 3}
	svc := NewService(store, NewFakeLLM(), nil, nil, NewInProcStore(), "test-model")
	svc.storeIndexSnapshot(sections, BuildCorpus(sections), map[string][]float32{}, true)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if _, _, err := svc.Index(context.Background()); err != nil {
			t.Errorf("Service.Index() error = %v, want nil", err)
		}
	}()

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _, _, err := svc.ChatWithMetrics(context.Background(), FullAccessPrincipal("test-owner"), "How long do refunds take?", "session")
			if err != nil {
				t.Errorf("Service.Chat(concurrent %d) error = %v, want nil", i, err)
			}
		}(i)
	}
	wg.Wait()
}

func TestServiceChat(t *testing.T) {
	sections := mustSampleSections(t)

	tests := []struct {
		name          string
		query         string
		ready         bool
		wantErr       error
		wantAnswer    string
		wantStrategy  string
		wantSources   []string
		wantLLMCalls  int
		wantLLMSource string
	}{
		{
			name:          "strong_score_uses_markdown",
			query:         "Can I change my email address?",
			ready:         true,
			wantAnswer:    "Use Account Settings to change and verify the new email.",
			wantStrategy:  "markdown",
			wantSources:   []string{"account_help.md#change-email-address"},
			wantLLMCalls:  1,
			wantLLMSource: "account_help.md#change-email-address",
		},
		{
			name:         "both_weak_cannot_confirm",
			query:        "Which restaurants are nearby?",
			ready:        true,
			wantAnswer:   "I cannot confirm that from the knowledge base.",
			wantSources:  []string{},
			wantLLMCalls: 0,
		},
		{
			name:    "empty_query_returns_err_empty_query",
			query:   "  ",
			ready:   true,
			wantErr: ErrEmptyQuery,
		},
		{
			name:    "not_indexed_returns_err_not_indexed",
			query:   "Can I change my email address?",
			ready:   false,
			wantErr: ErrNotIndexed,
		},
		{
			name:          "citations_formatted_as_file_hash_anchor",
			query:         "How long do refunds take?",
			ready:         true,
			wantAnswer:    "Refunds are processed within 5-7 business days.",
			wantStrategy:  "markdown",
			wantSources:   []string{"refund_policy.md#refund-timeline"},
			wantLLMCalls:  1,
			wantLLMSource: "refund_policy.md#refund-timeline",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			llm := &fakeLLM{answer: tt.wantAnswer}
			svc := NewService(&fakeSectionStore{}, llm, nil, nil, NewInProcStore(), "test-model")
			svc.storeIndexSnapshot(sections, BuildCorpus(sections), map[string][]float32{}, tt.ready)

			answer, sessionID, _, err := svc.ChatWithMetrics(context.Background(), FullAccessPrincipal("test-owner"), tt.query, "session-1")
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Service.Chat(%q) error = %v, want %v", tt.query, err, tt.wantErr)
			}
			if tt.wantErr != nil {
				return
			}
			if sessionID != "session-1" {
				t.Errorf("Service.Chat(%q) sessionID = %q, want session-1", tt.query, sessionID)
			}
			if answer.Text() != tt.wantAnswer {
				t.Errorf("Service.Chat(%q) answer = %q, want %q", tt.query, answer.Text(), tt.wantAnswer)
			}
			if answer.Strategy() != tt.wantStrategy {
				t.Errorf("Service.Chat(%q) strategy = %q, want %q", tt.query, answer.Strategy(), tt.wantStrategy)
			}
			gotSources := citationStrings(answer.Sources())
			if !sameStrings(gotSources, tt.wantSources) {
				t.Errorf("Service.Chat(%q) sources = %#v, want %#v", tt.query, gotSources, tt.wantSources)
			}
			if llm.calls != tt.wantLLMCalls {
				t.Errorf("Service.Chat(%q) LLM calls = %d, want %d", tt.query, llm.calls, tt.wantLLMCalls)
			}
			if tt.wantLLMSource != "" {
				if len(llm.sections) == 0 || llm.sections[0].Citation() != tt.wantLLMSource {
					t.Errorf("Service.Chat(%q) LLM first source = %#v, want %q", tt.query, llm.sections, tt.wantLLMSource)
				}
			}
		})
	}
}

func TestServiceChatWeakScoreUsesVectorRetrieval(t *testing.T) {
	bm25Section, err := NewSection("refund_policy.md", "Refund Timeline", "weak body", map[string]string{"doc_type": "procedure"}, nil)
	if err != nil {
		t.Fatalf("NewSection(bm25Section) error = %v, want nil", err)
	}
	vectorSection, err := NewSection("account_help.md", "Change Email Address", "nearest vector body", map[string]string{"doc_type": "procedure"}, nil)
	if err != nil {
		t.Fatalf("NewSection(vectorSection) error = %v, want nil", err)
	}
	llm := &fakeLLM{answer: "vector answer"}
	embedder := &fakeEmbedder{vectors: map[string][]float32{"weak weak": {0, 1}}}
	svc := NewService(&fakeSectionStore{}, llm, embedder, nil, NewInProcStore(), "test-model")
	svc.storeIndexSnapshot([]Section{bm25Section, vectorSection}, Corpus{
		DocTokens: [][]string{{"weak"}, {"other"}},
		DocFreq:   map[string]int{"weak": 1, "other": 1},
		DocLen:    []int{1, 1},
		AvgLen:    1,
		N:         2,
	}, map[string][]float32{
		bm25Section.Citation():   {1, 0},
		vectorSection.Citation(): {0, 1},
	}, true)

	answer, _, _, err := svc.ChatWithMetrics(context.Background(), FullAccessPrincipal("test-owner"), "weak weak", "session-1")
	if err != nil {
		t.Fatalf("Service.Chat(weak vector query) error = %v, want nil", err)
	}
	if answer.Strategy() != "hybrid" {
		t.Errorf("Service.Chat(weak vector query) strategy = %q, want hybrid", answer.Strategy())
	}
	gotSources := citationStrings(answer.Sources())
	if !containsString(gotSources, vectorSection.Citation()) {
		t.Errorf("Service.Chat(weak vector query) sources = %#v, want vector citation %q", gotSources, vectorSection.Citation())
	}
}

func TestServiceChatAnswersChineseQueryWithZeroBM25(t *testing.T) {
	sections := mustSampleSections(t)
	query := "動態密碼如何登入"
	llm := &fakeLLM{answer: "answer"}
	embedder := &fakeEmbedder{vectors: map[string][]float32{query: {0, 1}}}
	svc := NewService(&fakeSectionStore{}, llm, embedder, nil, NewInProcStore(), "test-model")
	svc.storeIndexSnapshot(sections, BuildCorpus(sections), map[string][]float32{sections[0].Citation(): {1, 0}, sections[1].Citation(): {0, 1}}, true)

	answer, _, _, err := svc.ChatWithMetrics(context.Background(), FullAccessPrincipal("test-owner"), query, "session")
	if err != nil {
		t.Fatalf("Service.Chat() error = %v, want nil", err)
	}
	if answer.Strategy() != "vector" {
		t.Errorf("Service.Chat() strategy = %q, want vector", answer.Strategy())
	}
}

func TestServiceChatEnglishIdentifierUsesBM25(t *testing.T) {
	section, err := NewSection("login.md", "Engineering Context", "LoginViewModel validates the dynamic password.", map[string]string{"doc_type": "procedure"}, nil)
	if err != nil {
		t.Fatalf("NewSection() error = %v, want nil", err)
	}
	llm := &fakeLLM{answer: "answer"}
	svc := NewService(&fakeSectionStore{}, llm, nil, nil, NewInProcStore(), "test-model")
	svc.storeIndexSnapshot([]Section{section}, BuildCorpus([]Section{section}), map[string][]float32{}, true)
	answer, _, _, err := svc.ChatWithMetrics(context.Background(), FullAccessPrincipal("test-owner"), "LoginViewModel LoginViewModel LoginViewModel LoginViewModel LoginViewModel", "session")
	if err != nil {
		t.Fatalf("Service.Chat() error = %v, want nil", err)
	}
	if !containsString(citationStrings(answer.Sources()), section.Citation()) {
		t.Errorf("Service.Chat() sources = %#v, want %q", citationStrings(answer.Sources()), section.Citation())
	}
}

// Degrading to BM25 keeps the query answerable, but strategy=markdown alone
// cannot say why: a 606-question run downgraded two queries this way and the
// field read the same as a section that simply has no vector. The warn line is
// what makes the degrade countable, so it is part of the contract, not decoration.
func TestServiceChatDegradesWhenEmbedderFails(t *testing.T) {
	core, logs := observer.New(zapcore.WarnLevel)
	defer zap.ReplaceGlobals(zap.New(core))()

	sections := mustSampleSections(t)
	llm := &fakeLLM{answer: "answer"}
	svc := NewService(&fakeSectionStore{}, llm, &fakeEmbedder{err: errors.New("down")}, nil, NewInProcStore(), "test-model")
	svc.storeIndexSnapshot(sections, BuildCorpus(sections), map[string][]float32{sections[0].Citation(): {1, 0}}, true)

	answer, _, _, err := svc.ChatWithMetrics(context.Background(), FullAccessPrincipal("test-owner"), "How long do refunds take?", "session")
	if err != nil {
		t.Fatalf("Service.Chat() error = %v, want nil", err)
	}
	if answer.Strategy() != "markdown" {
		t.Errorf("Service.Chat() strategy = %q, want markdown", answer.Strategy())
	}
	entries := logs.FilterMessage("query_embed_failed").All()
	if len(entries) != 1 {
		t.Fatalf("query_embed_failed entries = %d, want 1", len(entries))
	}
	if got := entries[0].ContextMap()["error"]; got != "down" {
		t.Errorf("query_embed_failed error = %v, want it to name the cause", got)
	}
}

func TestServiceChatReturnsCitedImages(t *testing.T) {
	first, err := NewSection("login.md", "Password", "Password", map[string]string{"doc_type": "procedure"}, []string{"screenshots/login.png", "screenshots/shared.png"})
	if err != nil {
		t.Fatalf("NewSection(first) error = %v, want nil", err)
	}
	second, err := NewSection("reports.md", "Report", "Report", map[string]string{"doc_type": "procedure"}, []string{"screenshots/shared.png", "screenshots/report.png"})
	if err != nil {
		t.Fatalf("NewSection(second) error = %v, want nil", err)
	}
	svc := NewService(&fakeSectionStore{}, &fakeLLM{answer: "answer"}, nil, nil, NewInProcStore(), "test-model")
	svc.storeIndexSnapshot([]Section{first, second}, BuildCorpus([]Section{first, second}), map[string][]float32{}, true)

	answer, _, _, err := svc.ChatWithMetrics(context.Background(), FullAccessPrincipal("test-owner"), "Password Password Password Report Report Report", "session")
	if err != nil {
		t.Fatalf("Service.ChatWithMetrics() error = %v, want nil", err)
	}
	want := []string{"screenshots/login.png", "screenshots/shared.png", "screenshots/report.png"}
	if !sameStrings(answer.Images(), want) {
		t.Errorf("Service.ChatWithMetrics() images = %#v, want %#v", answer.Images(), want)
	}
}

func TestServiceChatGeneratesSessionID(t *testing.T) {
	sections := mustSampleSections(t)
	llm := &fakeLLM{answer: "Refunds are processed within 5-7 business days."}
	svc := NewService(&fakeSectionStore{}, llm, nil, nil, NewInProcStore(), "test-model")
	svc.storeIndexSnapshot(sections, BuildCorpus(sections), map[string][]float32{}, true)

	_, sessionID, _, err := svc.ChatWithMetrics(context.Background(), FullAccessPrincipal("test-owner"), "How long do refunds take?", "")
	if err != nil {
		t.Fatalf("Service.Chat(empty session) error = %v, want nil", err)
	}
	if sessionID == "" {
		t.Errorf("Service.Chat(empty session) sessionID = empty, want generated id")
	}
}

func TestServiceChatUsesHistoryForFollowUpRetrieval(t *testing.T) {
	sections := mustSampleSections(t)
	llm := &fakeLLM{answer: "answer"}
	svc := NewService(&fakeSectionStore{}, llm, nil, nil, NewInProcStore(), "test-model")
	svc.storeIndexSnapshot(sections, BuildCorpus(sections), map[string][]float32{}, true)

	_, sessionID, _, err := svc.ChatWithMetrics(context.Background(), FullAccessPrincipal("test-owner"), "How long do refunds take?", "")
	if err != nil {
		t.Fatalf("Service.Chat(first turn) error = %v, want nil", err)
	}
	answer, _, _, err := svc.ChatWithMetrics(context.Background(), FullAccessPrincipal("test-owner"), "And which items can't be refunded?", sessionID)
	if err != nil {
		t.Fatalf("Service.Chat(follow-up) error = %v, want nil", err)
	}

	gotSources := citationStrings(answer.Sources())
	wantSources := []string{"refund_policy.md#non-refundable-items"}
	if len(gotSources) == 0 || gotSources[0] != wantSources[0] {
		t.Errorf("Service.Chat(follow-up) first source = %#v, want %#v", gotSources, wantSources)
	}
	if len(llm.history) != 1 || llm.history[0].Query != "How long do refunds take?" {
		t.Errorf("Service.Chat(follow-up) history = %#v, want first turn", llm.history)
	}
	if len(llm.sections) == 0 || llm.sections[0].Citation() != "refund_policy.md#non-refundable-items" {
		t.Errorf("Service.Chat(follow-up) LLM first section = %#v, want non-refundable section", llm.sections)
	}
}

func TestInProcStoreKeepsRecentTurnsAndExpiresIdleSessions(t *testing.T) {
	now := time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC)
	store := NewInProcStore()
	store.now = func() time.Time { return now }

	if _, err := store.Claim(context.Background(), "s1", "owner-a"); err != nil {
		t.Fatalf("InProcStore.Claim(s1, owner-a) error = %v, want nil", err)
	}
	for i := 0; i < 6; i++ {
		if err := store.Append(context.Background(), "s1", "owner-a", Turn{Query: string(rune('a' + i)), Answer: "answer"}); err != nil {
			t.Fatalf("InProcStore.Append(s1, owner-a, turn %d) error = %v, want nil", i, err)
		}
	}
	turns, err := store.Claim(context.Background(), "s1", "owner-a")
	if err != nil {
		t.Fatalf("InProcStore.Claim(s1, owner-a) error = %v, want nil", err)
	}
	if len(turns) != 5 || turns[0].Query != "b" || turns[4].Query != "f" {
		t.Errorf("InProcStore.Claim(s1, owner-a) turns = %#v, want last five b..f", turns)
	}

	now = now.Add(31 * time.Minute)
	turns, err = store.Claim(context.Background(), "s1", "owner-a")
	if err != nil {
		t.Fatalf("InProcStore.Claim(expired s1, owner-a) error = %v, want nil", err)
	}
	if len(turns) != 0 {
		t.Errorf("InProcStore.Claim(expired s1, owner-a) turns = %#v, want empty", turns)
	}
}

func mustSampleSections(t *testing.T) []Section {
	t.Helper()
	specs := []struct {
		file    string
		heading string
		body    string
	}{
		{file: "refund_policy.md", heading: "Refund Timeline", body: "Approved refunds take 5-7 business days to process. The exact arrival time depends on the customer's bank or card provider."},
		{file: "refund_policy.md", heading: "Non-Refundable Items", body: "Digital gift cards, final sale items, and personalized products are not refundable unless required by local law."},
		{file: "account_help.md", heading: "Change Email Address", body: "Customers can change their email address from Account Settings. After saving the new email address, the customer must verify it before it becomes active."},
	}

	sections := make([]Section, 0, len(specs))
	for _, spec := range specs {
		section, err := NewSection(spec.file, spec.heading, spec.body, map[string]string{"doc_type": "procedure", "team": "Store.POS"}, nil)
		if err != nil {
			t.Fatalf("NewSection(%q, %q) error = %v, want nil", spec.file, spec.heading, err)
		}
		sections = append(sections, section)
	}
	return sections
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// TestServiceChatLogsEveryQuery pins the query log down because it is write-only
// in production: nothing reads it back, so if the line stops being emitted —
// a refactor of record(), a caller added that bypasses it — every downstream
// consumer just sees an empty file and reads it as "no refusals". Both an
// answered and a denied query must appear, since the denied ones are the whole
// point of keeping the log.
func TestServiceChatLogsEveryQuery(t *testing.T) {
	core, logs := observer.New(zapcore.InfoLevel)
	defer zap.ReplaceGlobals(zap.New(core))()

	sections := mustSampleSections(t)
	svc := NewService(&fakeSectionStore{loadSections: sections}, &fakeLLM{answer: "grounded answer"}, nil, nil, NewInProcStore(), "test-model")
	if err := svc.LoadOnStartup(context.Background()); err != nil {
		t.Fatalf("Service.LoadOnStartup() error = %v, want nil", err)
	}

	if _, _, _, err := svc.ChatWithMetrics(context.Background(), FullAccessPrincipal("test-owner"), "login", "session"); err != nil {
		t.Fatalf("Service.Chat() error = %v, want nil", err)
	}
	if _, _, _, err := svc.ChatWithMetrics(context.Background(), FullAccessPrincipal("test-owner"), "zzzz", "session"); err != nil {
		t.Fatalf("Service.Chat() error = %v, want nil", err)
	}

	entries := logs.FilterMessage("kb_query").All()
	if len(entries) != 2 {
		t.Fatalf("kb_query lines = %d, want 2 (one answered, one denied)", len(entries))
	}
	for _, want := range []string{"q", "grounded", "strategy", "sources"} {
		if _, ok := entries[0].ContextMap()[want]; !ok {
			t.Errorf("kb_query missing field %q, got %v", want, entries[0].ContextMap())
		}
		if got := entries[0].ContextMap()["owner_id"]; got != "test-owner" {
			t.Errorf("kb_query owner_id = %v, want test-owner", got)
		}
	}
	if got := entries[1].ContextMap()["grounded"]; got != false {
		t.Errorf("kb_query grounded = %v for an unmatched query, want false — the log is only useful if refusals are marked", got)
	}
}

// TestComposeQuery pins what actually gets ranked, which is not what the caller
// asked. Nothing else covers it: the evals and the retrieval probe ask each
// question in its own session, so no other test reaches a second turn.
func TestComposeQuery(t *testing.T) {
	prior := []Turn{
		{Query: "折扣規則怎麼設定?"},
		{Query: "折扣範本可以套用到哪些商品?"},
		{Query: "折扣參數有哪些欄位?"},
	}
	tests := []struct {
		name    string
		history []Turn
		query   string
		want    string
	}{
		{
			name:  "first_turn_is_the_query_alone",
			query: "如何作廢訂單?",
			want:  "如何作廢訂單?",
		},
		{
			// The regression the marker test exists to prevent: prepending here
			// made retrieval return the discount sections, not the void ones.
			name:    "a_self_sufficient_query_ignores_history_entirely",
			history: prior,
			query:   "如何作廢訂單?",
			want:    "如何作廢訂單?",
		},
		{
			// Without the subject this asks nothing answerable, so it is the one
			// case that must pay the dilution.
			name:    "an_anaphoric_query_takes_the_previous_turn",
			history: []Turn{{Query: "如何作廢訂單?"}},
			query:   "那要什麼權限?",
			want:    "如何作廢訂單? 那要什麼權限?",
		},
		{
			name:    "only_the_previous_turn_is_taken_not_the_window",
			history: prior,
			query:   "它有哪些欄位?",
			want:    "折扣參數有哪些欄位? 它有哪些欄位?",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := composeQuery(tc.history, tc.query); got != tc.want {
				t.Errorf("composeQuery() = %q, want %q", got, tc.want)
			}
		})
	}
}
