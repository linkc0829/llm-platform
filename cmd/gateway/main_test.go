package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/linkc0829/llm-platform/internal/platform/config"
)

func TestGatewayConfigLoading(t *testing.T) {
	tempDir := t.TempDir()
	authFile := filepath.Join(tempDir, "auth.json")
	if err := os.WriteFile(authFile, []byte(`{"principals":[{"id":"p1","name":"alice","token_sha256":"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"}]}`), 0o600); err != nil {
		t.Fatalf("write auth: %v", err)
	}

	t.Setenv("GATEWAY_UPSTREAM_BASE_URL", "http://127.0.0.1:8000")
	t.Setenv("KB_AUTH_FILE", authFile)
	t.Setenv("GATEWAY_MAX_INFLIGHT", "32")
	t.Setenv("GATEWAY_MAX_INFLIGHT_PER_USER", "2")

	cfg, err := config.LoadGateway()
	if err != nil {
		t.Fatalf("LoadGateway() error = %v", err)
	}

	if cfg.Gateway.UpstreamBaseURL != "http://127.0.0.1:8000" {
		t.Errorf("UpstreamBaseURL = %q, want http://127.0.0.1:8000", cfg.Gateway.UpstreamBaseURL)
	}
	if cfg.Gateway.MaxInflightGlobal != 32 {
		t.Errorf("MaxInflightGlobal = %d, want 32", cfg.Gateway.MaxInflightGlobal)
	}
	if cfg.Gateway.MaxInflightPerUser != 2 {
		t.Errorf("MaxInflightPerUser = %d, want 2", cfg.Gateway.MaxInflightPerUser)
	}
}
