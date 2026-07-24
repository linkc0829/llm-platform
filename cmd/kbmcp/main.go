package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/bootstrap"
	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/kb"
	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/mcpserver"
	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/platform/config"
	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/platform/logger"
)

func main() {
	cfg, err := config.LoadKB()
	if err != nil {
		fatal(err)
	}

	// stderr, never stdout: stdout carries the JSON-RPC protocol on this transport.
	lg, err := logger.New(logger.Config{Level: cfg.Logger.Level, Encoding: cfg.Logger.Encoding, Output: "stderr"})
	if err != nil {
		fatal(err)
	}
	defer func() { _ = lg.Sync() }()

	svc := bootstrap.NewKBService(cfg)
	if err := svc.LoadOnStartup(context.Background()); err != nil {
		switch {
		case errors.Is(err, kb.ErrNotIndexed), errors.Is(err, kb.ErrIndexStale), errors.Is(err, kb.ErrVectorsIgnored):
			fmt.Fprintln(os.Stderr, "knowledge base:", err)
		default:
			fatal(fmt.Errorf("load index: %w", err))
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := mcpserver.New(svc, lg).Run(ctx, &mcp.StdioTransport{}); err != nil && !errors.Is(err, context.Canceled) {
		fatal(fmt.Errorf("run MCP server: %w", err))
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "kbmcp:", err)
	os.Exit(1)
}
