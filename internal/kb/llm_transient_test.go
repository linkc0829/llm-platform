package kb

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/openai/openai-go"
)

func apiError(status int, retryAfter string) error {
	response := &http.Response{StatusCode: status, Header: http.Header{}}
	if retryAfter != "" {
		response.Header.Set("Retry-After", retryAfter)
	}
	return &openai.Error{StatusCode: status, Response: response}
}

// A 429 reported as 500 told the eval runner "this request is broken" when the
// upstream had said "come back later". The runner skipped 30 of 606 questions
// on that misreading. These cases fail if the two ever collapse together again.
func TestClassifyLLMError(t *testing.T) {
	tests := []struct {
		name           string
		err            error
		wantKind       error
		wantRetryAfter string
	}{
		{
			name:           "quota_429_is_rate_limited_and_keeps_backoff_hint",
			err:            apiError(http.StatusTooManyRequests, "37"),
			wantKind:       ErrLLMRateLimited,
			wantRetryAfter: "37",
		},
		{
			name:     "429_without_header_carries_no_invented_hint",
			err:      apiError(http.StatusTooManyRequests, ""),
			wantKind: ErrLLMRateLimited,
		},
		{
			name:     "upstream_502_is_unavailable_not_rate_limited",
			err:      apiError(http.StatusBadGateway, ""),
			wantKind: ErrLLMUnavailable,
		},
		{
			name:     "deadline_exceeded_is_unavailable",
			err:      fmt.Errorf("openai chat: %w", context.DeadlineExceeded),
			wantKind: ErrLLMUnavailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyLLMError(fmt.Errorf("openai chat: %w", tt.err))
			if !errors.Is(got, tt.wantKind) {
				t.Fatalf("classifyLLMError(%v) kind = %v, want %v", tt.err, got, tt.wantKind)
			}
			if after := RetryAfterOf(got); after != tt.wantRetryAfter {
				t.Errorf("Retry-After = %q, want %q", after, tt.wantRetryAfter)
			}
		})
	}
}

// A 400 is the caller's fault and must stay a 500 to the client: retrying it
// changes nothing, so tagging it retryable would make runners loop on a bug.
func TestClassifyLLMErrorLeavesPermanentFailuresAlone(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusNotFound} {
		err := classifyLLMError(fmt.Errorf("openai chat: %w", apiError(status, "")))
		if errors.Is(err, ErrLLMRateLimited) || errors.Is(err, ErrLLMUnavailable) {
			t.Errorf("status %d classified as transient", status)
		}
	}
}

func TestHandlerChatMapsTransientLLMErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name           string
		chatErr        error
		wantStatus     int
		wantRetryAfter string
	}{
		{
			name:           "rate_limited_returns_429_with_upstream_hint",
			chatErr:        fmt.Errorf("llm answer: %w", NewLLMTransientError(ErrLLMRateLimited, "37", errors.New("429"))),
			wantStatus:     http.StatusTooManyRequests,
			wantRetryAfter: "37",
		},
		{
			name:       "unavailable_returns_503",
			chatErr:    fmt.Errorf("llm answer: %w", NewLLMTransientError(ErrLLMUnavailable, "", errors.New("502"))),
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name:       "unclassified_error_still_returns_500",
			chatErr:    errors.New("boom"),
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := gin.New()
			RegisterRoutes(r.Group(""), &Handler{svc: &fakeHandlerService{chatErr: tt.chatErr}, principal: AnonymousPrincipal},
				RouteGuards{AllowUnauthenticated: true})

			req := httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(`{"query":"how do I void an order?"}`))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Fatalf("status = %d body = %s, want %d", w.Code, w.Body.String(), tt.wantStatus)
			}
			if got := w.Header().Get("Retry-After"); got != tt.wantRetryAfter {
				t.Errorf("Retry-After = %q, want %q", got, tt.wantRetryAfter)
			}
			if strings.Contains(w.Body.String(), "429") || strings.Contains(w.Body.String(), "502") {
				t.Errorf("response leaks upstream detail: %s", w.Body.String())
			}
		})
	}
}
