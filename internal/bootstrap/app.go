// Package bootstrap is the composition root.
package bootstrap

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/platform/config"
	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/platform/httpserver"
	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/platform/logger"
	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/platform/metrics"
	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/platform/otel"
)

// App holds every wired-up resource a bootstrap-backed binary needs. main.go calls Run()
// and Shutdown().
type App struct {
	logger       *zap.Logger
	server       *httpserver.Server
	otelShutdown otel.ShutdownFunc
}

// NewApp wires the application graph.
func NewApp(ctx context.Context, cfg *config.Config) (*App, error) {
	lg, err := logger.New(logger.Config{
		Level:    cfg.Logger.Level,
		Encoding: cfg.Logger.Encoding,
	})
	if err != nil {
		return nil, fmt.Errorf("init logger: %w", err)
	}

	otelShutdown, err := otel.Setup(ctx, otel.Config{
		Enabled:     cfg.OTel.Enabled,
		Endpoint:    cfg.OTel.Endpoint,
		ServiceName: cfg.OTel.ServiceName,
	})
	if err != nil {
		return nil, fmt.Errorf("init otel: %w", err)
	}

	engine := httpserver.New(lg)
	metricsReg := metrics.New()

	engine.GET("/healthz", metrics.Health())
	engine.GET("/metrics", metricsReg.Handler())

	wireFeatures(engine)

	srv := httpserver.Wrap(engine, httpserver.Config{Port: cfg.HTTP.Port}, lg)

	return &App{
		logger:       lg,
		server:       srv,
		otelShutdown: otelShutdown,
	}, nil
}

// Logger exposes the logger so main can log fatals.
func (a *App) Logger() *zap.Logger { return a.logger }

// Run starts the HTTP server. Blocks until Shutdown is called or it errors.
func (a *App) Run() error { return a.server.Start() }

