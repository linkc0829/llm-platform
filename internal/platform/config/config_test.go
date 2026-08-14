package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadKBReadsEnvFileAliases(t *testing.T) {
	unsetEnv(t, "OPENAI_API_KEY")
	unsetEnv(t, "APP_PORT")
	unsetEnv(t, "KB_CHAT_MODEL")
	unsetEnv(t, "KB_EMBED_BASE_URL")

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}
	defer func() {
		_ = os.Chdir(cwd)
	}()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("OPENAI_API_KEY='test-key'\nAPP_PORT=9090\nKB_CHAT_MODEL=test-model\nKB_EMBED_BASE_URL=http://embed.example/v1\nKB_EMBED_API_KEY=embed-key\nKB_GEMINI_THINKING_LEVEL=minimal\n"), 0o600); err != nil {
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
	if cfg.OpenAI.ChatModel != "test-model" {
		t.Fatalf("OpenAI chat model = %q, want test-model", cfg.OpenAI.ChatModel)
	}
	if cfg.OpenAI.EmbedBaseURL != "http://embed.example/v1" {
		t.Fatalf("OpenAI embed base URL = %q, want test URL", cfg.OpenAI.EmbedBaseURL)
	}
	if cfg.OpenAI.EmbedAPIKey != "embed-key" {
		t.Fatalf("OpenAI embed API key = %q, want embed-key", cfg.OpenAI.EmbedAPIKey)
	}
	if cfg.OpenAI.GeminiThinkingLevel != "minimal" {
		t.Fatalf("Gemini thinking level = %q, want minimal", cfg.OpenAI.GeminiThinkingLevel)
	}
}

func TestLoadKBAllowsBaseURLWithoutAPIKey(t *testing.T) {
	unsetEnv(t, "OPENAI_API_KEY")
	unsetEnv(t, "KB_LLM_MODE")
	unsetEnv(t, "OPENAI_BASE_URL")

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}
	defer func() { _ = os.Chdir(cwd) }()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("OPENAI_BASE_URL=http://localhost:11434/v1\n"), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	cfg, err := LoadKB()
	if err != nil {
		t.Fatalf("LoadKB() error = %v, want nil", err)
	}
	if cfg.OpenAI.BaseURL != "http://localhost:11434/v1" {
		t.Errorf("OpenAI base URL = %q, want Ollama URL", cfg.OpenAI.BaseURL)
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

func TestLoadKBAllowsFakeLLMWithoutAPIKey(t *testing.T) {
	unsetEnv(t, "OPENAI_API_KEY")
	unsetEnv(t, "KB_LLM_MODE")

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}
	defer func() {
		_ = os.Chdir(cwd)
	}()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("KB_LLM_MODE=fake\n"), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	cfg, err := LoadKB()
	if err != nil {
		t.Fatalf("LoadKB: %v", err)
	}
	if cfg.OpenAI.LLMMode != "fake" {
		t.Fatalf("OpenAI LLM mode = %q, want fake", cfg.OpenAI.LLMMode)
	}
}

// TestLoadKBDefaultsLoggerOutputToFile pins the default that makes the metric
// log exist at all. A typo in the viper key would leave Output empty, the logger
// would fall back to stdout only, and the absence would look exactly like a
// service nobody queried — so assert the default rather than trust the string.
func TestLoadKBDefaultsLoggerOutputToFile(t *testing.T) {
	unsetEnv(t, "LOG_OUTPUT")
	unsetEnv(t, "OPENAI_API_KEY")
	unsetEnv(t, "KB_LLM_MODE")

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}
	defer func() {
		_ = os.Chdir(cwd)
	}()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("KB_LLM_MODE=fake\n"), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	cfg, err := LoadKB()
	if err != nil {
		t.Fatalf("LoadKB: %v", err)
	}
	if cfg.Logger.Output != "stdout,log/kb.log" {
		t.Errorf("Logger.Output = %q, want stdout,log/kb.log", cfg.Logger.Output)
	}
}
