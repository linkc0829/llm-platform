package kb

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
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
	if len(sections) != 4 {
		t.Fatalf("MarkdownRepo.Parse() sections = %d, want 4", len(sections))
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
	section, err := NewSection("refund_policy.md", "Refund Timeline", "Refunds take 5-7 business days.", map[string]string{"功能區": "退款"}, []string{"../screenshots/退款.png"})
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
	if saved.Sections[0].Meta["功能區"] != "退款" || len(saved.Sections[0].Images) != 1 {
		t.Errorf("saved.Sections metadata/images = %#v/%#v, want persisted values", saved.Sections[0].Meta, saved.Sections[0].Images)
	}

	loaded, err := repo.Load(context.Background())
	if err != nil {
		t.Fatalf("MarkdownRepo.Load() error = %v, want nil", err)
	}
	if len(loaded) != 1 || loaded[0].Citation() != section.Citation() || loaded[0].Body() != section.Body() || loaded[0].Meta()["功能區"] != "退款" || len(loaded[0].Images()) != 1 {
		t.Errorf("MarkdownRepo.Load() = %#v, want section %#v", loaded, section)
	}
}

func TestMarkdownRepoParseWalksNestedDirs(t *testing.T) {
	docsDir := t.TempDir()
	for _, path := range []string{filepath.Join(docsDir, "a"), filepath.Join(docsDir, "b")} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v, want nil", path, err)
		}
	}
	writeTestFile(t, filepath.Join(docsDir, "a", "one.md"), "# First\nbody")
	writeTestFile(t, filepath.Join(docsDir, "b", "one.md"), "# Second\nbody")

	sections, files, err := NewMarkdownRepo(docsDir, t.TempDir()).Parse(context.Background())
	if err != nil {
		t.Fatalf("MarkdownRepo.Parse() error = %v, want nil", err)
	}
	if files != 2 || len(sections) != 2 {
		t.Fatalf("MarkdownRepo.Parse() files/sections = %d/%d, want 2/2", files, len(sections))
	}
	if sections[0].File() == sections[1].File() || sections[0].File() != "a/one.md" || sections[1].File() != "b/one.md" {
		t.Errorf("MarkdownRepo.Parse() files = %q, %q, want a/one.md, b/one.md", sections[0].File(), sections[1].File())
	}
}

func TestMarkdownRepoParseExtractsWPFMetadataAndImages(t *testing.T) {
	docsDir := t.TempDir()
	writeTestFile(t, filepath.Join(docsDir, "登入__00_動態密碼登入.md"), "# 登入/00_動態密碼登入\n\n**功能區**: 登入\n\n**到達路徑**: start\n\n![登入/00_動態密碼登入](../screenshots/登入/00_動態密碼登入.png)\n\n## 操作\n輸入動態密碼。")

	sections, _, err := NewMarkdownRepo(docsDir, t.TempDir()).Parse(context.Background())
	if err != nil {
		t.Fatalf("MarkdownRepo.Parse() error = %v, want nil", err)
	}
	if len(sections) != 1 {
		t.Fatalf("MarkdownRepo.Parse() sections = %d, want 1", len(sections))
	}
	if sections[0].Meta()["功能區"] != "登入" || sections[0].Meta()["到達路徑"] != "start" {
		t.Errorf("MarkdownRepo.Parse() metadata = %#v, want WPF metadata", sections[0].Meta())
	}
	wantImage := "../screenshots/登入/00_動態密碼登入.png"
	if len(sections[0].Images()) != 1 || sections[0].Images()[0] != wantImage {
		t.Errorf("MarkdownRepo.Parse() images = %#v, want %#v", sections[0].Images(), []string{wantImage})
	}
	if !strings.Contains(sections[0].Body(), "## 操作") || !strings.Contains(sections[0].Body(), "輸入動態密碼") {
		t.Errorf("MarkdownRepo.Parse() body = %q, want complete WPF flow", sections[0].Body())
	}
}

func TestMarkdownRepoParseSkipsEmptyBodySections(t *testing.T) {
	docsDir := t.TempDir()
	writeTestFile(t, filepath.Join(docsDir, "doc.md"), "# H1\n## H2\nbody")

	sections, _, err := NewMarkdownRepo(docsDir, t.TempDir()).Parse(context.Background())
	if err != nil {
		t.Fatalf("MarkdownRepo.Parse() error = %v, want nil", err)
	}
	if len(sections) != 1 || sections[0].Heading() != "H2" {
		t.Errorf("MarkdownRepo.Parse() sections = %#v, want one H2 section", sections)
	}
}

func TestMarkdownRepoLoadMissingIndexReturnsErrNotIndexed(t *testing.T) {
	repo := NewMarkdownRepo(t.TempDir(), filepath.Join(t.TempDir(), ".kb"))
	_, err := repo.Load(context.Background())
	if !errors.Is(err, ErrNotIndexed) {
		t.Errorf("MarkdownRepo.Load() error = %v, want ErrNotIndexed", err)
	}
}

func TestMarkdownRepoLoadStaleAnchorVersionReturnsErrIndexStale(t *testing.T) {
	indexDir := filepath.Join(t.TempDir(), ".kb")
	if err := os.MkdirAll(indexDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v, want nil", indexDir, err)
	}
	writeTestFile(t, filepath.Join(indexDir, "index.json"), `{"anchor_version":0,"sections":[],"corpus":{}}`)
	repo := NewMarkdownRepo(t.TempDir(), indexDir)

	_, err := repo.Load(context.Background())
	if !errors.Is(err, ErrIndexStale) {
		t.Errorf("MarkdownRepo.Load() error = %v, want ErrIndexStale", err)
	}
}

func writeTestFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v, want nil", path, err)
	}
}
