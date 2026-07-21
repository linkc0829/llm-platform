# Knowledge Base Q&A Bot

A local Go service that indexes Markdown files from `docs/`, retrieves relevant sections with BM25/vector search, and answers questions with cited sources.

**A fresh clone ships no corpus.** `docs/` and `eval/` are gitignored: they hold `make import` output, which is reproducible from a team's exporter bundle and runs to several megabytes of screenshots per team. Import at least one bundle before the service can answer anything — without it `POST /index` reports zero sections and every question is refused.

## Quick Start

```powershell
cp .env.example .env
# Set OPENAI_API_KEY, or use fake mode for quota-free local checks:
$env:KB_LLM_MODE="fake"

# Required: a fresh clone has an empty docs/. See "Import a team bundle".
make import TEAM=Store.POS FROM=C:\Protech\wpf-replay\kb

go run ./cmd/kb
```

In another terminal:

```powershell
Invoke-RestMethod -Method Post -Uri http://localhost:8080/index

Invoke-RestMethod -Method Post -Uri http://localhost:8080/chat `
  -ContentType "application/json" `
  -Body '{"query":"登入畫面有哪些按鈕?"}' |
  ConvertTo-Json -Depth 5
```

## Endpoints

- `GET /health` - health check.
- `POST /index` - parses all Markdown below `docs/`, writes `.kb/index.json`, and loads the in-memory index.
- `POST /chat` - answers a question from indexed sections only, returning `answer`, `sources`, `images`, `strategy`, and `session_id`.

## Configuration

Environment variables:

- `APP_PORT` - HTTP port, default `8080`.
- `APP_SHUTDOWN_TIMEOUT` - graceful shutdown timeout, default `10s`.
- `LOG_LEVEL` - zap log level, default `info`.
- `LOG_ENCODING` - zap encoding, default `json`.
- `OPENAI_API_KEY` - required unless `KB_LLM_MODE=fake`.
- `KB_LLM_MODE` - `openai` or `fake`, default `openai`.
- `KB_DOCS_DIR` / `KB_INDEX_DIR` - source and local index directories.

## Import a team bundle

```powershell
make import TEAM=Store.POS FROM=C:\Protech\wpf-replay\kb
Invoke-RestMethod -Method Post -Uri http://localhost:8080/index
```

Imported files live below `docs/<team>/`; screenshots are copied below that team's `_assets/` directory. `images` in `/chat` are paths relative to `KB_DOCS_DIR`, so they only resolve on a machine that has run the import — they are identifiers, not URLs a client can fetch.

The import is transactional: it validates the staged bundle against every team already present and only then swaps the team's directory into place, so a rejected bundle leaves `docs/` untouched. Re-running it for the same team replaces that team's tree and nothing else.

All imported documents currently share one access level and one index. If a team needs a separate trust boundary, run a separate instance with its own `KB_DOCS_DIR` and `KB_INDEX_DIR`.

## Layout

```text
cmd/kb/                  # bot entrypoint
internal/kb/             # domain, service, ports, handlers, markdown/vector repos, LLM adapters
internal/platform/config # env/.env config loading
internal/platform/httpserver
internal/platform/logger
thoughts/qrspi/          # QRSPI artifacts
```

Not in version control — recreate with `make import` and `POST /index`:

```text
docs/<team>/             # imported Markdown and _assets/ screenshots
eval/<team>/             # bundle eval YAML and kb_index.json
.kb/index.json
.kb/faiss_index/metadata.json
```

## Make Targets

```powershell
make run      # run ./cmd/kb
make build    # build bin/kb
make import   # import a team bundle
make test     # go test -race -short -count=1 ./...
make lint     # golangci-lint run ./...
make verify   # lint + test
```
