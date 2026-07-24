package kb

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
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
}

func (f *fakeHandlerService) Index(_ context.Context) (int, int, error) {
	return f.indexFiles, f.indexSections, f.indexErr
}

func (f *fakeHandlerService) ChatWithMetrics(_ context.Context, query, _ string) (Answer, string, RetrievalMetrics, error) {
	f.chatQuery = query
	return f.chatAnswer, f.chatSessionID, f.chatMetrics, f.chatErr
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
			RegisterRoutes(r.Group(""), &Handler{svc: tt.svc})

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
	RegisterRoutes(r.Group(""), &Handler{svc: svc})

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

func TestWriteErrorUsesErrorSemantics(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	writeError(c, errors.Join(errors.New("wrap"), ErrEmptyQuery))

	if w.Code != http.StatusBadRequest {
		t.Errorf("writeError(ErrEmptyQuery) status = %d body = %s, want 400", w.Code, w.Body.String())
	}
}
