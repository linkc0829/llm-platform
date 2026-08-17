.PHONY: help build run mcp import test test-unit lint fmt vet tidy clean hooks-install verify

# ============================================================================
# Variables
# ============================================================================
BINARY_NAME := kb
BUILD_DIR   := bin

# ============================================================================
# Help
# ============================================================================
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2}'

# ============================================================================
# Build & Run
# ============================================================================
build: ## Build kb binary
	@if not exist $(BUILD_DIR) mkdir $(BUILD_DIR)
	go build -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/kb
	go build -o $(BUILD_DIR)/kbmcp ./cmd/kbmcp
	go build -o $(BUILD_DIR)/kbtoken ./cmd/kbtoken

run: ## Run kb locally (HTTP API + /mcp endpoint)
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
clean: ## Remove build artifacts
	@if exist $(BUILD_DIR) rmdir /s /q $(BUILD_DIR)
	@if exist coverage.out del coverage.out
	@if exist coverage.html del coverage.html
