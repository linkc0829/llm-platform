// Package mcpserver exposes KB retrieval through MCP.
package mcpserver

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.uber.org/zap"

	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/kb"
)

type searcher interface {
	ChatWithMetrics(ctx context.Context, query, sessionID string) (kb.Answer, string, kb.RetrievalMetrics, error)
}

type SearchInput struct {
	Query     string `json:"query" jsonschema:"the knowledge-base question to answer"`
	SessionID string `json:"session_id,omitempty" jsonschema:"an optional session ID for follow-up questions"`
}

type SearchOutput struct {
	Answer    string   `json:"answer" jsonschema:"the natural-language answer; may be a refusal, so branch on grounded rather than parsing this text"`
	Grounded  bool     `json:"grounded" jsonschema:"true when the answer is backed by the knowledge base; false when retrieval fell short or the model declined to answer despite context"`
	SessionID string   `json:"session_id" jsonschema:"the session ID to reuse for follow-up questions"`
	Sources   []string `json:"sources" jsonschema:"knowledge-base citations supporting the answer"`
	Images    []string `json:"images" jsonschema:"screenshot paths associated with cited sections"`
	Strategy  string   `json:"strategy" jsonschema:"the retrieval strategy used"`
}

// New builds an MCP server with the read-only search_kb tool. log records every
// call to stderr so an operator can see what an agent asked and what came back;
// pass a stderr-backed logger (never stdout — that carries the stdio protocol).
func New(svc searcher, log *zap.Logger) *mcp.Server {
	if log == nil {
		log = zap.NewNop()
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "knowledge-base-qa-bot", Version: "v1"}, nil)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "search_kb",
		Description: "Search the local knowledge base and return a grounded answer with citations.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input SearchInput) (*mcp.CallToolResult, SearchOutput, error) {
		answer, sessionID, _, err := svc.ChatWithMetrics(ctx, input.Query, input.SessionID)
		if err != nil {
			log.Error("search_kb", zap.String("query", input.Query), zap.Error(err))
			return nil, SearchOutput{}, fmt.Errorf("search knowledge base: %w", err)
		}
		out := toSearchOutput(answer, sessionID)
		log.Info("search_kb",
			zap.String("query", input.Query),
			zap.Bool("grounded", out.Grounded),
			zap.String("strategy", out.Strategy),
			zap.Strings("sources", out.Sources),
			zap.String("session", sessionID),
		)
		return nil, out, nil
	})
	return server
}

func toSearchOutput(answer kb.Answer, sessionID string) SearchOutput {
	sources := make([]string, 0, len(answer.Sources()))
	for _, citation := range answer.Sources() {
		sources = append(sources, citation.String())
	}
	images := answer.Images()
	if images == nil {
		images = []string{}
	}
	return SearchOutput{Answer: answer.Text(), Grounded: answer.Grounded(), SessionID: sessionID, Sources: sources, Images: images, Strategy: answer.Strategy()}
}
