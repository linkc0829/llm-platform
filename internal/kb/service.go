package kb

import (
	"context"
	"errors"
	"fmt"
)

type sectionStore interface {
	Parse(ctx context.Context) ([]Section, int, error)
	Save(ctx context.Context, sections []Section) error
	Load(ctx context.Context) ([]Section, error)
}

type Service struct {
	sections sectionStore
	corpus   Corpus
	indexed  []Section
	ready    bool
}

func NewService(sections sectionStore) *Service {
	return &Service{sections: sections}
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
