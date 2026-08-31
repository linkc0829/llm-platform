package bootstrap

import (
	"testing"

	"github.com/linkc0829/llm-platform/internal/auth"
	"github.com/linkc0829/llm-platform/internal/platform/config"
)

func TestNewServicesKeepsValidatedAuthSnapshot(t *testing.T) {
	cfg := &config.Config{OpenAI: config.OpenAIConfig{LLMMode: "fake"}}
	store := &auth.Store{}

	services := NewServices(cfg, store)
	if services.Auth != store {
		t.Fatalf("NewServices().Auth = %p, want exact validated store %p", services.Auth, store)
	}
	if services.KB == nil {
		t.Fatal("NewServices().KB = nil, want service")
	}
}
