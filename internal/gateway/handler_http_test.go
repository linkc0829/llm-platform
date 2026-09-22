package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/linkc0829/llm-platform/internal/shared"
)

type mockResolver struct {
	principals map[string]shared.Principal
}

func (m *mockResolver) Resolve(_ context.Context, token string) (shared.Principal, error) {
	p, ok := m.principals[token]
	if !ok {
		return shared.Principal{}, errors.New("invalid token")
	}
	return p, nil
}

type closeNotifyingRecorder struct {
	*httptest.ResponseRecorder
	closed chan bool
}

func newCloseNotifyingRecorder() *closeNotifyingRecorder {
	return &closeNotifyingRecorder{
		ResponseRecorder: httptest.NewRecorder(),
		closed:           make(chan bool, 1),
	}
}

func (c *closeNotifyingRecorder) CloseNotify() <-chan bool {
	return c.closed
}

func setupTestGateway(t *testing.T, upstreamHandler http.Handler, resolver TokenResolver, maxGlobal, maxPerUser int) (*gin.Engine, *httptest.Server) {
	gin.SetMode(gin.TestMode)
	upstream := httptest.NewServer(upstreamHandler)
	t.Cleanup(upstream.Close)

	uURL, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatalf("parse upstream url: %v", err)
	}

	limiter := NewLimiter(maxGlobal, maxPerUser)
	h := NewHandler(uURL, "upstream-secret-key", limiter, resolver, 0, zap.NewNop())

	engine := gin.New()
	RegisterRoutes(engine.Group(""), h)
	return engine, upstream
}

