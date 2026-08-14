package logger

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNewCreatesMissingLogDir covers the fresh-checkout case: log/ is gitignored,
// so it is absent on every new clone. zap opens files but never creates
// directories, so without ensureDirs the service dies at startup on an errno the
// operator cannot act on — and the metric log that justifies all of this never
// gets written.
func TestNewCreatesMissingLogDir(t *testing.T) {
	// Not t.TempDir(): zap keeps the sink open for the process lifetime and
	// Windows refuses to unlink an open file, so its cleanup would fail the test
	// after the assertions already passed. Best-effort removal instead.
	dir, err := os.MkdirTemp("", "logger-test-")
	if err != nil {
		t.Fatalf("MkdirTemp() error = %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "nested", "deeper", "kb.log")

	lg, err := New(Config{Output: "stdout," + filepath.ToSlash(path)})
	if err != nil {
		t.Fatalf("New() error = %v, want nil for a not-yet-existing directory", err)
	}
	lg.Info("hello")
	_ = lg.Sync()

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v, want the log file created", path, err)
	}
	if !strings.Contains(string(body), "hello") {
		t.Errorf("log file = %q, want it to contain the logged message", body)
	}
}

// TestWithoutStdout guards the MCP stdio transport. Its stdout carries JSON-RPC;
// a single log line written there corrupts the stream and breaks every client,
// so no LOG_OUTPUT value may result in a stdout sink.
func TestWithoutStdout(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"strips_stdout_and_adds_stderr", "stdout,log/kb.log", "stderr,log/kb.log"},
		{"stdout_only_becomes_stderr", "stdout", "stderr"},
		{"empty_becomes_stderr", "", "stderr"},
		{"keeps_existing_stderr_without_duplicating", "stderr,log/kb.log", "stderr,log/kb.log"},
		{"file_only_still_gains_stderr", "log/kb.log", "stderr,log/kb.log"},
		{"trims_whitespace", "stdout , log/kb.log", "stderr,log/kb.log"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := WithoutStdout(tt.input)
			if got != tt.want {
				t.Errorf("WithoutStdout(%q) = %q, want %q", tt.input, got, tt.want)
			}
			for _, p := range strings.Split(got, ",") {
				if p == "stdout" {
					t.Fatalf("WithoutStdout(%q) = %q, which still writes to stdout and would corrupt JSON-RPC", tt.input, got)
				}
			}
		})
	}
}

// TestNewWithoutFileSinkTouchesNoDisk pins the opt-out: LOG_OUTPUT=stdout must
// not create directories, so a caller who does not want files does not get them.
func TestNewWithoutFileSinkTouchesNoDisk(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	if _, err := New(Config{Output: "stdout"}); err != nil {
		t.Fatalf("New() error = %v, want nil", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("New() created %v, want nothing written for a stdout-only sink", entries)
	}
}
