package kb

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

const (
	strongThreshold = 1.7
	minThreshold    = 1.2
	topK            = 3
)

type sectionStore interface {
	Parse(ctx context.Context) ([]Section, int, error)
	Save(ctx context.Context, sections []Section) error
	Load(ctx context.Context) ([]Section, error)
}

type Service struct {
	sections sectionStore
	llm      LLM
	corpus   Corpus
	indexed  []Section
	ready    bool
}

func NewService(sections sectionStore, llm LLM) *Service {
	return &Service{sections: sections, llm: llm}
}

func (s *Service) Index(ctx context.Context) (int, int, error) {
	secs, files, err := s.sections.Parse(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("parse docs: %w", err)
	}
	if err := s.sections.Save(ctx, secs); err != nil {
		return 0, 0, fmt.Errorf("save index: %w", err)
	}
	s.indexed = secs
	s.corpus = BuildCorpus(secs)
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
	s.indexed = secs
	s.corpus = BuildCorpus(secs)
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

	sections := s.topSections(ranked, topK)
	text, err := s.llm.Answer(ctx, query, sections, nil)
	if err != nil {
		return Answer{}, sessionID, fmt.Errorf("llm answer: %w", err)
	}

	if ranked[0].Score >= strongThreshold {
		return NewAnswer(text, citationsFor(sections), "markdown"), sessionID, nil
	}

	// ponytail: P3 stub, replaced by vector branch in P4.
	return NewAnswer(text, citationsFor(sections), "markdown"), sessionID, nil
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

func (s *Service) cannotConfirm() Answer {
	return NewAnswer("I cannot confirm that from the knowledge base.", nil, "")
}

func citationsFor(sections []Section) []Citation {
	citations := make([]Citation, 0, len(sections))
	for _, section := range sections {
		citations = append(citations, NewCitation(section.File(), section.Anchor()))
	}
	return citations
}
