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
Invoke-RestMethod -Method Post -Uri http://localhost:12598/index

Invoke-RestMethod -Method Post -Uri http://localhost:12598/chat `
  -ContentType "application/json" `
  -Body '{"query":"登入畫面有哪些按鈕?"}' |
  ConvertTo-Json -Depth 5
```

## Endpoints

- `GET /health` - health check.
- `POST /index` - parses all Markdown below `docs/`, writes `.kb/index.json`, and loads the in-memory index.
- `POST /chat` - answers a question from indexed sections only, returning `answer`, `grounded`, `sources`, `images`, `strategy`, and `session_id`. `grounded` is `false` when retrieval fell below threshold or the model declined to answer despite context — branch on it instead of parsing the answer text.
- `/mcp` - Streamable HTTP MCP endpoint exposing the read-only `search_kb` tool.

## MCP for coding agents

One read-only tool, `search_kb`, with a required `query` and optional `session_id`. It returns the answer, an explicit `grounded` boolean, session ID, citations, image paths, and retrieval strategy. `grounded` lets an agent decide programmatically whether the KB answered, without parsing refusal prose — it is `false` when retrieval fell short or the model declined to answer despite context.

The same tool is served over two transports, backed by the same service and index:

| Transport | Binary | When |
| --- | --- | --- |
| Streamable HTTP, `/mcp` | `cmd/kb` (`make run`) | **Default.** One process, one index in memory, one query log |
| stdio | `cmd/kbmcp` (`make mcp`) | Development, MCP Inspector, or a client that cannot speak Streamable HTTP |

Prefer HTTP. Each stdio client spawns its own process with its own copy of the index, and its queries land in a separate log file, which makes the metrics in `log/kb.log` incomplete.

Import a bundle and build the index before starting either one:

```powershell
make import TEAM=Store.POS FROM=C:\Protech\wpf-replay\kb
go run ./cmd/kb
# In another terminal: Invoke-RestMethod -Method Post -Uri http://localhost:12598/index
```

### Shared intranet server (default)

Run `cmd/kb` on the central host after importing and indexing its knowledge base. Its MCP endpoint is `http://192.168.17.139:12598/mcp`; configure OpenCode 1.1.34 or later with:

```jsonc
{
  "mcp": {
    "knowledge_base": {
      "type": "remote",
      "url": "http://192.168.17.139:12598/mcp",
      "enabled": true,
      "timeout": 60000
    }
  }
}
```

Allow TCP port 12598 only from approved company network ranges in Windows Firewall. This service has no application-layer authentication: `/health`, `/index`, `/chat`, and `/mcp` are all reachable by any permitted network client.

### Local stdio server (development)

`make mcp` runs `cmd/kbmcp`, the stdio transport of the same server. Use it when driving MCP Inspector (below), or for a client that only accepts a `command`. Point the client at this repository:

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

- `APP_PORT` - HTTP port, default `12598`.
- `APP_SHUTDOWN_TIMEOUT` - graceful shutdown timeout, default `10s`.
- `LOG_LEVEL` - zap log level, default `info`.
- `LOG_ENCODING` - zap encoding, default `json`. The `jq` recipes for the query log assume `json`.
- `LOG_OUTPUT` - comma-separated log sinks, default `stdout,log/kb.log`. Missing directories are created. Set `stdout` alone to stop writing files. `cmd/kbmcp` drops any `stdout` sink regardless: its stdout carries the JSON-RPC protocol.
- `OPENAI_API_KEY` - required unless `KB_LLM_MODE=fake`, or when `OPENAI_BASE_URL` points at a keyless endpoint (e.g. Ollama).
- `KB_LLM_MODE` - `openai` or `fake`, default `openai`.
- `OPENAI_BASE_URL` - OpenAI-compatible endpoint for chat and embeddings. Point it at a local Ollama (`http://localhost:11434/v1`) to keep the corpus off the network.
- `KB_EMBED_BASE_URL` - optional separate endpoint for embeddings; falls back to `OPENAI_BASE_URL`.
- `KB_EMBED_API_KEY` - optional API key for embeddings; defaults to `OPENAI_API_KEY` when unset.
- `KB_GEMINI_THINKING_LEVEL` - optional Gemini OpenAI-compatible thinking level; set `minimal` to disable Gemma 4 thinking.
- `KB_CHAT_MODEL` / `KB_EMBED_MODEL` - chat and embedding model names (e.g. `llama3.1:8b` / `snowflake-arctic-embed2`).
- `KB_DOCS_DIR` / `KB_INDEX_DIR` - source and local index directories.

