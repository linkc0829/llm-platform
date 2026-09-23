package kb

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestServiceIndexSkipsUnchangedBodies(t *testing.T) {
	section := mustIncrementalSection(t, "guide.md", "Guide", "unchanged body")
	store := &fakeSectionStore{parseSections: []Section{section}, parseFiles: 1}
	embedder := &fakeEmbedder{vectors: map[string][]float32{section.Body(): {1, 0}}}
	vectors := &fakeVectorStore{}
	svc := NewService(store, nil, embedder, vectors, nil, "model")

	if _, _, err := svc.Index(context.Background()); err != nil {
		t.Fatalf("first Index() error = %v, want nil", err)
	}
	embedder.texts = nil

	if _, _, err := svc.Index(context.Background()); err != nil {
		t.Fatalf("second Index() error = %v, want nil", err)
	}
	if embedder.calls != 1 {
		t.Errorf("Embed() calls = %d, want 1", embedder.calls)
	}
	if len(embedder.texts) != 0 {
		t.Errorf("second Embed() texts = %#v, want none", embedder.texts)
	}
	if _, ok := vectors.saved[sectionBodyHash(section.Body())]; !ok {
		t.Errorf("saved vectors = %#v, want body hash key", vectors.saved)
	}
	if _, ok := vectors.saved[section.Citation()]; ok {
		t.Errorf("saved vectors = %#v, must not use citation key", vectors.saved)
	}
}

func TestServiceIndexEmbedsOnlyChangedBody(t *testing.T) {
	oldSection := mustIncrementalSection(t, "guide.md", "Guide", "old body")
	newSection := mustIncrementalSection(t, "guide.md", "Guide", "new body")
	store := &fakeSectionStore{parseSections: []Section{oldSection}, parseFiles: 1}
	embedder := &fakeEmbedder{vectors: map[string][]float32{
		oldSection.Body(): {1, 0},
		newSection.Body(): {0, 1},
	}}
	vectors := &fakeVectorStore{}
	svc := NewService(store, nil, embedder, vectors, nil, "model")

	if _, _, err := svc.Index(context.Background()); err != nil {
		t.Fatalf("first Index() error = %v, want nil", err)
	}
	store.parseSections = []Section{newSection}
	embedder.texts = nil

	if _, _, err := svc.Index(context.Background()); err != nil {
		t.Fatalf("second Index() error = %v, want nil", err)
	}
	if !sameStrings(embedder.texts, []string{newSection.Body()}) {
		t.Errorf("second Embed() texts = %#v, want changed body only", embedder.texts)
	}
	if _, ok := vectors.saved[sectionBodyHash(oldSection.Body())]; ok {
		t.Errorf("saved vectors = %#v, must prune old body hash", vectors.saved)
	}
	if _, ok := vectors.saved[sectionBodyHash(newSection.Body())]; !ok {
		t.Errorf("saved vectors = %#v, want new body hash", vectors.saved)
	}
}

func TestServiceIndexReusesBodyWhenOnlyHeadingChanges(t *testing.T) {
	oldSection := mustIncrementalSection(t, "guide.md", "Old heading", "same body")
	newSection := mustIncrementalSection(t, "guide.md", "New heading", "same body")
	store := &fakeSectionStore{parseSections: []Section{oldSection}, parseFiles: 1}
	embedder := &fakeEmbedder{vectors: map[string][]float32{oldSection.Body(): {1, 0}}}
	vectors := &fakeVectorStore{}
	svc := NewService(store, nil, embedder, vectors, nil, "model")

	if _, _, err := svc.Index(context.Background()); err != nil {
		t.Fatalf("first Index() error = %v, want nil", err)
	}
	store.parseSections = []Section{newSection}
	embedder.texts = nil

	if _, _, err := svc.Index(context.Background()); err != nil {
		t.Fatalf("second Index() error = %v, want nil", err)
	}
	if len(embedder.texts) != 0 {
		t.Errorf("second Embed() texts = %#v, want none after heading-only change", embedder.texts)
	}
	_, _, loaded, _ := svc.indexSnapshot()
	if _, ok := loaded[newSection.Citation()]; !ok {
		t.Errorf("snapshot vectors = %#v, want new citation key", loaded)
	}
	if _, ok := loaded[oldSection.Citation()]; ok {
		t.Errorf("snapshot vectors = %#v, must not retain old citation key", loaded)
	}
}

