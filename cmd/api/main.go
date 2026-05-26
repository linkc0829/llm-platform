package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os/signal"
	"syscall"

	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/bootstrap"
	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/platform/config"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	app, err := bootstrap.NewApp(ctx, cfg)
	if err != nil {
		log.Fatalf("bootstrap: %v", err)
	}

	// Run the HTTP server in a goroutine so we can wait for the signal.
	errs := make(chan error, 1)
	go func() {
		if err := app.Run(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errs <- err
		}
		close(errs)
	}()

	select {
	case <-ctx.Done():
		// Signal received — graceful shutdown.
	case err := <-errs:
		if err != nil {
			app.Logger().Sugar().Fatalf("server: %v", err)
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.App.ShutdownTimeout)
	defer cancel()

	app.Shutdown(shutdownCtx)
}
