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
	dryRun := false
	if override := strings.TrimSpace(os.Getenv("KB_BASELINE_DOCS")); override != "" {
		if !filepath.IsAbs(override) {
			t.Fatalf("KB_BASELINE_DOCS = %q, want an absolute path", override)
		}
		docsDir = override
		dryRun = true
	}
	if _, err := os.Stat(docsDir); errors.Is(err, os.ErrNotExist) {
		if dryRun {
			t.Fatalf("KB_BASELINE_DOCS directory %q does not exist", docsDir)
		}
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
	const expectedFingerprint = "3b017f611971611e598f62d05e396d45a99b87a619be6217e543d6c84ce7b480"
	if !dryRun && fingerprint != expectedFingerprint {
		t.Fatalf("current corpus fingerprint = %s, want %s; rerun the classification dry run before comparing counts", fingerprint, expectedFingerprint)
	}
	if !dryRun && len(sections) != 3021 {
		t.Fatalf("current corpus sections = %d, want 3021 for fingerprint %s", len(sections), fingerprint)
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
		{docType: "engineering_reference"}:                   508,
		{docType: "engineering_reference", content: true}:    243,
		{docType: "ui_inventory"}:                            955,
		{docType: "reference"}:                               450,
		{docType: "procedure"}:                               415,
		{docType: "procedure", heading: true}:                122,
		{docType: "procedure", heading: true, content: true}: 274,
		{docType: "playlist"}:                                54,
	}
	got := map[key]int{}
	tiers := map[SectionTier]int{}
	for _, section := range sections {
		tier, err := ClassifySection(section)
		if err != nil {
			t.Fatalf("ClassifySection(%q) error = %v, want nil", section.Citation(), err)
		}
		tiers[tier]++
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
		if !dryRun && got[k] != wantCount {
			t.Errorf("current corpus classification %s heading=%v content=%v = %d, want %d", k.docType, k.heading, k.content, got[k], wantCount)
		}
	}
	if !dryRun && len(got) != len(want) {
		t.Errorf("current corpus classification buckets = %d, want %d; got %#v", len(got), len(want), got)
	}
	total := 0
	for _, count := range got {
		total += count
	}
	if total != len(sections) {
		t.Errorf("current corpus classified sections = %d, want %d", total, len(sections))
	}
	if dryRun {
		t.Logf("classification dry run: docs=%s fingerprint=%s sections=%d doc_type_distribution=%#v tier_distribution=%#v", docsDir, fingerprint, len(sections), got, tiers)
	}
}
