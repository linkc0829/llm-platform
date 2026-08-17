package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadKBReadsAuthAndBindConfig(t *testing.T) {
	unsetEnv(t, "KB_AUTH_FILE")
	unsetEnv(t, "KB_AUTH_DISABLED")
	unsetEnv(t, "APP_BIND_ADDRESS")
	unsetEnv(t, "OPENAI_API_KEY")
	unsetEnv(t, "KB_LLM_MODE")

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}
	defer func() { _ = os.Chdir(cwd) }()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("KB_LLM_MODE=fake\nKB_AUTH_FILE=auth.json\nKB_AUTH_DISABLED=true\nAPP_BIND_ADDRESS=10.0.0.1\n"), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	cfg, err := LoadKB()
	if err != nil {
		t.Fatalf("LoadKB() error = %v, want nil", err)
	}
	if cfg.Auth.File != "auth.json" {
		t.Errorf("LoadKB().Auth.File = %q, want %q", cfg.Auth.File, "auth.json")
	}
	if !cfg.Auth.Disabled {
		t.Errorf("LoadKB().Auth.Disabled = false, want true")
	}
	if cfg.HTTP.BindAddress != "10.0.0.1" {
		t.Errorf("LoadKB().HTTP.BindAddress = %q, want %q", cfg.HTTP.BindAddress, "10.0.0.1")
	}
}
