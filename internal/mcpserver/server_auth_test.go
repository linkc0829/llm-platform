package mcpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.uber.org/zap"

	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/auth"
	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/kb"
	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/shared"
)

type mcpTestResolver struct{}

func (mcpTestResolver) Resolve(_ context.Context, token string) (shared.Principal, error) {
	if token != "kb_valid" {
		return shared.Principal{}, auth.ErrInvalidToken
	}
	return shared.Principal{ID: "p_valid", Name: "valid"}, nil
}

func TestProtectedStreamableHTTPHandler(t *testing.T) {
	searcher := &fakeSearcher{answer: kb.NewAnswer("Use Settings.", nil, "hybrid", nil, true), sessionID: "s1"}
	handler := auth.RequireMCPBearerToken(
		mcpTestResolver{},
		NewStreamableHTTPHandler(searcher, zap.NewNop()),
	)
	httpServer := httptest.NewServer(handler)
	t.Cleanup(httpServer.Close)

	for _, tt := range []struct {
		name       string
		authorize  string
		wantStatus int
	}{
		{name: "missing_token", wantStatus: http.StatusUnauthorized},
		{name: "invalid_token", authorize: "Bearer kb_invalid", wantStatus: http.StatusUnauthorized},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodPost, httpServer.URL, nil)
			if err != nil {
				t.Fatalf("http.NewRequest(%q) error = %v, want nil", tt.authorize, err)
			}
			if tt.authorize != "" {
				req.Header.Set("Authorization", tt.authorize)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("HTTP MCP request(%q) error = %v, want nil", tt.authorize, err)
			}
			t.Cleanup(func() { _ = resp.Body.Close() })
			if resp.StatusCode != tt.wantStatus {
				t.Errorf("HTTP MCP request(%q) status = %d, want %d", tt.authorize, resp.StatusCode, tt.wantStatus)
			}
			if got := resp.Header.Get("WWW-Authenticate"); got != "" {
				t.Errorf("HTTP MCP request(%q) WWW-Authenticate = %q, want empty", tt.authorize, got)
			}
		})
	}

	clientHTTP := &http.Client{Transport: bearerRoundTripper{token: "kb_valid", base: http.DefaultTransport}}
	t.Cleanup(clientHTTP.CloseIdleConnections)
	client := mcp.NewClient(&mcp.Implementation{Name: "auth-test", Version: "v1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:             httpServer.URL,
		HTTPClient:           clientHTTP,
		DisableStandaloneSSE: true,
		MaxRetries:           -1,
	}, nil)
	if err != nil {
		t.Fatalf("MCP client Connect(valid token) error = %v, want nil", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("MCP ListTools(valid token) error = %v, want nil", err)
	}
	if len(tools.Tools) != 1 || tools.Tools[0].Name != "search_kb" {
		t.Errorf("MCP ListTools(valid token) = %#v, want search_kb", tools.Tools)
	}

	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "search_kb", Arguments: map[string]any{"query": "How do I configure a printer?"}})
	if err != nil {
		t.Fatalf("MCP CallTool(valid token) error = %v, want nil", err)
	}
	if result.IsError {
		t.Fatal("MCP CallTool(valid token) IsError = true, want false")
	}
	if searcher.ownerID != "p_valid" {
		t.Errorf("MCP search ownerID = %q, want p_valid from TokenInfo.UserID", searcher.ownerID)
	}
}

type bearerRoundTripper struct {
	token string
	base  http.RoundTripper
}

func (t bearerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(clone)
}
