# Design Discussion — Knowledge Base Q&A Bot

## Current State

The repo is an **unstarted hexagonal template** (research Q1–Q7). What exists:

- Three demo feature slices — `order`, `user`, `payment` — under `internal/`, each a single
  package with the 11-file layout (`domain.go`, `service.go`, `ports.go`, `handler_http.go`,
  `routes.go`, `repo_postgres.go`, `errors.go`, `dto_*.go`, `*_test.go`).
- Composition root `internal/bootstrap/wire.go:24` — the only place importing >1 feature; builds
  repo→adapters→`NewService`→`NewHandler`→`RegisterRoutes` per feature under `/api/v1`.
- Thin entrypoint `cmd/api/main.go` booting full infra (Postgres, Redis, auth, OTel).
- Platform utils: `internal/platform/config` (viper, research Q3), `httperr`, `logger`, `postgres`.
- `internal/shared` value-object kernel (`Money`, typed IDs, `Pagination`) — research Q7.

No KB feature, no `cmd/kb`, no OpenAI dependency, no `docs/` or `.kb/` artifacts exist yet.

## Desired End State

A standalone `internal/kb/` feature + `cmd/kb` entrypoint that:

1. Indexes `docs/*.md` into inspectable `.kb/index.json` (BM25 corpus) and
   `.kb/faiss_index/metadata.json` (embeddings), reloaded on startup.
2. Answers questions **grounded only** in retrieved heading-sections, citing `filename#anchor`.
3. Deterministically routes each query: strong BM25 → markdown; weak BM25 + vectors present →
   vector RAG; below `minThreshold` → cannot-confirm (no LLM call).
4. Supports multi-turn: a `SessionStore` port shapes *retrieval* via deterministic query
   composition; retrieved sources still control the answer.

**Verification** (spec §9): `make lint && make test` pass; the three sample-doc queries cite the
expected sections, "restaurants" → cannot-confirm `sources:[]`, a follow-up resolves against the
prior topic, and the router reports `strategy:"markdown"` vs `"vector"` observably.

## Patterns to Follow

- **Feature layout & request flow** — mirror `order`: `routes.go → handler_http.go → service.go →
  ports.go → adapters` (research Q1, `order/routes.go:10`, `order/service.go:16`).
- **Dual inbound interface** — handler defines a *local* `service` interface for mocking;
  `bootstrap` injects the concrete `*Service` (research cross-cutting, `order/handler_http.go:15`).
- **Capability-named outbound ports** — `Embedder`, `LLM`, `SectionStore`, `VectorStore`,
  `SessionStore`; provider-agnostic, satisfied structurally (research Q6, `order/ports.go:31`).
- **Domain constructors validating invariants** — `NewSection`/`NewAnswer`, all-private fields,
  no `context`/IO imports; pure BM25 + cosine live here (research Q7, `order/domain.go:31`).
- **Hand-written fakes in tests**, table-driven, `snake_case` cases, `errors.Is` for sentinels
  (research Q4, `order/service_test.go:18`). No gomock despite stale directives.
- **Per-handler `context.WithTimeout`** wrapping external calls; repos/services propagate ctx
  (research Q6, R3.2).
- **Error mapping via `errors.Is` switch in handler `writeError`**, raw `c.JSON(gin.H{"error"})`
  (decision below; `order/handler_http.go:184`).

**Do NOT follow / avoid:**
- `httperr` helpers — present but unused by every handler; staying consistent with shipped code
  (research Q5). Don't introduce them just for kb.
- The `//go:generate mockgen` directives — stale, no `mocks/` dir; don't add generated mocks.
- `cmd/api`'s full-infra boot — `cmd/kb` must NOT require Postgres/Redis/JWT.

## Design Decisions

1. **Error envelope: raw `gin.H`** — match existing handlers exactly (CLAUDE.md R11 conformance).
   `httperr` stays unused. Maps `ErrNotIndexed`→200 body, empty query→400, others→500.
2. **OpenAI access: official `openai-go` SDK** — behind the `Embedder` and `LLM` ports. Accepts a
   new third-party dependency in exchange for typed clients + built-in retries. Confined to
   `adapter_openai.go`; ports keep the rest of the package SDK-agnostic.
3. **Both retrieval strategies ship v1** — BM25 router + brute-force cosine vector RAG + OpenAI
   embeddings, per spec §3. Required by acceptance criteria 2 & 4 (observable `strategy:"vector"`).
   Brute-force cosine over JSON is adequate well past the sample corpus (spec §5).
4. **Config: reuse `platform/config` (viper)** — extend the nested `Config` with an OpenAI field +
   `BindEnv("openai.api_key"←OPENAI_API_KEY)`. **But** `cmd/kb` uses a load path that skips
   `validate()`'s `POSTGRES_DSN`/`JWT_SECRET` gate (research Q3, `config.go:130`), since the bot
   has no DB/JWT. See Open Risks — this reconciles the user's "reuse" choice with spec §7's
   "boot with only `OPENAI_API_KEY`."
5. **Retrieval unit = heading-section** — single Markdown parse on `^#{1,6} `; citation granularity
   is section-level `filename#anchor` (GitHub slug). No sub-chunking (spec §3).
6. **Deterministic router, no LLM** — top BM25 score is the specificity signal; thresholds are
   package constants, the tuning knob (CLAUDE.md R5, spec §3).
7. **In-process `SessionStore`** — map `session_id→[]turn`, last N=5, idle TTL 30min. Memory
   contextualizes retrieval by deterministic string composition; never relaxes grounding (spec §6).
   `ponytail:` in-memory; swap for Redis/DB behind the port when multi-instance.

## What We're NOT Doing

- No streaming, browser UI, multi-format import, CLI/MCP, wiki generation (spec §2 stretch).
- No persistent chat history — the diagram's `chats` table is in-memory only this version (spec §2).
- No hallucination retry loop (`retry_times`) — grounding + cannot-confirm gate suffice (spec §2).
- No auth, multi-tenancy, Postgres/Redis for KB content — read-only over filesystem + LLM call.
- No sub-chunking of large sections, no real FAISS (Go has none; JSON + cosine).
- No edits to `cmd/api` or the demo slices; kb is purely additive.
- No `httperr` migration, no generated mocks.

## Open Risks

- **Config reuse vs `validate()` gate.** Reusing `platform/config` (decision 4) conflicts with
  `validate()` hard-requiring `POSTGRES_DSN`+`JWT_SECRET`. Need a kb-specific load that bypasses
  that — either a `LoadKB()` variant or a flag. Cleanest resolution to confirm during structure.
  If this proves awkward, the spec's original "read env directly" (§7) is the fallback.
- **Threshold calibration** (`strongThreshold`/`minThreshold`) is empirical — must be tuned so the
  three sample docs answer in-scope queries and "restaurants" falls through. May need iteration
  against real BM25 scores; acceptance criteria 2 & 4 depend on it.
- **Vector strategy observability** — criterion 4 needs a vague in-scope paraphrase to actually
  score below `strongThreshold` yet retrieve the right section. Coupled to threshold tuning.
- **OpenAI cost/latency at index time** — every section is embedded on `POST /index`; fine at
  corpus size but the only place cost scales with doc count.
- **`go.mod` dependency addition** — `openai-go` is the first new third-party dep; confirm it
  doesn't trip depguard rules in `.golangci.yml`.
