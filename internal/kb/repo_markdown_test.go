package kb

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestMarkdownRepoParseSplitsDocsIntoSections(t *testing.T) {
	docsDir := t.TempDir()
	writeTestFile(t, filepath.Join(docsDir, "refund_policy.md"), "# Refund Policy\n\n## Refund Timeline\nRefunds take 5-7 business days.\n\n## Non-Refundable Items\nGift cards are final sale.\n")
	writeTestFile(t, filepath.Join(docsDir, "account_help.md"), "intro ignored\n# Account Help\n\n## Change Email Address\nChange it from settings.\n")
	writeTestFile(t, filepath.Join(docsDir, "shipping.md"), "# Shipping\n\n## Standard Shipping\nShips in 3-5 business days.\n")

	repo := NewMarkdownRepo(docsDir, filepath.Join(t.TempDir(), ".kb"))
	sections, files, err := repo.Parse(context.Background())
	if err != nil {
		t.Fatalf("MarkdownRepo.Parse() error = %v, want nil", err)
	}
	if files != 3 {
		t.Errorf("MarkdownRepo.Parse() files = %d, want 3", files)
	}
	if len(sections) != 7 {
		t.Fatalf("MarkdownRepo.Parse() sections = %d, want 7", len(sections))
	}

	byCitation := map[string]Section{}
	for _, section := range sections {
		byCitation[section.Citation()] = section
	}
	section, ok := byCitation["refund_policy.md#refund-timeline"]
	if !ok {
		t.Fatalf("MarkdownRepo.Parse() citations = %#v, want refund_policy.md#refund-timeline", byCitation)
	}
	if section.Body() != "Refunds take 5-7 business days." {
		t.Errorf("MarkdownRepo.Parse() refund timeline body = %q, want refund body", section.Body())
	}
}

func TestMarkdownRepoSaveLoadRoundTrip(t *testing.T) {
	section, err := NewSection("refund_policy.md", "Refund Timeline", "Refunds take 5-7 business days.")
	if err != nil {
		t.Fatalf("NewSection() error = %v, want nil", err)
	}
	indexDir := filepath.Join(t.TempDir(), ".kb")
	repo := NewMarkdownRepo(t.TempDir(), indexDir)

	if err := repo.Save(context.Background(), []Section{section}); err != nil {
		t.Fatalf("MarkdownRepo.Save() error = %v, want nil", err)
	}

	b, err := os.ReadFile(filepath.Join(indexDir, "index.json"))
	if err != nil {
		t.Fatalf("ReadFile(index.json) error = %v, want nil", err)
	}
	var saved indexJSON
	if err := json.Unmarshal(b, &saved); err != nil {
		t.Fatalf("json.Unmarshal(index.json) error = %v, want nil", err)
	}
	if saved.Corpus.N != 1 {
		t.Errorf("saved.Corpus.N = %d, want 1", saved.Corpus.N)
	}
	if len(saved.Sections) != 1 || saved.Sections[0].Anchor != "refund-timeline" {
		t.Errorf("saved.Sections = %#v, want one refund-timeline section", saved.Sections)
	}

	loaded, err := repo.Load(context.Background())
	if err != nil {
		t.Fatalf("MarkdownRepo.Load() error = %v, want nil", err)
	}
	if len(loaded) != 1 || loaded[0].Citation() != section.Citation() || loaded[0].Body() != section.Body() {
		t.Errorf("MarkdownRepo.Load() = %#v, want section %#v", loaded, section)
	}
}

func TestMarkdownRepoLoadMissingIndexReturnsErrNotIndexed(t *testing.T) {
	repo := NewMarkdownRepo(t.TempDir(), filepath.Join(t.TempDir(), ".kb"))
	_, err := repo.Load(context.Background())
	if !errors.Is(err, ErrNotIndexed) {
		t.Errorf("MarkdownRepo.Load() error = %v, want ErrNotIndexed", err)
	}
}

func writeTestFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v, want nil", path, err)
	}
}
