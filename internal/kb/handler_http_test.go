package kb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/linkc0829/llm-platform/internal/shared"
)

type fakeHandlerService struct {
	indexFiles    int
	indexSections int
	indexErr      error
	chatAnswer    Answer
	chatSessionID string
	chatMetrics   RetrievalMetrics
	chatErr       error
	chatQuery     string
	chatOwnerID   string
	vectorsState  string
}

func (f *fakeHandlerService) Index(_ context.Context) (int, int, error) {
	return f.indexFiles, f.indexSections, f.indexErr
}

func (f *fakeHandlerService) ChatWithMetrics(_ context.Context, principal shared.Principal, query, _ string) (Answer, string, RetrievalMetrics, error) {
	f.chatQuery = query
	f.chatOwnerID = principal.ID
	return f.chatAnswer, f.chatSessionID, f.chatMetrics, f.chatErr
}

func (f *fakeHandlerService) VectorsState() string {
	if f.vectorsState != "" {
		return f.vectorsState
	}
	return "ok"
}

func TestHandlerChat(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name       string
		body       string
		svc        *fakeHandlerService
		wantStatus int
		wantBody   string
	}{
		{
			name:       "empty_query_returns_400",
			body:       `{"query":""}`,
			svc:        &fakeHandlerService{chatErr: ErrEmptyQuery},
			wantStatus: http.StatusBadRequest,
			wantBody:   `"error":"query is required"`,
		},
		{
			name:       "not_indexed_returns_200",
			body:       `{"query":"How long do refunds take?"}`,
			svc:        &fakeHandlerService{chatErr: ErrNotIndexed},
			wantStatus: http.StatusOK,
			wantBody:   "POST /index first",
		},
		{
			name: "happy_path_returns_answer_sources_strategy",
			body: `{"query":"How long do refunds take?","session_id":"s1"}`,
			svc: &fakeHandlerService{
				chatAnswer:    NewAnswer("Refunds take 5-7 business days.", []Citation{NewCitation("refund_policy.md", "refund-timeline")}, "markdown", []string{"../screenshots/refund.png"}, true),
				chatSessionID: "s1",
			},
			wantStatus: http.StatusOK,
			wantBody:   `"grounded":true,"sources":["refund_policy.md#refund-timeline"]`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := gin.New()
			RegisterRoutes(r.Group(""), &Handler{svc: tt.svc, principal: AnonymousPrincipal}, RouteGuards{AllowUnauthenticated: true})

			req := httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			r.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Fatalf("POST /chat status = %d body = %s, want %d", w.Code, w.Body.String(), tt.wantStatus)
			}
			if !strings.Contains(w.Body.String(), tt.wantBody) {
				t.Errorf("POST /chat body = %s, want substring %q", w.Body.String(), tt.wantBody)
			}
		})
	}
}

