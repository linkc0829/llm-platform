package user

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/platform/auth"
	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/shared"
)

// service is the inbound port — what the handler depends on.
// Defined as an interface (not concrete *Service) so handler tests can mock.
type service interface {
	Register(ctx context.Context, in RegisterInput) (*User, string, error)
	Login(ctx context.Context, in LoginInput) (*User, string, error)
	GetByID(ctx context.Context, id shared.UserID) (*User, error)
}

type Handler struct {
	svc service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// ----------------------------------------------------------------------------
// POST /api/v1/auth/register
// ----------------------------------------------------------------------------

func (h *Handler) register(c *gin.Context) {
	var req RegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	u, token, err := h.svc.Register(ctx, req.toInput())
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusCreated, toAuthResponse(u, token))
}

// ----------------------------------------------------------------------------
// POST /api/v1/auth/login
// ----------------------------------------------------------------------------

func (h *Handler) login(c *gin.Context) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	u, token, err := h.svc.Login(ctx, req.toInput())
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, toAuthResponse(u, token))
}

// ----------------------------------------------------------------------------
// GET /api/v1/users/me
// ----------------------------------------------------------------------------

func (h *Handler) me(c *gin.Context) {
	sub := auth.UserIDFromContext(c)
	if sub == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
		return
	}
	id, err := shared.ParseUserID(sub)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid subject"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()

	u, err := h.svc.GetByID(ctx, id)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, toUserResponse(u))
}

// ----------------------------------------------------------------------------
// Error mapping (domain errors → HTTP)
// ----------------------------------------------------------------------------

func writeError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrInvalidCredentials):
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
	case errors.Is(err, ErrEmailAlreadyExists):
		c.JSON(http.StatusConflict, gin.H{"error": "email already exists"})
	case errors.Is(err, ErrUserNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
	case errors.Is(err, ErrInvalidEmail),
		errors.Is(err, ErrInvalidPassword),
		errors.Is(err, ErrInvalidDisplayName):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
	}
}
