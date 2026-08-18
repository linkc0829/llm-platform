.PHONY: help build run mcp import test test-unit test-cover lint fmt vet tidy clean hooks-install verify

# ============================================================================
# Variables
# ============================================================================
BINARY_NAME := kb
BUILD_DIR   := bin
# go build -o writes the name verbatim, so Windows needs the suffix spelled
# out. The README and run_eng_eval.py both invoke bin/kbmcp.exe.
EXE         := .exe

# ============================================================================
# Help
# ============================================================================
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2}'

# ============================================================================
# Build & Run
# ============================================================================
build: ## Build kb, kbmcp and kbtoken into bin/
	go build -o $(BUILD_DIR)/$(BINARY_NAME)$(EXE) ./cmd/kb
	go build -o $(BUILD_DIR)/kbmcp$(EXE) ./cmd/kbmcp
	go build -o $(BUILD_DIR)/kbtoken$(EXE) ./cmd/kbtoken

run: ## Run kb locally (needs $$env:KB_AUTH_FILE, or KB_AUTH_DISABLED=true)
	go run ./cmd/kb

mcp: ## Run the stdio MCP server (dev / Inspector; prefer make run)
	go run ./cmd/kbmcp

import: ## Import a team bundle: make import TEAM=X FROM=Y
	go run ./cmd/kbimport -team $(TEAM) -from $(FROM)

# ============================================================================
# Test
# ============================================================================
test: test-unit ## Run unit tests (default)

test-unit: ## Run unit tests only
	go test -race -short -count=1 ./...

test-cover: ## Run tests with coverage
	go test -race -short -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

# ============================================================================
# Code quality
# ============================================================================
lint: ## Run golangci-lint
	golangci-lint run ./...

fmt: ## Format code
	gofmt -s -w .

vet: ## Run go vet
	go vet ./...

tidy: ## Tidy go.mod
	go mod tidy

# ============================================================================
# Git hooks
# ============================================================================
hooks-install: ## Point git at the repo's .githooks directory
	git config core.hooksPath .githooks
	@echo "git hooks installed from .githooks/"

# ============================================================================
# Aggregate
# ============================================================================
verify: lint test ## Run lint and unit tests

# ============================================================================
# Cleanup
# ============================================================================
# cmd.exe syntax: run from PowerShell, not Git Bash.
clean: ## Remove build artifacts (PowerShell/cmd only)
	@if exist $(BUILD_DIR) rmdir /s /q $(BUILD_DIR)
	@if exist coverage.out del coverage.out
	@if exist coverage.html del coverage.html
