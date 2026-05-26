// Package bootstrap is the composition root.
//
// This is the ONLY place where features are wired together. Cross-feature port
// implementations are decided here. cmd/api/main.go should be a thin wrapper
// around bootstrap.NewApp + (*App).Run.
//
// AI / contributor rule: when adding a new feature, register it in wire.go,
// not here.
package bootstrap

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/platform/auth"
	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/platform/config"
	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/platform/httpserver"
	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/platform/logger"
	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/platform/metrics"
	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/platform/otel"
	pgplatform "github.com/linkc0829/go-knowledge-base-qa-bot/internal/platform/postgres"
	rdsplatform "github.com/linkc0829/go-knowledge-base-qa-bot/internal/platform/redis"
)

// App holds every wired-up resource the api binary needs. main.go calls Run()
// and Shutdown().
type App struct {
	cfg          *config.Config
	logger       *zap.Logger
	pool         *pgxpool.Pool
	rdb          *redis.Client
	authMgr      *auth.Manager
	metricsReg   *metrics.Registry
	server       *httpserver.Server
	otelShutdown otel.ShutdownFunc
}

// NewApp wires the entire application graph. Order of construction matters
// only for resources whose lifecycle depends on others.
func NewApp(ctx context.Context, cfg *config.Config) (*App, error) {
	// ----- Logger (first — everything else may log) ---------------------
	lg, err := logger.New(logger.Config{
		Level:    cfg.Logger.Level,
		Encoding: cfg.Logger.Encoding,
	})
	if err != nil {
		return nil, fmt.Errorf("init logger: %w", err)
	}

	// ----- OTel tracing ------------------------------------------------
	otelShutdown, err := otel.Setup(ctx, otel.Config{
		Enabled:     cfg.OTel.Enabled,
		Endpoint:    cfg.OTel.Endpoint,
		ServiceName: cfg.OTel.ServiceName,
	})
	if err != nil {
		return nil, fmt.Errorf("init otel: %w", err)
	}

	// ----- Datastores --------------------------------------------------
	pool, err := pgplatform.New(ctx, pgplatform.Config{
		DSN:      cfg.DB.DSN,
		MaxConns: cfg.DB.MaxConns,
		MinConns: cfg.DB.MinConns,
	})
	if err != nil {
		return nil, fmt.Errorf("init postgres: %w", err)
	}

	rdb, err := rdsplatform.New(ctx, rdsplatform.Config{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
	})
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("init redis: %w", err)
	}

	// ----- Auth manager -------------------------------------------------
	authMgr := auth.NewManager(auth.Config{
		Secret: cfg.JWT.Secret,
		Issuer: cfg.JWT.Issuer,
		TTL:    cfg.JWT.TTL,
	})

	// ----- HTTP engine + base middleware --------------------------------
	engine := httpserver.New(lg)
	metricsReg := metrics.New()

	// Health & metrics routes (no auth)
	engine.GET("/healthz", metrics.Health())
	engine.GET("/metrics", metricsReg.Handler())

	// ----- Wire feature slices -----------------------------------------
	// All cross-feature port wiring happens in wire.go.
	wireFeatures(engine, pool, rdb, authMgr, lg)

	// ----- HTTP server wrapper ------------------------------------------
	srv := httpserver.Wrap(engine, httpserver.Config{Port: cfg.HTTP.Port}, lg)

	return &App{
		cfg:          cfg,
		logger:       lg,
		pool:         pool,
		rdb:          rdb,
		authMgr:      authMgr,
		metricsReg:   metricsReg,
		server:       srv,
		otelShutdown: otelShutdown,
	}, nil
}

// Logger exposes the logger so main can log fatals.
func (a *App) Logger() *zap.Logger { return a.logger }

// Run starts the HTTP server. Blocks until Shutdown is called or it errors.
func (a *App) Run() error { return a.server.Start() }
