package kb

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCurrentCorpusClassificationBaseline(t *testing.T) {
	docsDir := filepath.Join("..", "..", "docs")
	indexDir := filepath.Join("..", "..", ".kb")
	if _, err := os.Stat(docsDir); errors.Is(err, os.ErrNotExist) {
		t.Skip("local docs corpus is not present")
	}
	repo := NewMarkdownRepo(docsDir, indexDir)
	sections, _, err := repo.Parse(context.Background())
	if err != nil {
		t.Fatalf("MarkdownRepo.Parse(current corpus) error = %v, want nil", err)
	}
	fingerprint, err := repo.fingerprint()
	if err != nil {
		t.Fatalf("MarkdownRepo.fingerprint(current corpus) error = %v, want nil", err)
	}
	const expectedFingerprint = "50347b40088c988e1722914ad5770ae2ec918d328bf7e6fe42800f5501b44fb3"
	if fingerprint != expectedFingerprint {
		t.Fatalf("current corpus fingerprint = %s, want %s; rerun the classification dry run before comparing counts", fingerprint, expectedFingerprint)
	}
	if len(sections) != 679 {
		t.Fatalf("current corpus sections = %d, want 679 for fingerprint %s", len(sections), fingerprint)
	}
	if anchorVersion != 2 {
		t.Fatalf("anchorVersion = %d, want 2 for recorded baseline", anchorVersion)
	}

	type key struct {
		docType string
		heading bool
		content bool
	}
	want := map[key]int{
		{docType: "ui_inventory"}:                            389,
		{docType: "procedure"}:                               124,
		{docType: "procedure", heading: true}:                34,
		{docType: "procedure", heading: true, content: true}: 109,
		{docType: "playlist"}:                                23,
	}
	got := map[key]int{}
	for _, section := range sections {
		if _, err := ClassifySection(section); err != nil {
			t.Fatalf("ClassifySection(%q) error = %v, want nil", section.Citation(), err)
		}
		k := key{
			docType: strings.TrimSpace(section.Meta()["doc_type"]),
			heading: strings.Contains(section.Heading(), "工程對應"),
			content: containsEngineeringEvidence(section.Body()),
		}
		got[k]++
	}
	if err := AuditSections(sections); err != nil {
		t.Fatalf("AuditSections(current corpus) error = %v, want nil", err)
	}
	for k, wantCount := range want {
		if got[k] != wantCount {
			t.Errorf("current corpus classification %s heading=%v content=%v = %d, want %d", k.docType, k.heading, k.content, got[k], wantCount)
		}
	}
	if len(got) != len(want) {
		t.Errorf("current corpus classification buckets = %d, want %d; got %#v", len(got), len(want), got)
	}
	total := 0
	for _, count := range got {
		total += count
	}
	if total != len(sections) {
		t.Errorf("current corpus classified sections = %d, want %d", total, len(sections))
	}
}