func TestServiceIndexRebuildsWhenEmbeddingModelChanges(t *testing.T) {
	section := mustIncrementalSection(t, "guide.md", "Guide", "body")
	store := &fakeSectionStore{parseSections: []Section{section}, parseFiles: 1}
	vectors := &fakeVectorStore{}
	firstEmbedder := &fakeEmbedder{vectors: map[string][]float32{section.Body(): {1, 0}}}
	first := NewService(store, nil, firstEmbedder, vectors, nil, "old-model")
	if _, _, err := first.Index(context.Background()); err != nil {
		t.Fatalf("first Index() error = %v, want nil", err)
	}

	secondEmbedder := &fakeEmbedder{vectors: map[string][]float32{section.Body(): {0, 1}}}
	second := NewService(store, nil, secondEmbedder, vectors, nil, "new-model")
	if _, _, err := second.Index(context.Background()); err != nil {
		t.Fatalf("second Index() error = %v, want nil", err)
	}
	if !sameStrings(secondEmbedder.texts, []string{section.Body()}) {
		t.Errorf("second Embed() texts = %#v, want all sections after model change", secondEmbedder.texts)
	}
	if vectors.savedModel != "new-model" {
		t.Errorf("saved model = %q, want new-model", vectors.savedModel)
	}
}

func TestServiceIndexRebuildsWhenEmbeddingEndpointChanges(t *testing.T) {
	section := mustIncrementalSection(t, "guide.md", "Guide", "body")
	store := &fakeSectionStore{parseSections: []Section{section}, parseFiles: 1}
	vectors := &fakeVectorStore{}
	firstEmbedder := &fakeEmbedder{vectors: map[string][]float32{section.Body(): {1, 0}}, identity: "model@old"}
	first := NewService(store, nil, firstEmbedder, vectors, nil, "model")
	if _, _, err := first.Index(context.Background()); err != nil {
		t.Fatalf("first Index() error = %v, want nil", err)
	}

	secondEmbedder := &fakeEmbedder{vectors: map[string][]float32{section.Body(): {0, 1}}, identity: "model@new"}
	second := NewService(store, nil, secondEmbedder, vectors, nil, "model")
	if _, _, err := second.Index(context.Background()); err != nil {
		t.Fatalf("second Index() error = %v, want nil", err)
	}
	if !sameStrings(secondEmbedder.texts, []string{section.Body()}) {
		t.Errorf("second Embed() texts = %#v, want all sections after endpoint change", secondEmbedder.texts)
	}
}

func TestServiceIndexDeduplicatesEqualBodies(t *testing.T) {
	first := mustIncrementalSection(t, "first.md", "First", "same body")
	second := mustIncrementalSection(t, "second.md", "Second", "same body")
	store := &fakeSectionStore{parseSections: []Section{first, second}, parseFiles: 2}
	embedder := &fakeEmbedder{vectors: map[string][]float32{first.Body(): {1, 0}}}
	vectors := &fakeVectorStore{}
	svc := NewService(store, nil, embedder, vectors, nil, "model")

	if _, _, err := svc.Index(context.Background()); err != nil {
		t.Fatalf("Index() error = %v, want nil", err)
	}
	if !sameStrings(embedder.texts, []string{first.Body()}) {
		t.Errorf("Embed() texts = %#v, want one deduplicated body", embedder.texts)
	}
	if len(vectors.saved) != 1 {
		t.Errorf("saved vector count = %d, want 1 unique body hash", len(vectors.saved))
	}
	_, _, loaded, _ := svc.indexSnapshot()
	if len(loaded) != 2 {
		t.Errorf("snapshot vector count = %d, want both citation keys", len(loaded))
	}
}

