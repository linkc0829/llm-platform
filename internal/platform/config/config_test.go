package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadKBReadsEnvFileAliases(t *testing.T) {
	unsetEnv(t, "OPENAI_API_KEY")
	unsetEnv(t, "APP_PORT")

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}
	defer func() {
		_ = os.Chdir(cwd)
	}()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("OPENAI_API_KEY='test-key'\nAPP_PORT=9090\n"), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	cfg, err := LoadKB()
	if err != nil {
		t.Fatalf("LoadKB: %v", err)
	}
	if cfg.OpenAI.APIKey != "test-key" {
		t.Fatalf("OpenAI API key = %q, want test-key", cfg.OpenAI.APIKey)
	}
	if cfg.HTTP.Port != 9090 {
		t.Fatalf("HTTP port = %d, want 9090", cfg.HTTP.Port)
	}
}

func unsetEnv(t *testing.T, key string) {
	t.Helper()
	old, ok := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("unset %s: %v", key, err)
	}
	t.Cleanup(func() {
		if ok {
			if err := os.Setenv(key, old); err != nil {
				t.Fatalf("restore %s: %v", key, err)
			}
			return
		}
		if err := os.Unsetenv(key); err != nil {
			t.Fatalf("restore unset %s: %v", key, err)
		}
	})
}
