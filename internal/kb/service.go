package kb

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/google/uuid"
)

const (
	// Thresholds are empirical for the sample docs; see the QRSPI plan calibration note.
	strongThreshold = 1.7
	minThreshold    = 1.2
	topK            = 3
	embeddingModel  = "text-embedding-3-small"
)

type sectionStore interface {
	Parse(ctx context.Context) ([]Section, int, error)
	Save(ctx context.Context, sections []Section) error
	Load(ctx context.Context) ([]Section, error)
}

type Service struct {
	sections sectionStore
	llm      LLM
	embedder Embedder
	vectors  VectorStore
	sessions SessionStore

	mu      sync.RWMutex
	vecMap  map[string][]float32
	corpus  Corpus
	indexed []Section
	ready   bool
}

func NewService(sections sectionStore, llm LLM, embedder Embedder, vectors VectorStore, sessions SessionStore) *Service {
	return &Service{sections: sections, llm: llm, embedder: embedder, vectors: vectors, sessions: sessions}
}

func (s *Service) Index(ctx context.Context) (int, int, error) {
	secs, files, err := s.sections.Parse(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("parse docs: %w", err)
	}
	if err := s.sections.Save(ctx, secs); err != nil {
		return 0, 0, fmt.Errorf("save index: %w", err)
	}

	vecMap, err := s.embedSections(ctx, secs)
	if err != nil {
		return 0, 0, err
	}

	s.storeIndexSnapshot(secs, BuildCorpus(secs), vecMap, true)
	return files, len(secs), nil
}

func (s *Service) LoadOnStartup(ctx context.Context) error {
	secs, err := s.sections.Load(ctx)
	if errors.Is(err, ErrNotIndexed) || errors.Is(err, ErrIndexStale) {
		return err
	}
	if err != nil {
		return fmt.Errorf("load index: %w", err)
	}

	vecMap := map[string][]float32{}
	if s.vectors != nil {
		vecMap, err = s.vectors.Load(ctx)
		if err != nil {
			return fmt.Errorf("load vectors: %w", err)
		}
	}

	s.storeIndexSnapshot(secs, BuildCorpus(secs), vecMap, true)
	return nil
}

func (s *Service) Chat(ctx context.Context, query, sessionID string) (Answer, string, error) {
	if strings.TrimSpace(query) == "" {
		return Answer{}, sessionID, ErrEmptyQuery
	}
	if sessionID == "" {
		sessionID = uuid.NewString()
	}

	indexed, corpus, vecMap, ready := s.indexSnapshot()
	if !ready {
		return Answer{}, sessionID, ErrNotIndexed
	}

	history := s.history(ctx, sessionID)
	contextualQuery := composeQuery(history, query)
	ranked := corpus.RankBM25(tokenize(contextualQuery))
	if len(ranked) == 0 || ranked[0].Score < minThreshold {
		return s.deny(ctx, sessionID, query)
	}

	if ranked[0].Score >= strongThreshold || len(vecMap) == 0 || s.embedder == nil {
		return s.answerFrom(ctx, sessionID, query, topSections(indexed, ranked, topK), history, "markdown")
	}

	queryVectors, err := s.embedder.Embed(ctx, []string{contextualQuery})
	if err != nil {
		return Answer{}, sessionID, fmt.Errorf("embed query: %w", err)
	}
	if len(queryVectors) == 0 {
		return s.deny(ctx, sessionID, query)
	}

	sections := topByCosine(indexed, vecMap, queryVectors[0], topK)
	if len(sections) == 0 {
		return s.deny(ctx, sessionID, query)
	}
	return s.answerFrom(ctx, sessionID, query, sections, history, "vector")
}

// deny records the turn and returns the cannot-confirm answer.
func (s *Service) deny(ctx context.Context, sessionID, query string) (Answer, string, error) {
	answer := s.cannotConfirm()
	s.appendTurn(ctx, sessionID, query, answer)
	return answer, sessionID, nil
}

