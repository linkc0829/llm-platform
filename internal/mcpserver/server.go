// Package mcpserver exposes KB retrieval through MCP.
package mcpserver

import (
	"context"
	"fmt"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.uber.org/zap"

	"github.com/linkc0829/llm-platform/internal/kb"
	"github.com/linkc0829/llm-platform/internal/shared"
)

type searcher interface {
	ChatWithMetrics(ctx context.Context, principal shared.Principal, query, sessionID string) (kb.Answer, string, kb.RetrievalMetrics, error)
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

// New builds the stdio MCP server, whose process boundary is already trusted.
func New(svc searcher, log *zap.Logger) *mcp.Server {
	return newServer(svc, log, kb.FullAccessPrincipal(kb.AnonymousOwner))
}

// newServer builds the read-only search_kb tool.
// log records every call to stderr so an operator can see what an agent asked and what came back.
func newServer(svc searcher, log *zap.Logger, fallbackPrincipal shared.Principal) *mcp.Server {
	if log == nil {
		log = zap.NewNop()
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "knowledge-base-qa-bot", Version: "v1"}, nil)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "search_kb",
		Description: "Search the Store.POS knowledge base: UI operation procedures (login, checkout, void, reprint, reports), screen names, and the verified ViewModel / command / HTTP endpoint behind each action. Call this BEFORE answering any question about this POS app's screens, operation steps, or which API an action calls — do not answer from memory. Check the grounded field: when it is false the knowledge base did not cover the question, so say so instead of guessing.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input SearchInput) (*mcp.CallToolResult, SearchOutput, error) {
		principal := principalFromRequest(req, fallbackPrincipal)
		answer, sessionID, _, err := svc.ChatWithMetrics(ctx, principal, input.Query, input.SessionID)
		if err != nil {
			log.Error("search_kb",
				zap.String("query", input.Query),
				zap.String("principal_id", principal.ID),
				zap.Error(err),
			)
			return nil, SearchOutput{}, fmt.Errorf("search knowledge base: %w", err)
		}
		out := toSearchOutput(answer, sessionID)
		log.Info("search_kb",
			zap.String("query", input.Query),
			zap.Bool("grounded", out.Grounded),
			zap.String("strategy", out.Strategy),
			zap.Strings("sources", out.Sources),
			zap.String("principal_id", principal.ID),
			zap.String("session", sessionID),
		)
		return nil, out, nil
	})
	return server
}

func principalFromRequest(req *mcp.CallToolRequest, fallback shared.Principal) shared.Principal {
	if req == nil || req.Extra == nil || req.Extra.TokenInfo == nil {
		return fallback
	}
	principal := shared.Principal{ID: req.Extra.TokenInfo.UserID}
	if candidate, ok := req.Extra.TokenInfo.Extra["principal"].(shared.Principal); ok {
		return candidate
	}
	return principal
}

// NewStreamableHTTPHandler exposes the authenticated read-only server over the
// MCP Streamable HTTP transport. A missing token context remains an empty
// principal and is rejected by the KB service.
func NewStreamableHTTPHandler(svc searcher, log *zap.Logger) http.Handler {
	return newStreamableHTTPHandler(svc, log, shared.Principal{})
}

// NewUnauthenticatedStreamableHTTPHandler exposes the explicitly local,
// unauthenticated MCP Streamable HTTP transport.
func NewUnauthenticatedStreamableHTTPHandler(svc searcher, log *zap.Logger) http.Handler {
	return newStreamableHTTPHandler(svc, log, kb.FullAccessPrincipal(kb.AnonymousOwner))
}

func newStreamableHTTPHandler(svc searcher, log *zap.Logger, fallbackPrincipal shared.Principal) http.Handler {
	server := newServer(svc, log, fallbackPrincipal)
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return server
	}, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
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
