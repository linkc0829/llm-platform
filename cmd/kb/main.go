package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os/signal"
	"strings"
	"syscall"

	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/kb"
	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/platform/config"
	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/platform/httpserver"
	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/platform/logger"
)

func main() {
	cfg, err := config.LoadKB()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	lg, err := logger.New(logger.Config{Level: cfg.Logger.Level, Encoding: cfg.Logger.Encoding})
	if err != nil {
		log.Fatalf("logger: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	repo := kb.NewMarkdownRepo("docs", ".kb")
	vecRepo := kb.NewVectorRepo(".kb")
	sessions := kb.NewInProcStore()
	var llm kb.LLM
	var embedder kb.Embedder
	if strings.EqualFold(cfg.OpenAI.LLMMode, "fake") {
		lg.Warn("using fake LLM mode; responses are deterministic and do not call OpenAI")
		fake := kb.NewFakeLLM()
		llm = fake
		embedder = fake
	} else {
		oai := kb.NewOpenAIClient(cfg.OpenAI.APIKey)
		llm = oai
		embedder = oai
	}
	svc := kb.NewService(repo, llm, embedder, vecRepo, sessions)
	if err := svc.LoadOnStartup(ctx); err != nil {
		if errors.Is(err, kb.ErrNotIndexed) {
			lg.Warn("knowledge base not indexed yet; POST /index to build it")
		} else {
			lg.Sugar().Fatalf("load index: %v", err)
		}
	}
	h := kb.NewHandler(svc)

	engine := httpserver.New(lg)
	kb.RegisterRoutes(engine.Group(""), h)

	srv := httpserver.Wrap(engine, httpserver.Config{Port: cfg.HTTP.Port}, lg)

	errs := make(chan error, 1)
	go func() {
		if err := srv.Start(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errs <- err
		}
		close(errs)
	}()

	select {
	case <-ctx.Done():
	case err := <-errs:
		if err != nil {
			lg.Sugar().Fatalf("server: %v", err)
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.App.ShutdownTimeout)
	defer cancel()

	_ = srv.Shutdown(shutdownCtx)
	_ = lg.Sync()
}