// answerFrom grounds the LLM on the given sections, records the turn, and returns the answer.
func (s *Service) answerFrom(ctx context.Context, sessionID, query string, sections []Section, history []Turn, strategy string) (Answer, string, error) {
	text, err := s.llm.Answer(ctx, query, sections, history)
	if err != nil {
		return Answer{}, sessionID, fmt.Errorf("llm answer: %w", err)
	}
	answer := NewAnswer(text, citationsFor(sections), strategy)
	s.appendTurn(ctx, sessionID, query, answer)
	return answer, sessionID, nil
}

func (s *Service) embedSections(ctx context.Context, secs []Section) (map[string][]float32, error) {
	if s.embedder == nil || s.vectors == nil {
		return map[string][]float32{}, nil
	}

	embs, err := s.embedder.Embed(ctx, bodiesOf(secs))
	if err != nil {
		return nil, fmt.Errorf("embed sections: %w", err)
	}
	if len(embs) != len(secs) {
		return nil, fmt.Errorf("embed sections: got %d vectors, want %d", len(embs), len(secs))
	}

	vecMap := make(map[string][]float32, len(secs))
	for i, sec := range secs {
		vecMap[sec.Citation()] = embs[i]
	}
	if err := s.vectors.Save(ctx, embeddingModel, vecMap); err != nil {
		return nil, fmt.Errorf("save vectors: %w", err)
	}
	return vecMap, nil
}

func (s *Service) storeIndexSnapshot(indexed []Section, corpus Corpus, vecMap map[string][]float32, ready bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.indexed = indexed
	s.corpus = corpus
	s.vecMap = vecMap
	s.ready = ready
}

func (s *Service) indexSnapshot() ([]Section, Corpus, map[string][]float32, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.indexed, s.corpus, s.vecMap, s.ready
}

func topSections(indexed []Section, ranked []ScoredSection, k int) []Section {
	if k > len(ranked) {
		k = len(ranked)
	}
	sections := make([]Section, 0, k)
	for _, scored := range ranked[:k] {
		if scored.Score <= 0 {
			continue
		}
		if scored.Index >= 0 && scored.Index < len(indexed) {
			sections = append(sections, indexed[scored.Index])
		}
	}
	return sections
}

func topByCosine(indexed []Section, vecMap map[string][]float32, queryVec []float32, k int) []Section {
	ranked := make([]ScoredSection, 0, len(indexed))
	for i, section := range indexed {
		score := Cosine(queryVec, vecMap[section.Citation()])
		if score <= 0 {
			continue
		}
		ranked = append(ranked, ScoredSection{Index: i, Score: score})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].Score == ranked[j].Score {
			return ranked[i].Index < ranked[j].Index
		}
		return ranked[i].Score > ranked[j].Score
	})
	return topSections(indexed, ranked, k)
}

func (s *Service) cannotConfirm() Answer {
	return NewAnswer("I cannot confirm that from the knowledge base.", nil, "")
}

func (s *Service) history(ctx context.Context, sessionID string) []Turn {
	if s.sessions == nil {
		return nil
	}
	return s.sessions.Get(ctx, sessionID)
}

func (s *Service) appendTurn(ctx context.Context, sessionID, query string, answer Answer) {
	if s.sessions == nil {
		return
	}
	s.sessions.Append(ctx, sessionID, Turn{Query: query, Answer: answer.Text()})
}

func composeQuery(history []Turn, query string) string {
	if len(history) == 0 {
		return query
	}
	var b strings.Builder
	for _, turn := range history {
		b.WriteString(turn.Query)
		b.WriteByte(' ')
	}
	b.WriteString(query)
	return b.String()
}

func bodiesOf(sections []Section) []string {
	bodies := make([]string, 0, len(sections))
	for _, section := range sections {
		bodies = append(bodies, section.Body())
	}
	return bodies
}

func citationsFor(sections []Section) []Citation {
	citations := make([]Citation, 0, len(sections))
	for _, section := range sections {
		citations = append(citations, NewCitation(section.File(), section.Anchor()))
	}
	return citations
}
