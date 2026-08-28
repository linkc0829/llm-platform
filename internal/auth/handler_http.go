package auth

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// Handler adapts token management use cases to HTTP. Authentication and
// authorization are installed by RegisterTokenRoutes, not inferred here.
type Handler struct {
	manager TokenManager
	actorID func(*gin.Context) string
}

func NewHandler(manager TokenManager, actorID func(*gin.Context) string) *Handler {
	return &Handler{manager: manager, actorID: actorID}
}

func (h *Handler) listTokens(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	records, err := h.manager.ListTokens(ctx)
	if err != nil {
		writeTokenError(c, err)
		return
	}
	response := make([]TokenResponse, 0, len(records))
	for _, record := range records {
		response = append(response, tokenResponse(record))
	}
	c.JSON(http.StatusOK, response)
}

func (h *Handler) createToken(c *gin.Context) {
	var request CreateTokenRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid token request"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	actorID := ""
	if h.actorID != nil {
		actorID = h.actorID(c)
	}
	// request.Admin is deliberately not copied: the network API cannot mint
	// an admin principal, even when a caller includes "admin": true.
	record, token, err := h.manager.CreateToken(ctx, actorID, TokenSpec{
		Name:        request.Name,
		Teams:       request.Teams,
		AllTeams:    request.AllTeams,
		Engineering: request.Engineering,
		Indexer:     request.Indexer,
	})
	if err != nil {
		writeTokenError(c, err)
		return
	}
	c.JSON(http.StatusCreated, CreateTokenResponse{ID: record.ID, Name: record.Name, Token: token})
}

func (h *Handler) deleteToken(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	actorID := ""
	if h.actorID != nil {
		actorID = h.actorID(c)
	}
	if err := h.manager.DeleteToken(ctx, actorID, c.Param("id")); err != nil {
		writeTokenError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func writeTokenError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrInvalidName):
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid principal name"})
	case errors.Is(err, ErrPrincipalNameExists), errors.Is(err, ErrPrincipalLimit), errors.Is(err, ErrAuthSnapshotTooLarge):
		c.JSON(http.StatusConflict, gin.H{"error": "token cannot be created"})
	case errors.Is(err, ErrPrincipalNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "principal not found"})
	case errors.Is(err, ErrAdminTokenProtected):
		c.JSON(http.StatusForbidden, gin.H{"error": "admin principal cannot be changed by the API"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
	}
}
