# Structure Outline — Knowledge Base Q&A Bot

## Approach

One additive feature package `internal/kb/` + thin `cmd/kb` entrypoint, mirroring the
`order` slice's hexagonal layout. Build it as five vertical slices, each crossing
domain→service→ports→adapter→handler: boot a server, then index, then ground-and-answer
(markdown), then add vector RAG, then add multi-turn memory. Each slice is independently
runnable and testable. No edits to `cmd/api` or demo slices.

---

## Phase 1: Skeleton + `/health` + config path

Server boots via `cmd/kb` with only `OPENAI_API_KEY`/`APP_PORT`, gin engine + KB handler
wired, `/health` answering. Establishes the package and the no-infra boot path.

**Files**: `cmd/kb/main.go`, `internal/kb/{routes.go,handler_http.go,service.go,ports.go,errors.go,dto_http.go}`, `internal/platform/config/config.go`, `.env.example`, `go.mod`
**Key changes**:
- `config.LoadKB() (Config, error)` — viper load that skips `validate()`'s `POSTGRES_DSN`/`JWT_SECRET` gate; binds `openai.api_key`←`OPENAI_API_KEY`, `http.port`←`APP_PORT` (resolves design Open Risk #1)
- `kb.NewService(...) *Service` / `kb.NewHandler(svc *Service) *Handler` — concrete in, local `service` interface field
- `kb.RegisterRoutes(rg *gin.RouterGroup, h *Handler)` — `/health`, stubs for `/index`,`/chat`
- `Handler.health(c *gin.Context)` → `200 {"status":"ok"}`

**Verify**: `make lint && make test` pass; `go run ./cmd/kb` boots with only `OPENAI_API_KEY` set (no Postgres); `curl /health` → `200 {"status":"ok"}`.

---

## Phase 2: Indexing → BM25 → `.kb/index.json`

`POST /index` parses `docs/*.md` into heading-sections, builds the BM25 corpus, persists
`.kb/index.json`, loads into memory; reloaded on startup.

**Files**: `internal/kb/{domain.go,repo_markdown.go,dto_internal.go,service.go,handler_http.go,ports.go}`, `docs/*.md` (3 sample docs), `cmd/kb/main.go`
**Key changes**:
- `NewSection(file, heading, body string) (Section, error)` — computes GitHub-slug `anchor`; private fields
- pure `domain.go`: `tokenize(string) []string`, `BM25Score(...)`, corpus stats types
- `SectionStore` port: `Load(ctx) ([]Section, error)`, `Save(ctx, []Section) error`
- `repo_markdown.go`: parse on `^#{1,6} `, read/write `.kb/index.json` via `dto_internal.go`
- `Service.Index(ctx) (filesIndexed, sectionsIndexed int, err error)`; `Service.loadOnStartup`
- `Handler.index` → `200 {"files_indexed":int,"sections_indexed":int}`

**Verify**: `make test` (parser + slug + BM25 unit tests); `POST /index` → `200 {"files_indexed":3,...}`; `.kb/index.json` exists and is human-readable; restart loads it (log shows loaded, not warning).

---

## Phase 3: Single-turn `/chat` — markdown strategy + grounding + cannot-confirm

`POST /chat` runs BM25, and when top score ≥ `strongThreshold` calls the LLM with the
grounding prompt over top-K sections, returning answer + `filename#anchor` citations.
Below `minThreshold` → cannot-confirm, no LLM call. (Vector branch stubbed until Phase 4.)

