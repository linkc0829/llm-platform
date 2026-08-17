package kb

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/shared"
)

// service is the local inbound interface the handler depends on.
// It grows as endpoints are added in later phases.
type service interface {
	Index(ctx context.Context) (filesIndexed, sectionsIndexed int, err error)
	ChatWithMetrics(ctx context.Context, principal shared.Principal, query, sessionID string) (Answer, string, RetrievalMetrics, error)
}

type Handler struct {
	svc       service
	logger    *zap.Logger
	principal func(*gin.Context) shared.Principal
}

func NewHandler(svc *Service, logger *zap.Logger, principal func(*gin.Context) shared.Principal) *Handler {
	return &Handler{svc: svc, logger: logger, principal: principal}
}

// AnonymousPrincipal is the explicit full-access principal for unauthenticated
// local HTTP mode.
func AnonymousPrincipal(*gin.Context) shared.Principal {
	return FullAccessPrincipal(AnonymousOwner)
}

func (h *Handler) health(c *gin.Context) {
	c.JSON(http.StatusOK, HealthResponse{Status: "ok"})
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
			h.logger.Error("chat failed",
				zap.Error(err),
				zap.String("query", req.Query),
				zap.String("request_id", c.GetString("request_id")),
			)
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
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
	}
}
