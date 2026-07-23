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
)

func main() {
	cfg, err := config.LoadKB()
	if err != nil {
		fatal(err)
	}

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
	if err := mcpserver.New(svc).Run(ctx, &mcp.StdioTransport{}); err != nil && !errors.Is(err, context.Canceled) {
		fatal(fmt.Errorf("run MCP server: %w", err))
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "kbmcp:", err)
	os.Exit(1)
}
