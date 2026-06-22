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

func TestServiceIndexBuildsAndPersistsIndex(t *testing.T) {
	section, err := NewSection("refund_policy.md", "Refund Timeline", "Refunds take 5-7 business days.")
	if err != nil {
		t.Fatalf("NewSection() error = %v, want nil", err)
	}
	store := &fakeSectionStore{parseSections: []Section{section}, parseFiles: 3}
	svc := NewService(store)

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
	if !svc.ready || svc.corpus.N != 1 || len(svc.indexed) != 1 {
		t.Errorf("Service.Index() ready/corpus/indexed = %v/%d/%d, want true/1/1", svc.ready, svc.corpus.N, len(svc.indexed))
	}
}

func TestServiceLoadOnStartupHandlesMissingIndex(t *testing.T) {
	svc := NewService(&fakeSectionStore{loadErr: ErrNotIndexed})
	err := svc.LoadOnStartup(context.Background())
	if !errors.Is(err, ErrNotIndexed) {
		t.Errorf("Service.LoadOnStartup() error = %v, want ErrNotIndexed", err)
	}
}
