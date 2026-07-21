package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStageMarkdown(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "kb", "01_Login", "x.md")
	asset := filepath.Join(root, "output", "01_Login", "img", "x.jpg")
	write(t, asset, "image")
	body := "---\nid: \"Store.POS--x\"\nteam: \"Store.POS\"\n---\n# X\n![x](..\\..\\output\\01_Login\\img\\x.jpg)"
	write(t, src, body)
	stage := filepath.Join(root, "stage")
	if err := stageMarkdown(src, filepath.Join(stage, "01_Login", "x.md"), stage, root); err != nil {
		t.Fatalf("stageMarkdown() error = %v, want nil", err)
	}
	staged, err := os.ReadFile(filepath.Join(stage, "01_Login", "x.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(staged), `\`) || !strings.Contains(string(staged), "../_assets/output/01_Login/img/x.jpg") {
		t.Errorf("stageMarkdown() = %q, want slash reference", staged)
	}
	if _, err := os.Stat(filepath.Join(stage, "_assets", "output", "01_Login", "img", "x.jpg")); err != nil {
		t.Errorf("stageMarkdown() asset = %v, want copied", err)
	}
}

func TestStageMarkdownRejectsUnsafeOrMissingAssets(t *testing.T) {
	tests := []struct {
		name, ref string
		want      string
	}{
		{name: "rejects_file_scheme", ref: "file:///C:/x.jpg", want: "rejected image reference"},
		{name: "rejects_absolute", ref: "/tmp/x.jpg", want: "rejected image reference"},
		{name: "missing_asset_fails", ref: "../../output/x.jpg", want: "image \"../../output/x.jpg\""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			src := filepath.Join(root, "kb", "01_Login", "x.md")
			write(t, src, "---\nid: \"Store.POS--x\"\nteam: \"Store.POS\"\n---\n# X\n![x]("+tt.ref+")")
			err := stageMarkdown(src, filepath.Join(root, "stage", "x.md"), filepath.Join(root, "stage"), root)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("stageMarkdown() error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestStageMarkdownSkipsExternalHTTPS(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "kb", "x.md")
	stage := filepath.Join(root, "stage")
	write(t, src, "---\nid: \"Store.POS--x\"\nteam: \"Store.POS\"\n---\n# X\n![x](https://example.com/x.jpg)")
	if err := stageMarkdown(src, filepath.Join(stage, "x.md"), stage, root); err != nil {
		t.Fatalf("stageMarkdown() error = %v, want nil", err)
	}
	b, err := os.ReadFile(filepath.Join(stage, "x.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "https://example.com/x.jpg") {
		t.Errorf("stageMarkdown() = %q, want external URL unchanged", b)
	}
	if _, err := os.Stat(filepath.Join(stage, "_assets")); !os.IsNotExist(err) {
		t.Errorf("stageMarkdown() assets stat = %v, want absent", err)
	}
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
