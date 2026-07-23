package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestStdioServerExposesSearchKB(t *testing.T) {
	root := t.TempDir()
	command := exec.Command("go", "run", ".")
	command.Env = append(os.Environ(),
		"KB_LLM_MODE=fake",
		"KB_DOCS_DIR="+filepath.Join(root, "docs"),
		"KB_INDEX_DIR="+filepath.Join(root, ".kb"),
	)
	client := mcp.NewClient(&mcp.Implementation{Name: "smoke-test", Version: "v1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.CommandTransport{Command: command}, nil)
	if err != nil {
		t.Fatalf("Client.Connect() error = %v, want nil", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools() error = %v, want nil", err)
	}
	if len(tools.Tools) != 1 || tools.Tools[0].Name != "search_kb" {
		t.Fatalf("ListTools() = %#v, want only search_kb", tools.Tools)
	}

	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "search_kb", Arguments: map[string]any{"query": "Where is the report?"}})
	if err != nil {
		t.Fatalf("CallTool(search_kb) error = %v, want nil", err)
	}
	if !result.IsError {
		t.Error("CallTool(search_kb) IsError = false, want true for an unindexed KB")
	}
}
