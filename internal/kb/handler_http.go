package kb

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// service is the local inbound interface the handler depends on.
// It grows as endpoints are added in later phases.
type service interface {
	Index(ctx context.Context) (filesIndexed, sectionsIndexed int, err error)
	Chat(ctx context.Context, query, sessionID string) (Answer, string, error)
}

type Handler struct {
	svc service
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

func (h *Handler) health(c *gin.Context) {
	c.JSON(http.StatusOK, HealthResponse{Status: "ok"})
}

// ponytail: public local-tool endpoint; add rate limiting/auth before exposing beyond localhost.
func (h *Handler) index(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
	defer cancel()

	files, sections, err := h.svc.Index(ctx)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, IndexResponse{FilesIndexed: files, SectionsIndexed: sections})
}

// ponytail: public local-tool endpoint; add rate limiting/auth before exposing beyond localhost.
func (h *Handler) chat(c *gin.Context) {
	var req ChatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "query is required"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	answer, sessionID, err := h.svc.Chat(ctx, req.Query, req.SessionID)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, toChatResponse(answer, sessionID))
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
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
	}
}