## Retrieval

Two-way recall, fused, then truncated. There is no reranking stage.

```text
query ─┬─ BM25          → top candidateK (20)
       └─ vector cosine → top candidateK (20)
                ↓
           FuseRRF (rrfK 60)
                ↓
       topSections(..., topK)  → sent to the model
```

The knobs are constants in `internal/kb/service.go`, not environment variables — changing
one means a rebuild **and a service restart**, and a stale binary silently reports the old
behaviour as if the change had no effect.

RRF fuses by *rank*, never by content. Two sections of the same document at ranks 3 and 4
are indistinguishable to it, so the only lever for "the answering section ranked 6th" is a
larger `topK`. Measure before turning it:

```powershell
$env:KB_RETRIEVAL_PROBE_EVAL_OUT="eval_out.json"
go test ./internal/kb/ -tags retrievalprobe -run TestRetrievalProbe -v
```

The probe reports, per question, the rank at which a retrieved section actually contains
the answer term — distinct from the older file-level metric, which only says the expected
*document* ranked and once led to a wrong "the model is refusing" diagnosis.

### When a reranker becomes worth adding

`topK` trades context for recall, and both sides have a cost. Measured on 246 questions
over the WPF POS corpus (266 sections):

| topK | outcome |
|------|---------|
| 3 | answering section in the top 3 for only 3 of 22 refusals; A1 5/9, A2 145/173 |
| 8 | covers 20 of 22; A1 9/9, A2 166–169/173 — but ~2.7x the context per query |
| 10 | covers 22 of 22; diminishing returns, more dilution |

Raising it is not free: with `topK=8` one question began refusing *because* of the extra
context — the model saw fragments from several screens and concluded the list was
incomplete. That was fixed in the data (the module overview now enumerates its screens in
one self-contained sentence), not by tuning `topK`.

A reranker resolves that trade-off: recall wide (20), rerank by query-passage relevance,
send only the best 3. Consider it when any of these hold:

- **Context cost or latency starts to bite.** `topK=8` is roughly 2.7x the tokens of 3, per
  request, forever.
- **`topK` has to keep growing.** More modules and screens mean more sections competing;
  if the section-level probe starts needing 12–15, that is a ranking problem, not a
  truncation one.
- **Dilution regressions appear.** More than an isolated case of "the model refuses when
  given more context" means the ranking, not the budget, is what needs fixing.

It is not the answer when failures are a question/data-model mismatch. One current A3
failure retrieves the correct document and quotes its buttons, then refuses because the
question asks for a *per-module* control list while canonical `ui_inventory` documents are
deduplicated across modules by design. No amount of reranking changes that.

Cost of adding one: another model call per query (latency, spend, one more key to manage),
and `minThreshold` / `cosineMin` need re-measuring against the new ordering. The
section-level probe above is the instrument to judge whether it paid off — have it in place
before, not after.

## Import a team bundle

```powershell
make import TEAM=Store.POS FROM=C:\Protech\wpf-replay\kb
Invoke-RestMethod -Method Post -Uri http://localhost:12598/index
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
make run      # run ./cmd/kb (HTTP API + /mcp endpoint)
make mcp      # run the stdio MCP server (dev / Inspector only)
make build    # build bin/kb and bin/kbmcp
make import   # import a team bundle
make test     # go test -race -short -count=1 ./...
make lint     # golangci-lint run ./...
make verify   # lint + test
```
