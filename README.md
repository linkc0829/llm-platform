# Knowledge Base Q&A Bot

A local Go service that indexes Markdown files from `docs/`, retrieves relevant sections with BM25/vector search, and answers questions with cited sources.

## Quick Start

```powershell
cp .env.example .env
# Set OPENAI_API_KEY, or use fake mode for quota-free local checks:
$env:KB_LLM_MODE="fake"

go run ./cmd/kb
```

In another terminal:

```powershell
Invoke-RestMethod -Method Post -Uri http://localhost:8080/index

Invoke-RestMethod -Method Post -Uri http://localhost:8080/chat `
  -ContentType "application/json" `
  -Body '{"query":"How long do refunds take?"}' |
  ConvertTo-Json -Depth 5
```

## Endpoints

- `GET /health` - health check.
- `POST /index` - parses `docs/*.md`, writes `.kb/index.json` and `.kb/faiss_index/metadata.json`, and loads the in-memory index.
- `POST /chat` - answers a question from indexed sections only, returning `answer`, `sources`, `strategy`, and `session_id`.

## Configuration

Environment variables:

- `APP_PORT` - HTTP port, default `8080`.
- `APP_SHUTDOWN_TIMEOUT` - graceful shutdown timeout, default `10s`.
- `LOG_LEVEL` - zap log level, default `info`.
- `LOG_ENCODING` - zap encoding, default `json`.
- `OPENAI_API_KEY` - required unless `KB_LLM_MODE=fake`.
- `KB_LLM_MODE` - `openai` or `fake`, default `openai`.

## Layout

```text
cmd/kb/                  # bot entrypoint
internal/kb/             # domain, service, ports, handlers, markdown/vector repos, LLM adapters
internal/platform/config # env/.env config loading
internal/platform/httpserver
internal/platform/logger
docs/                    # Markdown knowledge base source files
thoughts/qrspi/          # QRSPI artifacts
```

Generated local artifacts:

```text
.kb/index.json
.kb/faiss_index/metadata.json
```

## Make Targets

```powershell
make run      # run ./cmd/kb
make build    # build bin/kb
make test     # go test -race -short -count=1 ./...
make lint     # golangci-lint run ./...
make verify   # lint + test
```
