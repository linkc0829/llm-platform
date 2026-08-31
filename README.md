# Knowledge Base Q&A Bot

A local Go service that indexes Markdown files from `docs/`, retrieves relevant sections with hybrid BM25 + vector search, and answers questions with cited, grounded sources — over HTTP (`/chat`) or MCP (`search_kb`) for coding agents.

**A fresh clone ships no corpus.** `docs/` and `eval/` are gitignored: they hold `make import` output, which is reproducible from a team's exporter bundle and runs to several megabytes of screenshots per team. Import at least one bundle before the service can answer anything — without it `POST /index` reports zero sections and every question is refused.

## Quick Start

```powershell
cp .env.example .env
# Set OPENAI_API_KEY, or use fake mode for quota-free local checks:
$env:KB_LLM_MODE="fake"
# This quick-start bypass is local-only and forces the server to 127.0.0.1:
$env:KB_AUTH_DISABLED="true"

# Required: a fresh clone has an empty docs/. See "Import a team bundle".
# FROM must be a MERGED staging tree holding every source for that team.
make import TEAM=Store.POS FROM=<staging>/kb

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
- `POST /index` - requires a bearer token with the `Indexer` capability; parses and audits all Markdown below `docs/`, rejects unknown or drifting section access metadata before saving or embedding, then loads the in-memory index.
- `POST /chat` - requires any valid bearer token and answers a question from indexed sections only, returning `answer`, `grounded`, `sources`, `images`, `strategy`, and `session_id`. `grounded` is `false` when retrieval fell below threshold or the model declined to answer despite context — branch on it instead of parsing the answer text. Transient upstream failures are reported as such: `429` when the model provider rate-limits (echoing its `Retry-After` if it sent one), `503` when the provider is unavailable or times out. Retry those; a `500` means the request itself failed and retrying will not help.
- `/mcp` - Streamable HTTP MCP endpoint exposing the read-only `search_kb` tool; it requires a bearer token.
- `GET /admin/tokens` - requires an admin bearer token; lists principal IDs, names, capabilities, and creation times without token hashes.
- `POST /admin/tokens` - requires an admin bearer token; creates a non-admin token and returns its plaintext exactly once. A request `admin` field is ignored.
- `DELETE /admin/tokens/:id` - requires an admin bearer token; revokes a non-admin principal by immutable ID. Admin principals can only be revoked by the local `kbtoken` CLI.
- `cmd/kbmcp` stdio - local process transport with no HTTP trust boundary; it intentionally runs with full access for the user who can execute the process.

## MCP for coding agents

One read-only tool, `search_kb`, with a required `query` and optional `session_id`. It returns the answer, an explicit `grounded` boolean, session ID, citations, image paths, and retrieval strategy. `grounded` lets an agent decide programmatically whether the KB answered, without parsing refusal prose — it is `false` when retrieval fell short or the model declined to answer despite context.

The same tool is served over two transports, backed by the same service and index:

| Transport | Binary | When |
| --- | --- | --- |
| Streamable HTTP, `/mcp` | `cmd/kb` (`make run`) | **Default.** One process, one index in memory, one query log |
| stdio | `cmd/kbmcp` (`make mcp`) | Development, MCP Inspector, or a client that cannot speak Streamable HTTP |

Prefer HTTP. Each stdio client spawns its own process with its own copy of the index, and its queries land in a separate log file, which makes the metrics in `log/kb.log` incomplete.

`cmd/kb` protects `/chat` and `/mcp` with the bearer token from `KB_AUTH_FILE`; `cmd/kbmcp` deliberately has no token header because the process owner already has local access to the corpus.

When authentication is enabled, `cmd/kbtoken` is the local bootstrap and admin-recovery tool. Stop the service first; the CLI also checks `/health`, while the shared `auth.json.lock` is the actual cross-process write lock:

```powershell
$env:KB_AUTH_FILE="auth.json"
go run ./cmd/kbtoken create-admin -name platform-admin
# Save the token from this output once, then start cmd/kb.
go run ./cmd/kbtoken list
go run ./cmd/kbtoken rotate-admin -id <old-admin-id>
go run ./cmd/kbtoken revoke-admin -id <old-admin-id>
```

`rotate-admin` creates a new principal ID and leaves the old admin alive until it is explicitly revoked. Revoking the last admin requires `-force`; if that is intentional, `create-admin` can bootstrap a new admin from the resulting empty auth file. A stale lock is never removed automatically—stop any writer and remove `auth.json.lock` manually only after confirming no process owns it.


Import a bundle and build the index before starting either one:

```powershell
make import TEAM=Store.POS FROM=<staging>/kb
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
      "headers": {
        "Authorization": "Bearer {env:KB_QA_TOKEN}"
      },
      "oauth": false,
      "enabled": true,
      "timeout": 60000
    }
  }
}
```

Allow TCP port 12598 only from approved company network ranges in Windows Firewall. `/health` is public; `/chat`, `/index`, and `/mcp` require a bearer token when auth is enabled.

This example uses plain HTTP for a trusted-network test. Bearer tokens are credentials: before rollout, put the endpoint behind HTTPS or use a confirmed encrypted trusted channel.

Missing or invalid bearer tokens intentionally return 401 without WWW-Authenticate. This service does not implement OAuth discovery or flow; that is a deliberate deviation from RFC 9110, so do not add resource_metadata to the MCP auth wrapper.

### Local stdio server (development)

`make mcp` runs `cmd/kbmcp`, the stdio transport of the same server. Use it when driving MCP Inspector (below), or for a client that only accepts a `command`. Point the client at this repository:

```json
{
  "mcpServers": {
    "knowledge-base": {
      "command": "go",
      "args": ["run", "./cmd/kbmcp"],
      "cwd": "C:\\path\\to\\llm-platform"
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
- `KB_AUTH_FILE` - required auth snapshot for `cmd/kb` unless auth is explicitly disabled.
- `KB_AUTH_DISABLED=true` - local-only escape hatch; the server forces `127.0.0.1` and leaves HTTP/MCP routes unguarded.
- `/admin/tokens` is registered only when authentication is enabled. The network API can grant `indexer`, but it can never create or delete an admin principal.
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

Every `/index` run audits section visibility before `Save` or embedding. `procedure`
sections whose heading contains 工程對應 are engineering-only; `ui_inventory`,
`playlist`, `reference` and `distilled` sections are public; other or missing
`doc_type` values fail the index. Endpoint markers under a non-engineering heading also fail closed. The
rule is intentionally asymmetric: an engineering `ViewModels` or `Commands`
section may contain no endpoint and still indexes successfully.

The 2026-08-28 corpus baseline is 3,068 sections with anchor version 2 and
fingerprint
`43de814bfe3044e7194a0beb95f7a7dbb6de88c8530a611953a51f260ac57c58`
(the 2026-08-14 baseline was 679 sections — the corpus has since absorbed three
more sources and 47 distilled action chains).
That fingerprint is a precondition, not a permanent claim: if the current
documents produce a different fingerprint, rerun the classification dry run
before comparing counts.

`corpus_baseline_test.go` pins three things that must move together — the
fingerprint, the section count, and the per-`doc_type` distribution. Changing the
corpus without updating all three leaves the test asserting a corpus that no
longer exists.

If an audit fails, the new content is not saved, embedded, or swapped into
memory. The previous last-known-good snapshot continues serving; an existing
section from that snapshot can still be retrieved until a corrected `/index`
succeeds. An index failure is therefore not an immediate revocation mechanism.
Startup applies the same audit to a persisted snapshot; if it fails, the
snapshot is rejected and the service remains unready until `/index` rebuilds it.

Two-way recall, fused, split into two pools, then truncated. There is no reranking stage.

```text
query ─┬─ BM25          → top candidateK (20)
       └─ vector cosine → top candidateK (20)
                ↓
           FuseRRF (rrfK 60)
                ↓
   PartitionAndRankPools  ── L0 pool (source sections) ─┐
   (split BEFORE truncation)  L1 pool (distilled, l1K 2) ┤
                ↓                                        │
   ExpandDistilled: each L1 hit expands deterministically │
   back to the L0 step sections it indexes ───────────────┘
                ↓
       topSections(..., topK 8)  → sent to the model
```

**L1 is an index, never evidence.** A `distilled` section can be retrieved but never
reaches the model: on a hit it expands to the L0 step sections it points at, and every
citation and screenshot comes from L0. That is deliberate — `distilled` is not in the
grounding prompt's evidence allow-list, and folding a `recorded_unlabeled` step's
narrative into a new `doc_type` would launder past the guard that such a step proves
only that a click occurred.

**The split happens before candidate truncation, not after.** BM25 penalises length
(`denom = f + k1*(1 - b + b*DocLen/AvgLen)`), and a distilled chain runs several times
longer than one step section — roughly a 4.6x disadvantage in a shared pool. Partitioning
after truncation would mean the L1 entries were already gone. When no L1 candidate
qualifies, its reserved slots return to L0, so questions that are not about action
sequences behave exactly as they did before distillation existed.

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

`topK` trades context for recall, and both sides have a cost. Measured 2026-08-14 on 246
questions over the then 266-section WPF POS corpus — kept because the shape of the
trade-off still holds, but the corpus is now 3,068 sections, so re-measure before acting
on the absolute numbers:

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

It is not the answer when failures are a question/data-model mismatch. The example that
prompted this note was an A3 failure that retrieved the correct document, quoted its
buttons, then refused because the question asked for a *per-module* control list while
canonical `ui_inventory` documents are deduplicated across modules by design. No amount of
reranking changes that. (That particular case no longer reproduces — A3 is 159/159 as of
2026-08-28 — but the reasoning is why a reranker is not a general answer.)

Cost of adding one: another model call per query (latency, spend, one more key to manage),
and `minThreshold` / `cosineMin` need re-measuring against the new ordering. The
section-level probe above is the instrument to judge whether it paid off — have it in place
before, not after.

## Import a team bundle

```powershell
make distill TEAM=Store.POS BUNDLE=<staging>/kb   # L1 action chains, before -check
make import  TEAM=Store.POS FROM=<staging>/kb
Invoke-RestMethod -Method Post -Uri http://localhost:12598/index
```

> **`FROM` must be a merged tree containing every source for that team.** `kbimport`
> replaces `docs/<team>/` and `eval/<team>/` wholesale, so pointing it at a single
> source deletes the others — and `-check` still passes, because it validates that the
> tree is *self-consistent*, not that it is *complete*. This has already cost one corpus:
> a 176-document team was cut to 22, taking its eval suite with it. Neither directory is
> in version control, so there is nothing to restore from.
>
> Which sources make up a team is recorded in `kb_sources.json` (gitignored — it holds
> internal absolute paths; copy `kb_sources.json.example` and fill it in). Merging,
> distilling, checking and importing are driven end to end by the `ui-kb-merge-import`
> skill; do not run `make import` from anywhere else.

`make distill` runs before `kbimport -check`, not after: `validateEvalIndex` requires
`kb_index.json` to cover exactly the staged file set, so distilled output arriving later
fails the check. It uses no LLM — it concatenates each module's Gherkin steps in order,
preserving every source anchor and evidence class.

Imported files live below `docs/<team>/`; screenshots are copied below that team's `_assets/` directory. `images` in `/chat` are paths relative to `KB_DOCS_DIR`, so they only resolve on a machine that has run the import — they are identifiers, not URLs a client can fetch.

The import is transactional: it validates the staged bundle against every team already present and only then swaps the team's directory into place, so a rejected bundle leaves `docs/` untouched. Re-running it for the same team replaces that team's tree and nothing else.

All imported documents currently share one access level and one index. If a team needs a separate trust boundary, run a separate instance with its own `KB_DOCS_DIR` and `KB_INDEX_DIR`.

## Layout

```text
cmd/kb/                  # HTTP server entrypoint (/health, /index, /chat)
cmd/kbimport/            # transactional team-bundle importer (make import)
cmd/kbdistill/           # L1 action-chain distiller, zero LLM (make distill)
cmd/kbtoken/             # local auth bootstrap, admin rotation, and revoke CLI
cmd/kbmcp/               # stdio MCP server exposing the search_kb tool
internal/kb/             # domain, service, ports, handlers, markdown/vector repos, LLM adapters
internal/auth/           # token resolution, CRUD, atomic auth persistence, HTTP adapter
internal/mcpserver/      # MCP adapter over the kb service (parallel to the HTTP handler)
internal/bootstrap/      # composition root — wires the kb service for both entrypoints
internal/platform/config # env/.env config loading
internal/platform/httpserver
internal/platform/atomicfile
internal/platform/lockfile
internal/platform/logger
kb_sources.json.example  # template for the (gitignored) per-team source manifest
thoughts/qrspi/          # QRSPI artifacts
```

Not in version control — recreate with `make import` and `POST /index`:

```text
docs/<team>/             # imported Markdown and _assets/ screenshots
eval/<team>/             # bundle eval YAML and kb_index.json
.kb/index.json
.kb/faiss_index/vectors.bin
```

Also gitignored, because they derive from a customer corpus rather than from this
repository — keep local copies, they are not recoverable from a clone:

```text
testdata/                # requirement and retrieval-probe suites (business rules verbatim)
internal/kb/testdata/    # corpus fixtures
metrics/                 # eval runs — these store model answers and cited passages
postman/                 # acceptance queries
kb_sources.json          # which sources compose each team; internal absolute paths
```

## Make Targets

```powershell
make run            # run ./cmd/kb; needs $env:KB_AUTH_FILE or KB_AUTH_DISABLED=true
make mcp            # run the stdio MCP server (dev / Inspector only)
make build          # build bin/kb.exe, bin/kbmcp.exe, and bin/kbtoken.exe
make import         # import a team bundle (FROM must be a merged tree — see above)
make distill        # generate L1 action chains into a staging bundle, before -check
make test           # go test -race -short -count=1 ./...
make test-cover     # same, plus coverage.out and coverage.html
make lint           # golangci-lint run ./...
make fmt            # gofmt -s -w .
make vet            # go vet ./...
make verify         # lint + test
make hooks-install  # enable .githooks/pre-commit (gofmt, lint, unit tests)
make clean          # remove bin/ and coverage output (PowerShell/cmd only)

go run ./cmd/kbtoken list   # list auth metadata while cmd/kb is stopped
```

`make build` writes the `.exe` suffix explicitly, because `go build -o` uses the
name verbatim rather than adding it. The Inspector command above and
`run_eng_eval.py` both invoke `bin/kbmcp.exe`, so the suffix is not optional.

Run `make hooks-install` once per clone. The hook runs `gofmt -l`, `golangci-lint`
and the unit tests before each commit; `git commit --no-verify` skips it.
