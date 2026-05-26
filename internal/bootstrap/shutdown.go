package bootstrap

import (
	"context"

	"go.uber.org/zap"
)

// Shutdown closes resources in reverse order of construction.
//
// We never return early on error — every resource gets a chance to release.
// Errors are logged so operators see what didn't shut down cleanly.
func (a *App) Shutdown(ctx context.Context) {
	// HTTP server first — stop accepting new connections.
	if err := a.server.Shutdown(ctx); err != nil {
		a.logger.Error("http server shutdown", zap.Error(err))
	}

	// OTel — flush spans before tearing down anything that may emit.
	if err := a.otelShutdown(ctx); err != nil {
		a.logger.Error("otel shutdown", zap.Error(err))
	}

	// Redis.
	if err := a.rdb.Close(); err != nil {
		a.logger.Error("redis close", zap.Error(err))
	}

	// Postgres pool.
	a.pool.Close()

	// Flush logger last (after this we cannot log).
	_ = a.logger.Sync()
}
