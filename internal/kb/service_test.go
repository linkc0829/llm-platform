package kb

import (
	"context"
	"errors"
	"testing"
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
	vectors map[string][]float32
	err     error
	calls   int
	texts   []string
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

type fakeVectorStore struct {
	loadVectors map[string][]float32
	loadErr     error
	saveErr     error
	savedModel  string
	saved       map[string][]float32
}

func (f *fakeVectorStore) Load(_ context.Context) (map[string][]float32, error) {
	return f.loadVectors, f.loadErr
}

func (f *fakeVectorStore) Save(_ context.Context, model string, vectors map[string][]float32) error {
	f.savedModel = model
	f.saved = vectors
	return f.saveErr
}

func TestServiceIndexBuildsAndPersistsIndex(t *testing.T) {
	section, err := NewSection("refund_policy.md", "Refund Timeline", "Refunds take 5-7 business days.")
	if err != nil {
		t.Fatalf("NewSection() error = %v, want nil", err)
	}
	store := &fakeSectionStore{parseSections: []Section{section}, parseFiles: 3}
	embedder := &fakeEmbedder{vectors: map[string][]float32{section.Body(): {1, 0}}}
	vectors := &fakeVectorStore{}
	svc := NewService(store, nil, embedder, vectors)

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
	if vectors.savedModel != embeddingModel || len(vectors.saved) != 1 {
		t.Errorf("Service.Index() saved vectors model/count = %q/%d, want %q/1", vectors.savedModel, len(vectors.saved), embeddingModel)
	}
	if !svc.ready || svc.corpus.N != 1 || len(svc.indexed) != 1 {
		t.Errorf("Service.Index() ready/corpus/indexed = %v/%d/%d, want true/1/1", svc.ready, svc.corpus.N, len(svc.indexed))
	}
}

func TestServiceLoadOnStartupHandlesMissingIndex(t *testing.T) {
	svc := NewService(&fakeSectionStore{loadErr: ErrNotIndexed}, nil, nil, nil)
	err := svc.LoadOnStartup(context.Background())
	if !errors.Is(err, ErrNotIndexed) {
		t.Errorf("Service.LoadOnStartup() error = %v, want ErrNotIndexed", err)
	}
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
			svc := NewService(&fakeSectionStore{}, llm, nil, nil)
			svc.indexed = sections
			svc.corpus = BuildCorpus(sections)
			svc.ready = tt.ready

			answer, sessionID, err := svc.Chat(context.Background(), tt.query, "session-1")
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
	bm25Section, err := NewSection("refund_policy.md", "Refund Timeline", "weak body")
	if err != nil {
		t.Fatalf("NewSection(bm25Section) error = %v, want nil", err)
	}
	vectorSection, err := NewSection("account_help.md", "Change Email Address", "nearest vector body")
	if err != nil {
		t.Fatalf("NewSection(vectorSection) error = %v, want nil", err)
	}
	llm := &fakeLLM{answer: "vector answer"}
	embedder := &fakeEmbedder{vectors: map[string][]float32{"weak weak": {0, 1}}}
	svc := NewService(&fakeSectionStore{}, llm, embedder, nil)
	svc.indexed = []Section{bm25Section, vectorSection}
	svc.corpus = Corpus{
		DocTokens: [][]string{{"weak"}, {"other"}},
		DocFreq:   map[string]int{"weak": 1, "other": 1},
		DocLen:    []int{1, 1},
		AvgLen:    1,
		N:         2,
	}
	svc.vecMap = map[string][]float32{
		bm25Section.Citation():   {1, 0},
		vectorSection.Citation(): {0, 1},
	}
	svc.ready = true

	answer, _, err := svc.Chat(context.Background(), "weak weak", "session-1")
	if err != nil {
		t.Fatalf("Service.Chat(weak vector query) error = %v, want nil", err)
	}
	if answer.Strategy() != "vector" {
		t.Errorf("Service.Chat(weak vector query) strategy = %q, want vector", answer.Strategy())
	}
	gotSources := citationStrings(answer.Sources())
	wantSources := []string{"account_help.md#change-email-address"}
	if !sameStrings(gotSources, wantSources) {
		t.Errorf("Service.Chat(weak vector query) sources = %#v, want %#v", gotSources, wantSources)
	}
	if len(llm.sections) == 0 || llm.sections[0].Citation() != vectorSection.Citation() {
		t.Errorf("Service.Chat(weak vector query) LLM first section = %#v, want %q", llm.sections, vectorSection.Citation())
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
		section, err := NewSection(spec.file, spec.heading, spec.body)
		if err != nil {
			t.Fatalf("NewSection(%q, %q) error = %v, want nil", spec.file, spec.heading, err)
		}
		sections = append(sections, section)
	}
	return sections
}

func citationStrings(citations []Citation) []string {
	out := make([]string, 0, len(citations))
	for _, citation := range citations {
		out = append(out, citation.String())
	}
	return out
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
