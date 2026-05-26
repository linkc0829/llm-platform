// Package httpserver builds the gin engine with standard middleware.
package httpserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Server wraps an http.Server with graceful shutdown.
type Server struct {
	srv    *http.Server
	logger *zap.Logger
}

type Config struct {
	Port int
}

// New constructs a gin Engine with default middleware (recovery, request id,
// zap logging). Routes should be registered by feature packages.
func New(logger *zap.Logger) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(
		gin.Recovery(),
		RequestIDMiddleware(),
		ZapLoggerMiddleware(logger),
	)
	return r
}

// Wrap wraps a gin engine in a Server with graceful shutdown.
func Wrap(engine *gin.Engine, cfg Config, logger *zap.Logger) *Server {
	return &Server{
		srv: &http.Server{
			Addr:              fmt.Sprintf(":%d", cfg.Port),
			Handler:           engine,
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       30 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       120 * time.Second,
		},
		logger: logger,
	}
}

// Start blocks until the server stops.
func (s *Server) Start() error {
	s.logger.Info("http server starting", zap.String("addr", s.srv.Addr))
	if err := s.srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("http server: %w", err)
	}
	return nil
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	s.logger.Info("http server shutting down")
	return s.srv.Shutdown(ctx)
}
