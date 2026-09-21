package kb

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/linkc0829/llm-platform/internal/shared"
)

// service is the local inbound interface the handler depends on.
// It grows as endpoints are added in later phases.
type service interface {
	Index(ctx context.Context) (filesIndexed, sectionsIndexed int, err error)
	ChatWithMetrics(ctx context.Context, principal shared.Principal, query, sessionID string) (Answer, string, RetrievalMetrics, error)
}

type Handler struct {
	svc        service
	logger     *zap.Logger
	principal  func(*gin.Context) shared.Principal
	chatConfig *ChatConfig
}

func NewHandler(svc *Service, logger *zap.Logger, principal func(*gin.Context) shared.Principal, chat *ChatConfig) *Handler {
	return &Handler{svc: svc, logger: logger, principal: principal, chatConfig: chat}
}

// AnonymousPrincipal is the explicit full-access principal for unauthenticated
// local HTTP mode.
func AnonymousPrincipal(*gin.Context) shared.Principal {
	return FullAccessPrincipal(AnonymousOwner)
}

func (h *Handler) health(c *gin.Context) {
	c.JSON(http.StatusOK, HealthResponse{Status: "ok", Chat: h.chatConfig})
}

// ponytail: public local-tool endpoint; add rate limiting before exposing beyond localhost.
func (h *Handler) index(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
	defer cancel()

	files, sections, err := h.svc.Index(ctx)
	if err != nil {
		// The response stays generic so it cannot leak corpus structure, which
		// leaves the log as the only place the operator can learn what to fix.
		// An access audit failure names every offending file#anchor, and without
		// this line "internal error" means grepping 679 sections by hand.
		if h.logger != nil {
			h.logger.Error("index failed",
				zap.Error(err),
				zap.String("request_id", c.GetString("request_id")),
			)
		}
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, IndexResponse{FilesIndexed: files, SectionsIndexed: sections})
}

// ponytail: public local-tool endpoint; add rate limiting before exposing beyond localhost.
func (h *Handler) chat(c *gin.Context) {
	var req ChatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "query is required"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	principal := shared.Principal{}
	if h.principal != nil {
		principal = h.principal(c)
	}
	answer, sessionID, metrics, err := h.svc.ChatWithMetrics(ctx, principal, req.Query, req.SessionID)
	if err != nil {
		if h.logger != nil && !errors.Is(err, ErrEmptyQuery) && !errors.Is(err, ErrNotIndexed) {
			fields := []zap.Field{
				zap.Error(err),
				zap.String("query", req.Query),
				zap.String("request_id", c.GetString("request_id")),
			}
			h.logger.Error("chat failed", append(fields, transientFields(err)...)...)
		}
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, toChatResponse(answer, sessionID, metrics))
}

func writeError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrNotIndexed):
		c.JSON(http.StatusOK, gin.H{
			"answer":  "The knowledge base has not been indexed yet. POST /index first.",
			"sources": []string{},
		})
	case errors.Is(err, ErrEmptyQuery):
		c.JSON(http.StatusBadRequest, gin.H{"error": "query is required"})
	case errors.Is(err, ErrSessionOwnerMismatch):
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
	case errors.Is(err, ErrLLMRateLimited):
		setRetryAfter(c, err)
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "upstream rate limited"})
	case errors.Is(err, ErrLLMUnavailable):
		setRetryAfter(c, err)
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "upstream temporarily unavailable"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
	}
}

// setRetryAfter echoes the upstream backoff hint when there is one. No header
// is emitted otherwise; clients back off on their own schedule rather than one
// this service invented.
func setRetryAfter(c *gin.Context, err error) {
	if after := RetryAfterOf(err); after != "" {
		c.Header("Retry-After", after)
	}
}

// transientFields records how an upstream failure was classified and what
// backoff the upstream asked for. Whether the upstream sends Retry-After at all
// decides which lever actually helps — honouring the hint, or lowering the
// request rate — and two acceptance runs ended without an answer because the
// only evidence was a retry marker on the eval runner's console, which nobody
// captured. An empty upstream_retry_after is the finding, not a gap: it means
// the upstream sent no hint.
func transientFields(err error) []zap.Field {
	var transient *LLMTransientError
	if !errors.As(err, &transient) {
		return nil
	}
	return []zap.Field{
		zap.String("upstream_class", transient.Kind.Error()),
		zap.String("upstream_retry_after", transient.RetryAfter),
	}
}