func TestHandler_Authentication(t *testing.T) {
	resolver := &mockResolver{
		principals: map[string]shared.Principal{
			"valid-token": {ID: "user-1", Name: "alice"},
		},
	}

	mockUpstream := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"choices":[]}`))
	})

	engine, _ := setupTestGateway(t, mockUpstream, resolver, 10, 5)

	t.Run("missing_token", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
		rec := newCloseNotifyingRecorder()
		engine.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
		}
	})

	t.Run("invalid_token", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
		req.Header.Set("Authorization", "Bearer bad-token")
		rec := newCloseNotifyingRecorder()
		engine.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
		}
	})

	t.Run("valid_token", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
		req.Header.Set("Authorization", "Bearer valid-token")
		rec := newCloseNotifyingRecorder()
		engine.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
		}
	})
}

func TestHandler_ConcurrencyLimit_ImmediateRejection(t *testing.T) {
	resolver := &mockResolver{
		principals: map[string]shared.Principal{
			"token-alice": {ID: "alice", Name: "alice"},
		},
	}

	holdUpstream := make(chan struct{})
	mockUpstream := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-holdUpstream
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	})

	// maxPerUser = 1
	engine, _ := setupTestGateway(t, mockUpstream, resolver, 10, 1)

	// Launch first request in background
	req1 := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	req1.Header.Set("Authorization", "Bearer token-alice")
	rec1 := newCloseNotifyingRecorder()

	go func() {
		engine.ServeHTTP(rec1, req1)
	}()

	time.Sleep(20 * time.Millisecond)

	core, logs := observer.New(zap.InfoLevel)
	defer zap.ReplaceGlobals(zap.New(core))()

	// Second request should immediately return 429 without waiting
	start := time.Now()
	req2 := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	req2.Header.Set("Authorization", "Bearer token-alice")
	rec2 := newCloseNotifyingRecorder()
	engine.ServeHTTP(rec2, req2)
	elapsed := time.Since(start)

	if rec2.Code != http.StatusTooManyRequests {
		t.Errorf("second request status = %d, want 429", rec2.Code)
	}
	if rec2.Header().Get("Retry-After") != "1" {
		t.Errorf("Retry-After = %q, want 1", rec2.Header().Get("Retry-After"))
	}
	if elapsed > 100*time.Millisecond {
		t.Errorf("rejection took %v, want immediate (<100ms)", elapsed)
	}

	// A rejection missing from gateway_usage would make A2's shed-not-queue
	// check blind: overload would look like silence instead of 429s.
	rejected := logs.FilterMessage("gateway_usage").FilterField(zap.Int("status", http.StatusTooManyRequests)).All()
	if len(rejected) != 1 {
		t.Fatalf("gateway_usage entries with status 429 = %d, want 1", len(rejected))
	}
	if got := rejected[0].ContextMap()["error"]; got != "user_concurrency_limit" {
		t.Errorf("rejection error = %v, want user_concurrency_limit", got)
	}

	// Release first request
	close(holdUpstream)
}

func TestHandler_TrustedImpersonation(t *testing.T) {
	resolver := &mockResolver{
		principals: map[string]shared.Principal{
			"untrusted-token": {ID: "untrusted-caller", Trusted: false},
			"trusted-service": {ID: "kb-service", Trusted: true},
		},
	}

	mockUpstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify X-On-Behalf-Of was stripped before forwarding upstream
		if r.Header.Get("X-On-Behalf-Of") != "" {
			t.Errorf("X-On-Behalf-Of should be stripped upstream, got %q", r.Header.Get("X-On-Behalf-Of"))
		}
		// Verify Authorization was swapped to upstream key
		if r.Header.Get("Authorization") != "Bearer upstream-secret-key" {
			t.Errorf("upstream Authorization = %q, want Bearer upstream-secret-key", r.Header.Get("Authorization"))
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	})

	engine, _ := setupTestGateway(t, mockUpstream, resolver, 10, 1)

	t.Run("untrusted_token_ignores_header", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
		req.Header.Set("Authorization", "Bearer untrusted-token")
		req.Header.Set("X-On-Behalf-Of", "alice")
		rec := newCloseNotifyingRecorder()
		engine.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("status = %d, want 200", rec.Code)
		}
	})

	t.Run("trusted_token_honors_header_and_enforces_user_limit", func(t *testing.T) {
		hold := make(chan struct{})
		defer close(hold)

		slowEngine, _ := setupTestGateway(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			<-hold
			w.WriteHeader(http.StatusOK)
		}), resolver, 10, 1)

		// First request on behalf of alice
		req1 := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
		req1.Header.Set("Authorization", "Bearer trusted-service")
		req1.Header.Set("X-On-Behalf-Of", "alice")
		rec1 := newCloseNotifyingRecorder()
		go slowEngine.ServeHTTP(rec1, req1)

		time.Sleep(20 * time.Millisecond)

		// Second request also on behalf of alice -> should hit 429 for alice
		req2 := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
		req2.Header.Set("Authorization", "Bearer trusted-service")
		req2.Header.Set("X-On-Behalf-Of", "alice")
		rec2 := newCloseNotifyingRecorder()
		slowEngine.ServeHTTP(rec2, req2)

		if rec2.Code != http.StatusTooManyRequests {
			t.Errorf("second request status = %d, want 429", rec2.Code)
		}

		// Third request on behalf of bob -> should succeed because bob != alice
		req3 := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
		req3.Header.Set("Authorization", "Bearer trusted-service")
		req3.Header.Set("X-On-Behalf-Of", "bob")
		rec3 := newCloseNotifyingRecorder()
		go slowEngine.ServeHTTP(rec3, req3)

		time.Sleep(20 * time.Millisecond)
		// bob was allowed in flight!
	})
}

func TestHandler_StreamBodyInjection_And_UsageExtraction(t *testing.T) {
	resolver := &mockResolver{
		principals: map[string]shared.Principal{
			"tok": {ID: "u1"},
		},
	}

	var upstreamReceivedBody []byte
	mockUpstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		upstreamReceivedBody, err = io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read upstream body: %v", err)
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		// Emit SSE chunks with delta content and final usage
		flusher, _ := w.(http.Flusher)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", `{"model":"gpt-4o","choices":[{"delta":{"content":"Hi"}}]}`)
		if flusher != nil {
			flusher.Flush()
		}
		_, _ = fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"delta":{"content":" there!"}}]}`)
		if flusher != nil {
			flusher.Flush()
		}
		_, _ = fmt.Fprintf(w, "data: %s\n\n", `{"choices":[],"usage":{"prompt_tokens":15,"completion_tokens":8,"total_tokens":23}}`)
		if flusher != nil {
			flusher.Flush()
		}
		_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
		if flusher != nil {
			flusher.Flush()
		}
	})

	engine, _ := setupTestGateway(t, mockUpstream, resolver, 10, 5)

	reqBody := `{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer tok")
	rec := newCloseNotifyingRecorder()
	engine.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	// Verify upstream received stream_options.include_usage = true
	var receivedMap map[string]any
	if err := json.Unmarshal(upstreamReceivedBody, &receivedMap); err != nil {
		t.Fatalf("unmarshal upstream body: %v", err)
	}
	opts, ok := receivedMap["stream_options"].(map[string]any)
	if !ok || opts["include_usage"] != true {
		t.Errorf("stream_options.include_usage was not injected: %#v", receivedMap["stream_options"])
	}

	// Verify client received response chunks
	bodyStr := rec.Body.String()
	if !strings.Contains(bodyStr, "Hi") || !strings.Contains(bodyStr, "there!") {
		t.Errorf("client body missing expected text chunks: %s", bodyStr)
	}
}

func TestHandler_PayloadTooLarge(t *testing.T) {
	resolver := &mockResolver{
		principals: map[string]shared.Principal{
			"tok": {ID: "u1"},
		},
	}

	mockUpstream := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	engine, _ := setupTestGateway(t, mockUpstream, resolver, 10, 5)

	// Create payload > 4MB
	oversized := bytes.Repeat([]byte("a"), (4<<20)+1024)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(oversized))
	req.Header.Set("Authorization", "Bearer tok")
	rec := newCloseNotifyingRecorder()
	engine.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413 Payload Too Large", rec.Code)
	}
}

func TestHandler_ClientCancellationPropagatesAndReleasesSlot(t *testing.T) {
	resolver := &mockResolver{
		principals: map[string]shared.Principal{
			"tok": {ID: "u1"},
		},
	}

	upstreamCanceled := make(chan struct{})
	var upstreamReceived atomic.Bool

	mockUpstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamReceived.Store(true)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"delta":{"content":"Start"}}]}`)
		if flusher != nil {
			flusher.Flush()
		}

		select {
		case <-r.Context().Done():
			close(upstreamCanceled)
		case <-time.After(2 * time.Second):
		}
	})

	engine, upstream := setupTestGateway(t, mockUpstream, resolver, 1, 1)

	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, upstream.URL+"/v1/chat/completions", strings.NewReader(`{"stream":true}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer tok")

	// Make call through gateway
	gwServer := httptest.NewServer(engine)
	defer gwServer.Close()

	gwReq, _ := http.NewRequestWithContext(ctx, http.MethodPost, gwServer.URL+"/v1/chat/completions", strings.NewReader(`{"stream":true}`))
	gwReq.Header.Set("Authorization", "Bearer tok")

	client := &http.Client{}
	resp, err := client.Do(gwReq)
	if err != nil {
		t.Fatalf("client Do error: %v", err)
	}

	// Read one chunk then cancel
	buf := make([]byte, 128)
	_, _ = resp.Body.Read(buf)
	cancel() // Cancel client request
	_ = resp.Body.Close()

	select {
	case <-upstreamCanceled:
		// Upstream context cancellation verified!
	case <-time.After(1 * time.Second):
		t.Fatal("upstream did not receive context cancellation")
	}
}

func TestSSETrackingReader_ChunkSplitAcrossReads(t *testing.T) {
	metrics := &requestMetrics{}
	startTime := time.Now()

	part1 := "data: {\"model\":\"test-model\",\"choices\":[{\"delta\":{\"c"
	part2 := "ontent\":\"hello\"}}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":20}}\n\n"

	pr, pw := io.Pipe()
	reader := newSSETrackingReader(pr, metrics, startTime)

	go func() {
		_, _ = pw.Write([]byte(part1))
		time.Sleep(10 * time.Millisecond)
		_, _ = pw.Write([]byte(part2))
		_ = pw.Close()
	}()

	out, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read all: %v", err)
	}

	if string(out) != part1+part2 {
		t.Errorf("read mismatch: %q", string(out))
	}

	prompt, comp, model, _, ttft, _ := metrics.snapshot()
	if model != "test-model" {
		t.Errorf("model = %q, want test-model", model)
	}
	if prompt != 10 || comp != 20 {
		t.Errorf("tokens = (%d, %d), want (10, 20)", prompt, comp)
	}
	if ttft <= 0 {
		t.Errorf("expected ttft > 0, got %v", ttft)
	}
}

func TestHandler_TransportConfiguration(t *testing.T) {
	uURL, _ := url.Parse("http://localhost:8080")

	// Custom timeout
	hCustom := NewHandler(uURL, "key", NewLimiter(10, 5), nil, 120*time.Second, zap.NewNop())
	trCustom, ok := hCustom.proxy.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected *http.Transport, got %T", hCustom.proxy.Transport)
	}
	if trCustom.ResponseHeaderTimeout != 120*time.Second {
		t.Errorf("ResponseHeaderTimeout = %v, want 120s", trCustom.ResponseHeaderTimeout)
	}
	if trCustom.DialContext == nil {
		t.Errorf("DialContext should be configured")
	}

	// Default fallback to 300s when <= 0
	hDefault := NewHandler(uURL, "key", NewLimiter(10, 5), nil, 0, zap.NewNop())
	trDefault, ok := hDefault.proxy.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected *http.Transport, got %T", hDefault.proxy.Transport)
	}
	if trDefault.ResponseHeaderTimeout != 300*time.Second {
		t.Errorf("default ResponseHeaderTimeout = %v, want 300s", trDefault.ResponseHeaderTimeout)
	}
}

func TestHandler_LargeIntegerPreserved(t *testing.T) {
	resolver := &mockResolver{
		principals: map[string]shared.Principal{
			"tok": {ID: "u1"},
		},
	}

	var upstreamReceivedBody []byte
	mockUpstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		upstreamReceivedBody, err = io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read upstream body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"choices":[]}`))
	})

	engine, _ := setupTestGateway(t, mockUpstream, resolver, 10, 5)

	// 9007199254740993 > 2^53 (max safe float64 integer), would become 9007199254740992 or scientific if cast to float64
	largeIntJSON := `{"model":"gpt-4o","stream":true,"seed":9007199254740993}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(largeIntJSON))
	req.Header.Set("Authorization", "Bearer tok")
	rec := newCloseNotifyingRecorder()
	engine.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	if !strings.Contains(string(upstreamReceivedBody), "9007199254740993") {
		t.Errorf("expected 9007199254740993 preserved in upstream body, got: %s", string(upstreamReceivedBody))
	}
}

func TestHandler_UpstreamFailure_Sanitized502(t *testing.T) {
	resolver := &mockResolver{
		principals: map[string]shared.Principal{
			"tok": {ID: "u1"},
		},
	}

	// Create an upstream that closes the connection immediately
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("webserver doesn't support hijacking")
		}
		conn, _, _ := hj.Hijack()
		_ = conn.Close()
	}))
	defer upstream.Close()

	uURL, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatalf("parse upstream url: %v", err)
	}

	limiter := NewLimiter(10, 5)
	h := NewHandler(uURL, "upstream-secret-key", limiter, resolver, 0, zap.NewNop())

	engine := gin.New()
	RegisterRoutes(engine.Group(""), h)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer tok")
	rec := newCloseNotifyingRecorder()
	engine.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", rec.Code)
	}
	if !strings.Contains(rec.Header().Get("Content-Type"), "application/json") {
		t.Errorf("Content-Type = %q, want application/json", rec.Header().Get("Content-Type"))
	}

	var errResp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("unmarshal error response: %v, raw: %s", err, rec.Body.String())
	}
	if errResp["error"] != "bad_gateway" {
		t.Errorf("error = %q, want bad_gateway", errResp["error"])
	}
	// Verify raw network error or upstream IP/URL is not leaked
	if strings.Contains(rec.Body.String(), upstream.URL) || strings.Contains(rec.Body.String(), "dial") {
		t.Errorf("error response leaked internal details: %s", rec.Body.String())
	}
}

func TestHandler_UpstreamTimeout_504(t *testing.T) {
	resolver := &mockResolver{
		principals: map[string]shared.Principal{
			"tok": {ID: "u1"},
		},
	}

	// Upstream handler sleeps longer than gateway header timeout
	mockUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(150 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"choices":[]}`))
	}))
	defer mockUpstream.Close()

	uURL, err := url.Parse(mockUpstream.URL)
	if err != nil {
		t.Fatalf("parse upstream url: %v", err)
	}

	// Configure a short 30ms response header timeout
	limiter := NewLimiter(10, 5)
	h := NewHandler(uURL, "upstream-secret-key", limiter, resolver, 30*time.Millisecond, zap.NewNop())

	engine := gin.New()
	RegisterRoutes(engine.Group(""), h)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer tok")
	rec := newCloseNotifyingRecorder()
	engine.ServeHTTP(rec, req)

	if rec.Code != http.StatusGatewayTimeout {
		t.Errorf("status = %d, want 504 Gateway Timeout", rec.Code)
	}
	if !strings.Contains(rec.Header().Get("Content-Type"), "application/json") {
		t.Errorf("Content-Type = %q, want application/json", rec.Header().Get("Content-Type"))
	}

	var errResp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("unmarshal error response: %v, raw: %s", err, rec.Body.String())
	}
	if errResp["error"] != "upstream_timeout" {
		t.Errorf("error = %q, want upstream_timeout", errResp["error"])
	}
}

