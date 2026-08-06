// Package bootstrap wires the KB service's concrete adapters.
package bootstrap

import (
	"strings"

	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/kb"
	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/platform/config"
)

// NewKBService constructs the shared KB service for HTTP and MCP entrypoints.
func NewKBService(cfg *config.Config) *kb.Service {
	repo := kb.NewMarkdownRepo(cfg.KB.DocsDir, cfg.KB.IndexDir)
	vecRepo := kb.NewVectorRepo(cfg.KB.IndexDir)
	sessions := kb.NewInProcStore()
	var llm kb.LLM
	var embedder kb.Embedder
	if strings.EqualFold(cfg.OpenAI.LLMMode, "fake") {
		fake := kb.NewFakeLLM()
		llm = fake
		embedder = fake
	} else {
		oai := kb.NewOpenAIClient(cfg.OpenAI.APIKey, cfg.OpenAI.BaseURL, cfg.OpenAI.EmbedBaseURL, cfg.OpenAI.EmbedAPIKey, cfg.OpenAI.GeminiThinkingLevel, cfg.OpenAI.ChatModel, cfg.OpenAI.EmbedModel)
		llm = oai
		embedder = oai
	}
	return kb.NewService(repo, llm, embedder, vecRepo, sessions, cfg.OpenAI.EmbedModel)
}
