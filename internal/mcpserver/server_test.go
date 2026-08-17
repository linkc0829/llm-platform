package mcpserver

import (
	"context"
	"errors"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.uber.org/zap"

	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/kb"
	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/shared"
)

type fakeSearcher struct {
	answer      kb.Answer
	sessionID   string
	err         error
	errForEmpty bool
	query       string
	session     string
	ownerID     string
}

func (f *fakeSearcher) ChatWithMetrics(_ context.Context, principal shared.Principal, query, sessionID string) (kb.Answer, string, kb.RetrievalMetrics, error) {
	f.query = query
	f.session = sessionID
	f.ownerID = principal.ID
	if f.errForEmpty && query == "" {
		return kb.Answer{}, "", kb.RetrievalMetrics{}, kb.ErrEmptyQuery
	}
	return f.answer, f.sessionID, kb.RetrievalMetrics{}, f.err
}

func TestPrincipalFromRequestUsesNamedExtra(t *testing.T) {
	named := shared.Principal{ID: "named", Engineering: true}
	request := &mcp.CallToolRequest{Extra: &mcp.RequestExtra{TokenInfo: &sdkauth.TokenInfo{
		UserID: "token-user",
		Extra: map[string]any{
			"other":     shared.Principal{ID: "other", Engineering: false},
			"principal": named,
		},
	}}}

	got := principalFromRequest(request, shared.Principal{ID: "fallback"})
	if !reflect.DeepEqual(got, named) {
		t.Errorf("principalFromRequest() = %#v, want named principal %#v", got, named)
	}

	request.Extra.TokenInfo.Extra["principal"] = "wrong type"
	got = principalFromRequest(request, shared.Principal{ID: "fallback"})
	want := shared.Principal{ID: "token-user"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("principalFromRequest(wrong type) = %#v, want token-user fallback %#v", got, want)
	}
}

func TestSearchKB(t *testing.T) {
	ctx := context.Background()
	searcher := &fakeSearcher{answer: kb.NewAnswer("Use Settings.", []kb.Citation{kb.NewCitation("settings.md", "printer")}, "hybrid", nil, true), sessionID: "next-session"}
	session := connect(t, New(searcher, zap.NewNop()))

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools() error = %v, want nil", err)
	}
	if len(tools.Tools) != 1 || tools.Tools[0].Name != "search_kb" {
		t.Fatalf("ListTools() = %#v, want only search_kb", tools.Tools)
	}

	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "search_kb", Arguments: map[string]any{"query": "How do I configure a printer?", "session_id": "prior-session"}})
	if err != nil {
		t.Fatalf("CallTool(search_kb) error = %v, want nil", err)
	}
	if result.IsError {
		t.Fatalf("CallTool(search_kb) IsError = true, want false")
	}
	if searcher.query != "How do I configure a printer?" || searcher.session != "prior-session" {
		t.Errorf("ChatWithMetrics() query/session = %q/%q, want forwarded input", searcher.query, searcher.session)

		if searcher.ownerID != kb.AnonymousOwner {
			t.Errorf("ChatWithMetrics() ownerID = %q, want %q for stdio mode", searcher.ownerID, kb.AnonymousOwner)
		}
	}
	got, ok := result.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("CallTool(search_kb) structured content = %T, want map[string]any", result.StructuredContent)
	}
	if got["answer"] != "Use Settings." || got["session_id"] != "next-session" || got["strategy"] != "hybrid" {
		t.Errorf("CallTool(search_kb) structured content = %#v, want answer, session ID, and strategy", got)
	}
	if got["grounded"] != true {
		t.Errorf("CallTool(search_kb) grounded = %#v, want true for a grounded answer", got["grounded"])
	}
	sources, ok := got["sources"].([]any)
	if !ok || len(sources) != 1 || sources[0] != "settings.md#printer" {
		t.Errorf("CallTool(search_kb) sources = %#v, want settings.md#printer", got["sources"])
	}
	images, ok := got["images"].([]any)
	if !ok || len(images) != 0 {
		t.Errorf("CallTool(search_kb) images = %#v, want an empty list", got["images"])
	}
	if len(result.Content) != 1 {
		t.Errorf("CallTool(search_kb) content length = %d, want 1 JSON text result", len(result.Content))
	} else if text, ok := result.Content[0].(*mcp.TextContent); !ok || !strings.Contains(text.Text, `"answer":"Use Settings."`) {
		t.Errorf("CallTool(search_kb) text content = %#v, want JSON with the answer", result.Content[0])
	}
}

// TestSearchKBReportsUngrounded locks the R9 contract: an ungrounded answer
// from the service (deny, or the model declining despite context) must surface
// as grounded=false so the agent can branch on the field, not the prose.
func TestSearchKBReportsUngrounded(t *testing.T) {
	ctx := context.Background()
	searcher := &fakeSearcher{answer: kb.NewAnswer("I cannot confirm that from the knowledge base.", nil, "", nil, false), sessionID: "s"}
	session := connect(t, New(searcher, zap.NewNop()))

	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "search_kb", Arguments: map[string]any{"query": "今天台北天氣如何?"}})
	if err != nil {
		t.Fatalf("CallTool(search_kb) error = %v, want nil", err)
	}
	got, ok := result.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("CallTool(search_kb) structured content = %T, want map[string]any", result.StructuredContent)
	}
	if got["grounded"] != false {
		t.Errorf("CallTool(search_kb) grounded = %#v, want false for an ungrounded answer", got["grounded"])
	}
}

