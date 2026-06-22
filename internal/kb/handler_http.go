package kb

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// service is the local inbound interface the handler depends on.
// It grows as endpoints are added in later phases.
type service interface{}

type Handler struct {
	svc service
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

func (h *Handler) health(c *gin.Context) {
	c.JSON(http.StatusOK, HealthResponse{Status: "ok"})
}
