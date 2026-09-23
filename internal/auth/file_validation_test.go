package auth

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

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

func TestReloadIfModified(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	tokAlice := "kb_alice_secret_token"
	initial := []byte(fmt.Sprintf(
		`{"principals":[{"id":"p_alice","name":"alice","token_sha256":%q}]}`,
		HashToken(tokAlice),
	))
	if err := os.WriteFile(path, initial, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	store, err := LoadFile(path, zap.NewNop())
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}

	// Verify initial token
	if _, err := store.Resolve(context.Background(), tokAlice); err != nil {
		t.Fatalf("Resolve alice: %v", err)
	}

	// Unmodified check
	reloaded, err := store.ReloadIfModified()
	if err != nil {
		t.Fatalf("ReloadIfModified: %v", err)
	}
	if reloaded {
		t.Fatal("expected reloaded=false when file unchanged")
	}

	// Overwrite with bob
	tokBob := "kb_bob_secret_token"
	updated := []byte(fmt.Sprintf(
		`{"principals":[{"id":"p_bob","name":"bob","workload":"rag","trusted":true,"token_sha256":%q}]}`,
		HashToken(tokBob),
	))
	time.Sleep(50 * time.Millisecond) // Ensure mtime advances
	if err := os.WriteFile(path, updated, 0o600); err != nil {
		t.Fatalf("WriteFile updated: %v", err)
	}

	reloaded, err = store.ReloadIfModified()
	if err != nil {
		t.Fatalf("ReloadIfModified updated: %v", err)
	}
	if !reloaded {
		t.Fatal("expected reloaded=true after file updated")
	}

	// Bob should resolve, alice should not
	pBob, err := store.Resolve(context.Background(), tokBob)
	if err != nil {
		t.Fatalf("Resolve bob: %v", err)
	}
	if pBob.Name != "bob" || pBob.Workload != "rag" || !pBob.Trusted {
		t.Errorf("pBob attributes mismatch: %+v", pBob)
	}

	if _, err := store.Resolve(context.Background(), tokAlice); err == nil {
		t.Fatal("Resolve alice should fail after reload")
	}
}

func TestReloadPreservesPreviousSnapshotOnBadFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	tokAlice := "kb_alice_secret_token"
	initial := []byte(fmt.Sprintf(
		`{"principals":[{"id":"p_alice","name":"alice","token_sha256":%q}]}`,
		HashToken(tokAlice),
	))
	if err := os.WriteFile(path, initial, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	store, err := LoadFile(path, zap.NewNop())
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}

	// Corrupt file
	if err := os.WriteFile(path, []byte(`{"principals":[{"broken"`), 0o600); err != nil {
		t.Fatalf("WriteFile corrupted: %v", err)
	}

	// Reload should error but keep old snapshot
	if err := store.Reload(); err == nil {
		t.Fatal("expected Reload() to fail on corrupted file")
	}

	// Alice should still resolve!
	p, err := store.Resolve(context.Background(), tokAlice)
	if err != nil {
		t.Fatalf("alice should still resolve from preserved snapshot: %v", err)
	}
	if p.Name != "alice" {
		t.Errorf("expected alice, got %s", p.Name)
	}
}

func TestReloadAllowsEmptyPrincipalSet(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	tokAlice := "kb_alice_secret_token"
	initial := []byte(fmt.Sprintf(
		`{"principals":[{"id":"p_alice","name":"alice","token_sha256":%q}]}`,
		HashToken(tokAlice),
	))
	if err := os.WriteFile(path, initial, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	store, err := LoadFile(path, zap.NewNop())
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}

	if _, err := store.Resolve(context.Background(), tokAlice); err != nil {
		t.Fatalf("Resolve alice: %v", err)
	}

	// Write empty principals and reload
	if err := os.WriteFile(path, []byte(`{"principals":[]}`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := store.Reload(); err != nil {
		t.Fatalf("Reload() error = %v, want nil", err)
	}

	if _, err := store.Resolve(context.Background(), tokAlice); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("Resolve alice error = %v, want ErrInvalidToken", err)
	}
}