func TestSearchKBReturnsToolErrors(t *testing.T) {
	ctx := context.Background()
	searcher := &fakeSearcher{err: kb.ErrNotIndexed}
	session := connect(t, New(searcher, zap.NewNop()))

	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "search_kb", Arguments: map[string]any{"query": "Where is the report?"}})
	if err != nil {
		t.Fatalf("CallTool(search_kb) error = %v, want nil", err)
	}
	if !result.IsError {
		t.Error("CallTool(search_kb) IsError = false, want true for an unavailable index")
	}
	if !errors.Is(searcher.err, kb.ErrNotIndexed) {
		t.Errorf("fake search error = %v, want ErrNotIndexed", searcher.err)
	}
}

func TestSearchKBReturnsSessionOwnerMismatchAsToolError(t *testing.T) {
	ctx := context.Background()
	searcher := &fakeSearcher{err: kb.ErrSessionOwnerMismatch}
	session := connect(t, New(searcher, zap.NewNop()))

	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "search_kb", Arguments: map[string]any{"query": "Where is the report?", "session_id": "claimed-by-someone-else"}})
	if err != nil {
		t.Fatalf("CallTool(search_kb) error = %v, want nil", err)
	}
	if !result.IsError {
		t.Error("CallTool(search_kb) IsError = false, want true for a session owner mismatch")
	}
	if !errors.Is(searcher.err, kb.ErrSessionOwnerMismatch) {
		t.Errorf("fake search error = %v, want ErrSessionOwnerMismatch", searcher.err)
	}
}

func TestSearchKBRejectsEmptyQuery(t *testing.T) {
	ctx := context.Background()
	session := connect(t, New(&fakeSearcher{errForEmpty: true}, zap.NewNop()))

	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "search_kb", Arguments: map[string]any{"query": ""}})
	if err != nil {
		t.Fatalf("CallTool(search_kb) error = %v, want nil", err)
	}
	if !result.IsError {
		t.Error("CallTool(search_kb) IsError = false, want true for an empty query")
	}
}

func TestStreamableHTTPServerExposesSearchKB(t *testing.T) {
	ctx := context.Background()
	searcher := &fakeSearcher{answer: kb.NewAnswer("Use Settings.", []kb.Citation{kb.NewCitation("settings.md", "printer")}, "hybrid", nil, true), sessionID: "next-session"}
	httpServer := httptest.NewServer(NewUnauthenticatedStreamableHTTPHandler(searcher, zap.NewNop()))
	t.Cleanup(httpServer.Close)

	client := mcp.NewClient(&mcp.Implementation{Name: "http-test", Version: "v1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: httpServer.URL}, nil)
	if err != nil {
		t.Fatalf("Client.Connect() error = %v, want nil", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools() error = %v, want nil", err)
	}
	if len(tools.Tools) != 1 || tools.Tools[0].Name != "search_kb" {
		t.Fatalf("ListTools() = %#v, want only search_kb", tools.Tools)
	}

	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "search_kb", Arguments: map[string]any{"query": "How do I configure a printer?"}})
	if err != nil {
		t.Fatalf("CallTool(search_kb) error = %v, want nil", err)
	}
	if result.IsError {
		t.Fatal("CallTool(search_kb) IsError = true, want false")
	}
	got, ok := result.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("CallTool(search_kb) structured content = %T, want map[string]any", result.StructuredContent)
	}
	if got["answer"] != "Use Settings." || got["grounded"] != true {
		t.Errorf("CallTool(search_kb) structured content = %#v, want grounded Use Settings answer", got)
	}
}

func TestStreamableHTTPServerReturnsToolErrors(t *testing.T) {
	ctx := context.Background()
	httpServer := httptest.NewServer(NewUnauthenticatedStreamableHTTPHandler(&fakeSearcher{err: kb.ErrNotIndexed}, zap.NewNop()))
	t.Cleanup(httpServer.Close)

	client := mcp.NewClient(&mcp.Implementation{Name: "http-test", Version: "v1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: httpServer.URL}, nil)
	if err != nil {
		t.Fatalf("Client.Connect() error = %v, want nil", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "search_kb", Arguments: map[string]any{"query": "Where is the report?"}})
	if err != nil {
		t.Fatalf("CallTool(search_kb) error = %v, want nil", err)
	}
	if !result.IsError {
		t.Error("CallTool(search_kb) IsError = false, want true for an unavailable index")
	}
}

func TestStreamableHTTPServerRejectsEmptyQuery(t *testing.T) {
	ctx := context.Background()
	httpServer := httptest.NewServer(NewUnauthenticatedStreamableHTTPHandler(&fakeSearcher{errForEmpty: true}, zap.NewNop()))
	t.Cleanup(httpServer.Close)

	client := mcp.NewClient(&mcp.Implementation{Name: "http-test", Version: "v1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: httpServer.URL}, nil)
	if err != nil {
		t.Fatalf("Client.Connect() error = %v, want nil", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "search_kb", Arguments: map[string]any{"query": ""}})
	if err != nil {
		t.Fatalf("CallTool(search_kb) error = %v, want nil", err)
	}
	if !result.IsError {
		t.Error("CallTool(search_kb) IsError = false, want true for an empty query")
	}
}

func connect(t *testing.T, server *mcp.Server) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("Server.Connect() error = %v, want nil", err)
	}
	t.Cleanup(func() { serverSession.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v1"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("Client.Connect() error = %v, want nil", err)
	}
	t.Cleanup(func() { session.Close() })
	return session
}