func TestServiceIndexPrunesRemovedBodies(t *testing.T) {
	first := mustIncrementalSection(t, "first.md", "First", "first body")
	second := mustIncrementalSection(t, "second.md", "Second", "second body")
	store := &fakeSectionStore{parseSections: []Section{first, second}, parseFiles: 2}
	embedder := &fakeEmbedder{vectors: map[string][]float32{
		first.Body():  {1, 0},
		second.Body(): {0, 1},
	}}
	vectors := &fakeVectorStore{}
	svc := NewService(store, nil, embedder, vectors, nil, "model")

	if _, _, err := svc.Index(context.Background()); err != nil {
		t.Fatalf("first Index() error = %v, want nil", err)
	}
	store.parseSections = []Section{first}
	embedder.texts = nil

	if _, _, err := svc.Index(context.Background()); err != nil {
		t.Fatalf("second Index() error = %v, want nil", err)
	}
	if embedder.calls != 1 {
		t.Errorf("Embed() calls = %d, want 1 after removing a section", embedder.calls)
	}
	if len(vectors.saved) != 1 {
		t.Errorf("saved vector count = %d, want 1 after pruning", len(vectors.saved))
	}
	if _, ok := vectors.saved[sectionBodyHash(second.Body())]; ok {
		t.Errorf("saved vectors = %#v, must not contain removed body hash", vectors.saved)
	}
}

func TestServiceIndexSaveFailureKeepsPreviousSnapshot(t *testing.T) {
	oldSection := mustIncrementalSection(t, "old.md", "Old", "old body")
	newSection := mustIncrementalSection(t, "new.md", "New", "new body")
	store := &fakeSectionStore{parseSections: []Section{newSection}, parseFiles: 1}
	embedder := &fakeEmbedder{vectors: map[string][]float32{newSection.Body(): {0, 1}}}
	vectors := &fakeVectorStore{saveErr: errors.New("disk full")}
	svc := NewService(store, nil, embedder, vectors, nil, "model")
	svc.storeIndexSnapshot([]Section{oldSection}, BuildCorpus([]Section{oldSection}), map[string][]float32{oldSection.Citation(): {1, 0}}, true)

	if _, _, err := svc.Index(context.Background()); err == nil {
		t.Fatal("Index() error = nil, want vector save error")
	}
	indexed, _, loaded, ready := svc.indexSnapshot()
	if !ready || len(indexed) != 1 || indexed[0].Citation() != oldSection.Citation() {
		t.Errorf("snapshot = ready:%v indexed:%#v, want previous section", ready, indexed)
	}
	if _, ok := loaded[oldSection.Citation()]; !ok {
		t.Errorf("snapshot vectors = %#v, want previous vector", loaded)
	}
}

func TestServiceIndexEmbedsMissingHashAndKeepsBM25Snapshot(t *testing.T) {
	first := mustIncrementalSection(t, "first.md", "First", "first body")
	second := mustIncrementalSection(t, "second.md", "Second", "second body")
	store := &fakeSectionStore{parseSections: []Section{first, second}, parseFiles: 2}
	embedder := &fakeEmbedder{vectors: map[string][]float32{second.Body(): {0, 1}}}
	vectors := &fakeVectorStore{
		loadModel:   "model",
		loadVectors: map[string][]float32{sectionBodyHash(first.Body()): {1, 0}},
	}
	svc := NewService(store, nil, embedder, vectors, nil, "model")

	if _, _, err := svc.Index(context.Background()); err != nil {
		t.Fatalf("Index() error = %v, want nil", err)
	}
	if !sameStrings(embedder.texts, []string{second.Body()}) {
		t.Errorf("Embed() texts = %#v, want missing body only", embedder.texts)
	}
	_, corpus, loaded, ready := svc.indexSnapshot()
	if !ready || corpus.N != 2 || len(loaded) != 2 {
		t.Errorf("snapshot = ready:%v corpus:%d vectors:%d, want ready/2/2", ready, corpus.N, len(loaded))
	}
}

