package atomicfile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestReplaceWritesCompleteFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	if err := Replace(path, []byte(`{"principals":[]}`), 0o600); err != nil {
		t.Fatalf("Replace(%q) error = %v, want nil", path, err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v, want nil", path, err)
	}
	if string(contents) != `{"principals":[]}` {
		t.Errorf("Replace(%q) contents = %q, want complete JSON", path, contents)
	}
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".auth.json.tmp-*"))
	if err != nil {
		t.Fatalf("Glob(temporary files) error = %v, want nil", err)
	}
	if len(matches) != 0 {
		t.Errorf("temporary files after Replace() = %v, want none", matches)
	}
}

func TestRenameRetryReturnsMissingSource(t *testing.T) {
	err := RenameRetry(filepath.Join(t.TempDir(), "missing"), filepath.Join(t.TempDir(), "target"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("RenameRetry() error = %v, want ErrNotExist", err)
	}
}
