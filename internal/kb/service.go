package kb

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
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
	vecMap   map[string][]float32
	corpus   Corpus
	indexed  []Section
	ready    bool
}

func NewService(sections sectionStore, llm LLM, embedder Embedder, vectors VectorStore) *Service {
	return &Service{sections: sections, llm: llm, embedder: embedder, vectors: vectors}
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
	if !s.ready {
		return Answer{}, sessionID, ErrNotIndexed
	}

	ranked := s.corpus.RankBM25(tokenize(query))
	if len(ranked) == 0 || ranked[0].Score < minThreshold {
		return s.cannotConfirm(), sessionID, nil
	}

	if ranked[0].Score >= strongThreshold || len(s.vecMap) == 0 || s.embedder == nil {
		sections := s.topSections(ranked, topK)
		text, err := s.llm.Answer(ctx, query, sections, nil)
		if err != nil {
			return Answer{}, sessionID, fmt.Errorf("llm answer: %w", err)
		}
		return NewAnswer(text, citationsFor(sections), "markdown"), sessionID, nil
	}

	queryVectors, err := s.embedder.Embed(ctx, []string{query})
	if err != nil {
		return Answer{}, sessionID, fmt.Errorf("embed query: %w", err)
	}
	if len(queryVectors) == 0 {
		return s.cannotConfirm(), sessionID, nil
	}

	sections := s.topByCosine(queryVectors[0], topK)
	if len(sections) == 0 {
		return s.cannotConfirm(), sessionID, nil
	}
	text, err := s.llm.Answer(ctx, query, sections, nil)
	if err != nil {
		return Answer{}, sessionID, fmt.Errorf("llm answer: %w", err)
	}
	return NewAnswer(text, citationsFor(sections), "vector"), sessionID, nil
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
