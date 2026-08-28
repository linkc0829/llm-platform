package lockfile

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAcquireIsExclusiveUntilRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json.lock")
	first, err := Acquire(path)
	if err != nil {
		t.Fatalf("Acquire(%q) first error = %v, want nil", path, err)
	}
	second, err := Acquire(path)
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("Acquire(%q) second error = %v, want ErrLocked", path, err)
	}
	if second != nil {
		t.Fatal("Acquire() second lock = non-nil, want nil")
	}
	if !strings.Contains(err.Error(), "remove this stale lock file manually") {
		t.Errorf("Acquire() stale-lock error = %q, want cleanup instruction", err)
	}
	if err := first.Release(); err != nil {
		t.Fatalf("Release() error = %v, want nil", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("lock path after Release() stat error = %v, want not exist", err)
	}
	third, err := Acquire(path)
	if err != nil {
		t.Fatalf("Acquire(%q) after release error = %v, want nil", path, err)
	}
	if err := third.Release(); err != nil {
		t.Fatalf("Release() third error = %v, want nil", err)
	}
}