func TestServiceLoadOnStartupRestoresHashVectorsToCitations(t *testing.T) {
	section := mustIncrementalSection(t, "guide.md", "Guide", "body")
	vectors := &fakeVectorStore{
		loadModel:   "model",
		loadVectors: map[string][]float32{sectionBodyHash(section.Body()): {1, 0}},
	}
	svc := NewService(&fakeSectionStore{loadSections: []Section{section}}, nil, nil, vectors, nil, "model")

	if err := svc.LoadOnStartup(context.Background()); err != nil {
		t.Fatalf("LoadOnStartup() error = %v, want nil", err)
	}
	_, _, loaded, ready := svc.indexSnapshot()
	if !ready {
		t.Fatal("LoadOnStartup() snapshot ready = false, want true")
	}
	if !reflect.DeepEqual(loaded, map[string][]float32{section.Citation(): {1, 0}}) {
		t.Errorf("snapshot vectors = %#v, want citation-keyed vector", loaded)
	}
}

func TestServiceLoadOnStartupDiscardsInvalidVectorFormat(t *testing.T) {
	section := mustIncrementalSection(t, "guide.md", "Guide", "body")
	vectors := &fakeVectorStore{loadErr: ErrVectorCacheFormat}
	svc := NewService(&fakeSectionStore{loadSections: []Section{section}}, nil, nil, vectors, nil, "model")

	err := svc.LoadOnStartup(context.Background())
	if !errors.Is(err, ErrVectorsIgnored) {
		t.Fatalf("LoadOnStartup() error = %v, want ErrVectorsIgnored", err)
	}
	_, _, loaded, ready := svc.indexSnapshot()
	if !ready || len(loaded) != 0 {
		t.Errorf("snapshot = ready:%v vectors:%#v, want ready BM25-only snapshot", ready, loaded)
	}
}

func TestServiceLoadOnStartupDiscardsInvalidFormatEvenWhenModelMatches(t *testing.T) {
	section := mustIncrementalSection(t, "guide.md", "Guide", "body")
	vectors := &fakeVectorStore{loadModel: "model", loadErr: ErrVectorCacheFormat}
	svc := NewService(&fakeSectionStore{loadSections: []Section{section}}, nil, nil, vectors, nil, "model")

	if err := svc.LoadOnStartup(context.Background()); !errors.Is(err, ErrVectorsIgnored) {
		t.Errorf("LoadOnStartup() error = %v, want ErrVectorsIgnored", err)
	}
}

func TestServiceLoadOnStartupMissingVectorFileReportsStale(t *testing.T) {
	section := mustIncrementalSection(t, "guide.md", "Guide", "body")
	vectors := &fakeVectorStore{}
	svc := NewService(&fakeSectionStore{loadSections: []Section{section}}, nil, nil, vectors, nil, "model")

	err := svc.LoadOnStartup(context.Background())
	if !errors.Is(err, ErrVectorsIgnored) {
		t.Fatalf("LoadOnStartup() error = %v, want ErrVectorsIgnored", err)
	}
	if got := svc.VectorsState(); got != "stale" {
		t.Errorf("VectorsState() = %q, want stale", got)
	}
	_, _, loaded, ready := svc.indexSnapshot()
	if !ready || len(loaded) != 0 {
		t.Errorf("snapshot = ready:%v vectors:%#v, want ready BM25-only snapshot", ready, loaded)
	}
}

func mustIncrementalSection(t *testing.T, file, heading, body string) Section {
	t.Helper()
	section, err := NewSection(file, heading, body, map[string]string{"doc_type": "procedure"}, nil)
	if err != nil {
		t.Fatalf("NewSection(%q, %q) error = %v, want nil", file, heading, err)
	}
	return section
}
