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
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

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

// Two acceptance runs could not answer "does the upstream send Retry-After?"
// because the only evidence lived on a console nobody captured. These fields
// move that answer into the log, where it survives the run.
func TestChatFailureLogsUpstreamClassAndRetryAfter(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name           string
		chatErr        error
		wantClass      string
		wantRetryAfter string
		wantFields     bool
	}{
		{
			name:           "quota_stall_records_the_hint_it_was_given",
			chatErr:        fmt.Errorf("llm answer: %w", NewLLMTransientError(ErrLLMRateLimited, "37", errors.New("429"))),
			wantClass:      ErrLLMRateLimited.Error(),
			wantRetryAfter: "37",
			wantFields:     true,
		},
		{
			// The empty value is the finding: the upstream sent no hint, so
			// honouring it cannot help and only a lower request rate will.
			name:           "quota_stall_without_a_hint_records_an_empty_one",
			chatErr:        fmt.Errorf("llm answer: %w", NewLLMTransientError(ErrLLMRateLimited, "", errors.New("429"))),
			wantClass:      ErrLLMRateLimited.Error(),
			wantRetryAfter: "",
			wantFields:     true,
		},
		{
			name:       "unclassified_failure_adds_no_upstream_fields",
			chatErr:    errors.New("boom"),
			wantFields: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			core, logs := observer.New(zap.ErrorLevel)
			r := gin.New()
			RegisterRoutes(r.Group(""), &Handler{svc: &fakeHandlerService{chatErr: tt.chatErr}, logger: zap.New(core), principal: AnonymousPrincipal},
				RouteGuards{AllowUnauthenticated: true})

			req := httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(`{"query":"how do I void an order?"}`))
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(httptest.NewRecorder(), req)

			entries := logs.FilterMessage("chat failed").All()
			if len(entries) != 1 {
				t.Fatalf("chat failed entries = %d, want 1", len(entries))
			}
			fields := entries[0].ContextMap()
			class, hasClass := fields["upstream_class"]
			after, hasAfter := fields["upstream_retry_after"]
			if hasClass != tt.wantFields || hasAfter != tt.wantFields {
				t.Fatalf("upstream fields present = (%t, %t), want %t", hasClass, hasAfter, tt.wantFields)
			}
			if !tt.wantFields {
				return
			}
			if class != tt.wantClass {
				t.Errorf("upstream_class = %v, want %q", class, tt.wantClass)
			}
			if after != tt.wantRetryAfter {
				t.Errorf("upstream_retry_after = %v, want %q", after, tt.wantRetryAfter)
			}
		})
	}
}
