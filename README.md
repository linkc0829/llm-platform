# Knowledge Base Q&A Bot

A local Go service that indexes Markdown files from `docs/`, retrieves relevant sections with hybrid BM25 + vector search, and answers questions with cited, grounded sources — over HTTP (`/chat`) or MCP (`search_kb`) for coding agents.

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
- `POST /chat` - answers a question from indexed sections only, returning `answer`, `grounded`, `sources`, `images`, `strategy`, and `session_id`. `grounded` is `false` when retrieval fell below threshold or the model declined to answer despite context — branch on it instead of parsing the answer text.

## MCP for coding agents

`kbmcp` is a local stdio MCP server. It exposes one read-only tool, `search_kb`, with a required `query` and optional `session_id`. It returns the answer, an explicit `grounded` boolean, session ID, citations, image paths, and retrieval strategy. `grounded` lets an agent decide programmatically whether the KB answered, without parsing refusal prose — it is `false` when retrieval fell short or the model declined to answer despite context.

Import a bundle and build the index before starting it:

```powershell
make import TEAM=Store.POS FROM=C:\Protech\wpf-replay\kb
go run ./cmd/kb
# In another terminal: Invoke-RestMethod -Method Post -Uri http://localhost:8080/index
```

Then configure the agent to run `go run ./cmd/kbmcp` from this repository. For example:

```json
{
  "mcpServers": {
    "knowledge-base": {
      "command": "go",
      "args": ["run", "./cmd/kbmcp"],
      "cwd": "C:\\path\\to\\knowledge-base-qa-bot"
    }
  }
}
```

The same stdio command shape is supported by Codex, Claude Code, and Cline; place it in that client's MCP configuration file. MCP does not rebuild the index or expose the HTTP endpoints.

### Test the server with MCP Inspector

Verify the tool without wiring it into a client. Inspector's CLI mode speaks the real stdio protocol and prints structured output — requires Node.js and a built `.kb` index (import + `POST /index` above):

```powershell
go build -o bin/kbmcp.exe ./cmd/kbmcp

# List the tool and its schema (no LLM call):
npx -y @modelcontextprotocol/inspector --cli ./bin/kbmcp.exe --method tools/list

# Call it — a grounded question:
npx -y @modelcontextprotocol/inspector --cli ./bin/kbmcp.exe --method tools/call `
  --tool-name search_kb --tool-arg query="登入畫面有哪些按鈕?"

# An unrelated question comes back grounded:false:
npx -y @modelcontextprotocol/inspector --cli ./bin/kbmcp.exe --method tools/call `
  --tool-name search_kb --tool-arg query="今天台北天氣如何?"
```

Drop `--cli` to open the browser UI instead: `npx @modelcontextprotocol/inspector ./bin/kbmcp.exe`, then open the printed `http://localhost:6274/?MCP_PROXY_AUTH_TOKEN=...` URL and drive `search_kb` from the Tools tab. `search_kb` needs a reachable chat model (`OPENAI_BASE_URL`) to generate the answer.

## Configuration

Environment variables:

- `APP_PORT` - HTTP port, default `8080`.
- `APP_SHUTDOWN_TIMEOUT` - graceful shutdown timeout, default `10s`.
- `LOG_LEVEL` - zap log level, default `info`.
- `LOG_ENCODING` - zap encoding, default `json`.
- `OPENAI_API_KEY` - required unless `KB_LLM_MODE=fake`, or when `OPENAI_BASE_URL` points at a keyless endpoint (e.g. Ollama).
- `KB_LLM_MODE` - `openai` or `fake`, default `openai`.
- `OPENAI_BASE_URL` - OpenAI-compatible endpoint for chat and embeddings. Point it at a local Ollama (`http://localhost:11434/v1`) to keep the corpus off the network.
- `KB_EMBED_BASE_URL` - optional separate endpoint for embeddings; falls back to `OPENAI_BASE_URL`.
- `KB_CHAT_MODEL` / `KB_EMBED_MODEL` - chat and embedding model names (e.g. `llama3.1:8b` / `snowflake-arctic-embed2`).
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
cmd/kb/                  # HTTP server entrypoint (/health, /index, /chat)
cmd/kbimport/            # transactional team-bundle importer (make import)
cmd/kbmcp/               # stdio MCP server exposing the search_kb tool
internal/kb/             # domain, service, ports, handlers, markdown/vector repos, LLM adapters
internal/mcpserver/      # MCP adapter over the kb service (parallel to the HTTP handler)
internal/bootstrap/      # composition root — wires the kb service for both entrypoints
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
make build    # build bin/kb and bin/kbmcp
make import   # import a team bundle
make test     # go test -race -short -count=1 ./...
make lint     # golangci-lint run ./...
make verify   # lint + test
```
