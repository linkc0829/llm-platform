package kb

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
)

const (
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
	vecMap   map[string][]float32
	corpus   Corpus
	indexed  []Section
	ready    bool
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

	s.indexed = secs
	s.corpus = BuildCorpus(secs)
	s.vecMap = vecMap
	s.ready = true
	return files, len(secs), nil
}

func (s *Service) LoadOnStartup(ctx context.Context) error {
	secs, err := s.sections.Load(ctx)
	if errors.Is(err, ErrNotIndexed) {
		return ErrNotIndexed
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

	s.indexed = secs
	s.corpus = BuildCorpus(secs)
	s.vecMap = vecMap
	s.ready = true
	return nil
}

func (s *Service) Chat(ctx context.Context, query, sessionID string) (Answer, string, error) {
	if strings.TrimSpace(query) == "" {
		return Answer{}, sessionID, ErrEmptyQuery
	}
	if sessionID == "" {
		sessionID = uuid.NewString()
	}
	if !s.ready {
		return Answer{}, sessionID, ErrNotIndexed
	}

	history := s.history(ctx, sessionID)
	contextualQuery := composeQuery(history, query)
	ranked := s.corpus.RankBM25(tokenize(contextualQuery))
	if len(ranked) == 0 || ranked[0].Score < minThreshold {
		answer := s.cannotConfirm()
		s.appendTurn(ctx, sessionID, query, answer)
		return answer, sessionID, nil
	}

	if ranked[0].Score >= strongThreshold || len(s.vecMap) == 0 || s.embedder == nil {
		sections := s.topSections(ranked, topK)
		text, err := s.llm.Answer(ctx, query, sections, history)
		if err != nil {
			return Answer{}, sessionID, fmt.Errorf("llm answer: %w", err)
		}
		answer := NewAnswer(text, citationsFor(sections), "markdown")
		s.appendTurn(ctx, sessionID, query, answer)
		return answer, sessionID, nil
	}

	queryVectors, err := s.embedder.Embed(ctx, []string{contextualQuery})
	if err != nil {
		return Answer{}, sessionID, fmt.Errorf("embed query: %w", err)
	}
	if len(queryVectors) == 0 {
		answer := s.cannotConfirm()
		s.appendTurn(ctx, sessionID, query, answer)
		return answer, sessionID, nil
	}

	sections := s.topByCosine(queryVectors[0], topK)
	if len(sections) == 0 {
		answer := s.cannotConfirm()
		s.appendTurn(ctx, sessionID, query, answer)
		return answer, sessionID, nil
	}
	text, err := s.llm.Answer(ctx, query, sections, history)
	if err != nil {
		return Answer{}, sessionID, fmt.Errorf("llm answer: %w", err)
	}
	answer := NewAnswer(text, citationsFor(sections), "vector")
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

func (s *Service) topSections(ranked []ScoredSection, k int) []Section {
	if k > len(ranked) {
		k = len(ranked)
	}
	sections := make([]Section, 0, k)
	for _, scored := range ranked[:k] {
		if scored.Score <= 0 {
			continue
		}
		if scored.Index >= 0 && scored.Index < len(s.indexed) {
			sections = append(sections, s.indexed[scored.Index])
		}
	}
	return sections
}

func (s *Service) topByCosine(queryVec []float32, k int) []Section {
	ranked := make([]ScoredSection, 0, len(s.indexed))
	for i, section := range s.indexed {
		score := Cosine(queryVec, s.vecMap[section.Citation()])
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
	return s.topSections(ranked, k)
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
