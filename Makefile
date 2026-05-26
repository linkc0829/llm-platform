.PHONY: help build run test test-unit test-integration lint fmt vet \
        sqlc-generate migrate-up migrate-down migrate-create mock-gen tidy clean \
        new-feature openapi-lint hooks-install verify

# ============================================================================
# Variables
# ============================================================================
BINARY_NAME := api
BUILD_DIR   := ./bin
DB_URL      := postgres://postgres:postgres@localhost:5432/app?sslmode=disable
MIGRATIONS  := ./migrations

# ============================================================================
# Help
# ============================================================================
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2}'

# ============================================================================
# Build & Run
# ============================================================================
build: ## Build api binary
	@mkdir -p $(BUILD_DIR)
	go build -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/api

run: ## Run api locally (requires local postgres+redis)
	go run ./cmd/api

# ============================================================================
# Test
# ============================================================================
test: test-unit ## Run unit tests (default)

test-unit: ## Run unit tests only (skip integration)
	go test -race -short -count=1 ./...

test-integration: ## Run integration tests (requires local postgres)
	go test -race -count=1 -tags=integration ./test/integration/...

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
# Code generation
# ============================================================================
sqlc-generate: ## Generate sqlc code
	sqlc generate

mock-gen: ## Generate mocks for all ports.go (requires mockgen)
	go generate ./...

# ============================================================================
# Migrations (requires golang-migrate)
# ============================================================================
migrate-up: ## Apply all pending migrations
	migrate -path $(MIGRATIONS) -database "$(DB_URL)" up

migrate-down: ## Rollback last migration
	migrate -path $(MIGRATIONS) -database "$(DB_URL)" down 1

migrate-create: ## Create new migration: make migrate-create NAME=add_xxx
	migrate create -ext sql -dir $(MIGRATIONS) -seq $(NAME)

# ============================================================================
# Scaffolding
# ============================================================================
new-feature: ## Scaffold a new feature: make new-feature name=foo
	@if [ -z "$(name)" ]; then echo "usage: make new-feature name=<snake_case>"; exit 1; fi
	go run ./scripts/new-feature -name='$(name)'

# ============================================================================
# API contract
# ============================================================================
openapi-lint: ## Lint api/openapi.yaml with Redocly (requires Node/npx)
	npx --yes @redocly/cli@latest lint api/openapi.yaml

# ============================================================================
# Git hooks
# ============================================================================
hooks-install: ## Point git at the repo's .githooks directory
	git config core.hooksPath .githooks
	@echo "git hooks installed from .githooks/"

# ============================================================================
# Aggregate
# ============================================================================
verify: lint test ## Run lint and unit tests (what CI / pre-commit should run)

# ============================================================================
# Cleanup
# ============================================================================
clean: ## Remove build artifacts
	rm -rf $(BUILD_DIR) coverage.out coverage.html