**Files**: `internal/kb/{domain.go,service.go,ports.go,adapter_openai.go,handler_http.go,dto_http.go,errors.go}`, `go.mod` (add `openai-go`)
**Key changes**:
- `NewAnswer(text string, sources []Citation, strategy string) (Answer, error)`
- `LLM` port: `Answer(ctx, query string, sections []Section, history []Turn) (string, error)`
- `adapter_openai.go`: `OpenAIClient` implementing `LLM` (`gpt-4o-mini`), grounding system prompt
- consts `strongThreshold`, `minThreshold`, `topK`
- `Service.Chat(ctx, query, sessionID string) (Answer, sessionID string, err error)` — markdown + cannot-confirm routing only
- `errors.go`: `ErrNotIndexed`, `ErrEmptyQuery`; `Handler.writeError` switch (`errors.Is`) — `ErrNotIndexed`→200 body, `ErrEmptyQuery`→400, default→500
- `Handler.chat` wraps LLM call in `context.WithTimeout`

**Verify**: `make test` covers strong→markdown, both-weak→cannot-confirm `sources:[]`, empty query→400, not-indexed→200 (fake LLM). Live: "How long do refunds take?" cites `refund_policy.md#refund-timeline`; "Which restaurants are nearby?" → cannot-confirm.

---

## Phase 4: Vector RAG strategy

When top BM25 < `strongThreshold` and a vector index is present, embed the query and
retrieve by brute-force cosine; persist `.kb/faiss_index/metadata.json` at index time.
Makes `strategy:"vector"` observable (acceptance criterion 4).

**Files**: `internal/kb/{domain.go,ports.go,repo_vector.go,adapter_openai.go,service.go,dto_internal.go,handler_http.go}`
**Key changes**:
- `domain.go`: `Cosine(a, b []float32) float64`
- `Embedder` port: `Embed(ctx, texts []string) ([][]float32, error)` (`text-embedding-3-small`)
- `VectorStore` port: `Load(ctx) (map[string][]float32, error)`, `Save(ctx, map[string][]float32) error`
- `repo_vector.go`: read/write `.kb/faiss_index/metadata.json` (model name + id→vector)
- `Service.Index` also embeds + saves vectors; `Service.Chat` adds the weak-BM25→vector branch (still gated by `minThreshold`)

**Verify**: `make test` covers weak→vector routing + cosine ranking (fake Embedder). Live: `POST /index` writes `.kb/faiss_index/metadata.json`; a vague in-scope paraphrase reports `"strategy":"vector"` and still cites the right section.

---

## Phase 5: Multi-turn memory

`SessionStore` holds last N=5 turns per `session_id` (idle TTL 30min). Memory composes
the retrieval query (deterministic string concat) and is passed as prior dialogue to the
LLM — grounding unchanged.

**Files**: `internal/kb/{ports.go,memory_inproc.go,service.go,domain.go,dto_http.go}`
**Key changes**:
- `Turn { Query, Answer string }`
- `SessionStore` port: `Get(ctx, id string) []Turn`, `Append(ctx, id string, t Turn)`
- `memory_inproc.go`: `InProcStore` — map + mutex + TTL sweep. `// ponytail: in-memory, swap for Redis behind port when multi-instance`
- `Service.Chat`: load turns → build contextual query for retrieval → pass history to `LLM.Answer` → append turn; generates session_id when omitted

**Verify**: `make test` — multi-turn follow-up: 2nd context-dependent query retrieves correct section. Live: "How long do refunds take?" then (same session) "And which items can't be refunded?" → cites `refund_policy.md#non-refundable-items`.

---

## Testing Checkpoints

- **After P1**: package compiles, lints; `cmd/kb` boots with no DB; `/health`→200.
- **After P2**: indexing produces `.kb/index.json`; reload-on-startup works; BM25/slug unit-tested.
- **After P3**: grounded single-turn answers + citations + cannot-confirm; `strategy:"markdown"`; error mapping correct. *Independently shippable core loop.*
- **After P4**: `strategy:"vector"` observable; `.kb/faiss_index/metadata.json` present.
- **After P5**: full acceptance criteria 1–4 met, incl. multi-turn.

Note: thresholds (`strongThreshold`/`minThreshold`) are empirical — expect calibration iteration in P3/P4 against real BM25 scores (design Open Risks #2–3).
