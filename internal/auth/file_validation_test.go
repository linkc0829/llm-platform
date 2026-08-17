package auth

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/zap"
)

func TestLoadFileRejectsEmptyPrincipalSet(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(path, []byte(`{"principals":[]}`), 0o600); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}

	if _, err := LoadFile(path, zap.NewNop()); err == nil {
		t.Fatalf("LoadFile(%q) error = nil, want empty-principal rejection", path)
	}
}

func TestLoadBootstrapFileAllowsEmptyPrincipalSet(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(path, []byte(`{"principals":[]}`), 0o600); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}

	store, err := LoadBootstrapFile(path, zap.NewNop())
	if err != nil {
		t.Fatalf("LoadBootstrapFile(%q) error = %v, want nil", path, err)
	}
	records, err := store.ListTokens(context.Background())
	if err != nil {
		t.Fatalf("ListTokens() error = %v, want nil", err)
	}
	if len(records) != 0 {
		t.Errorf("ListTokens() length = %d, want 0", len(records))
	}
}

func TestLoadFileRejectsUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	content := []byte(fmt.Sprintf(
		`{"principals":[{"id":"p_test","name":"alice","token_sha256":%q,"enginering":true}]}`,
		HashToken("kb_test"),
	))
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}

	if _, err := LoadFile(path, zap.NewNop()); err == nil {
		t.Fatalf("LoadFile(%q) error = nil, want unknown-field rejection", path)
	}
}