func TestHandlerIndexUsesService(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeHandlerService{indexFiles: 3, indexSections: 10}
	r := gin.New()
	RegisterRoutes(r.Group(""), &Handler{svc: svc, principal: AnonymousPrincipal}, RouteGuards{AllowUnauthenticated: true})

	req := httptest.NewRequest(http.MethodPost, "/index", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("POST /index status = %d body = %s, want 200", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"files_indexed":3`) || !strings.Contains(w.Body.String(), `"sections_indexed":10`) {
		t.Errorf("POST /index body = %s, want indexed counts", w.Body.String())
	}
}

// An audit failure returns a deliberately generic 500, so the log is the only
// route by which an operator learns which section to fix. If the citation stops
// reaching the log, /index becomes a fail-closed dead end: correct, but
// unactionable without grepping the whole corpus by hand.
func TestHandlerIndexLogsTheOffendingSection(t *testing.T) {
	gin.SetMode(gin.TestMode)
	core, logs := observer.New(zap.ErrorLevel)
	auditErr := fmt.Errorf("audit sections: %w: procedures/ADMIN/supply_period-procedure.md#步驟-1",
		ErrSectionAccessDrift)
	svc := &fakeHandlerService{indexErr: auditErr}

	r := gin.New()
	RegisterRoutes(r.Group(""), &Handler{svc: svc, logger: zap.New(core), principal: AnonymousPrincipal},
		RouteGuards{AllowUnauthenticated: true})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/index", nil))

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
	if strings.Contains(w.Body.String(), "supply_period") {
		t.Errorf("response body leaks corpus structure: %s", w.Body.String())
	}
	entries := logs.FilterMessage("index failed").All()
	if len(entries) != 1 {
		t.Fatalf("index failed log entries = %d, want 1", len(entries))
	}
	if logged := entries[0].ContextMap()["error"]; !strings.Contains(logged.(string), "supply_period-procedure.md#步驟-1") {
		t.Errorf("logged error = %q, want the offending citation", logged)
	}
}

func TestWriteErrorUsesErrorSemantics(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	writeError(c, errors.Join(errors.New("wrap"), ErrEmptyQuery))

	if w.Code != http.StatusBadRequest {
		t.Errorf("writeError(ErrEmptyQuery) status = %d body = %s, want 400", w.Code, w.Body.String())
	}
}

func TestWriteErrorMapsSessionOwnerMismatchToForbidden(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	writeError(c, errors.Join(errors.New("wrapped"), ErrSessionOwnerMismatch))

	if w.Code != http.StatusForbidden {
		t.Errorf("writeError(ErrSessionOwnerMismatch) status = %d body = %s, want 403", w.Code, w.Body.String())
	}
}

// The eval runner reads the answering model off /health, so it can record in
// each metrics file which model and decoding settings produced the round.
func TestHandlerHealthReportsChatRuntime(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name string
		chat *ChatConfig
		want string
	}{
		{
			name: "configured",
			chat: &ChatConfig{Model: "gemma-4-26b-a4b", Prompt: GroundingFingerprint(), Temperature: 0, MaxTokens: 1024},
			want: `{"status":"ok","vectors":"ok","chat":{"model":"gemma-4-26b-a4b","prompt":"` + GroundingFingerprint() + `","temperature":0,"max_tokens":1024}}`,
		},
		{name: "fake_llm_mode", chat: nil, want: `{"status":"ok","vectors":"ok"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := gin.New()
			RegisterRoutes(r.Group(""), &Handler{svc: &fakeHandlerService{}, principal: AnonymousPrincipal, chatConfig: tt.chat},
				RouteGuards{AllowUnauthenticated: true})

			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health", nil))

			if w.Code != http.StatusOK {
				t.Fatalf("GET /health status = %d, want 200", w.Code)
			}
			if w.Body.String() != tt.want {
				t.Errorf("GET /health body = %s, want %s", w.Body.String(), tt.want)
			}
		})
	}
}

func TestHandlerHealthReportsVectorsState(t *testing.T) {
	states := []string{"ok", "stale", "not_indexed", "disabled"}
	for _, state := range states {
		t.Run(state, func(t *testing.T) {
			svc := &fakeHandlerService{vectorsState: state}
			r := gin.New()
			RegisterRoutes(r.Group(""), &Handler{svc: svc, principal: AnonymousPrincipal},
				RouteGuards{AllowUnauthenticated: true})

			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health", nil))

			if w.Code != http.StatusOK {
				t.Fatalf("GET /health status = %d, want 200", w.Code)
			}

			var resp map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("unmarshal /health body: %v", err)
			}
			if _, ok := resp["vectors"]; !ok {
				t.Errorf("expected 'vectors' field in response, got %s", w.Body.String())
			}
			if resp["vectors"] != state {
				t.Errorf("vectors = %v, want %s", resp["vectors"], state)
			}
		})
	}
}

// TPS is decode speed: counting TTFT would fold queueing and prefill into it and
// a backend with a long queue would look slow at generating. A refused query made
// no model call, so it must carry no llm block rather than a row of zeros.
func TestToLLMStats(t *testing.T) {
	if got := toLLMStats(nil); got != nil {
		t.Errorf("toLLMStats(nil) = %+v, want nil", got)
	}
	got := toLLMStats(&Completion{Model: "m", PromptTokens: 100, CompletionTokens: 50, TTFT: 500 * time.Millisecond, Duration: 1500 * time.Millisecond})
	if got.TTFTMs != 500 || got.LatencyMs != 1500 || got.TPS != 50 || got.InputTokens != 100 || got.OutputTokens != 50 {
		t.Errorf("toLLMStats = %+v, want ttft 500ms, latency 1500ms, 50 tps", got)
	}
	if got := toLLMStats(&Completion{CompletionTokens: 50, Duration: time.Second}); got.TPS != 0 {
		t.Errorf("no first token: TPS = %v, want 0", got.TPS)
	}
}
