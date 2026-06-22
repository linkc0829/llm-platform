# Knowledge Base Q&A Bot — Specification

Status: draft · Owner: ken2_lin · Last updated: 2026-06-22

## 1. Overview

A service that answers natural-language questions **strictly grounded** in a small
Markdown knowledge base (`docs/*.md`). It builds an inspectable index, retrieves the
most relevant heading-sections per query, and asks an LLM to answer using only that
context. Every answer cites its sources as `filename#heading`. When retrieval is weak,
the service says it cannot confirm the answer rather than guessing. Follow-up questions
in a session are interpreted using recent conversation context.

Implemented in **Go**, as a single feature package `internal/kb/` following this repo's
hexagonal conventions (R1–R5 in `CLAUDE.md`), with a standalone `cmd/kb` entrypoint.

## 2. Goals / Non-goals

**Goals**
- Index `docs/*.md` into an inspectable on-disk index.
- Answer questions grounded only in indexed content, with `filename#heading` citations.
- Refuse (cannot-confirm) when no sufficiently relevant content exists — no hallucination.
- Route each query between two retrieval strategies automatically and deterministically.
- Support **multi-turn dialogue**: a follow-up question is interpreted in the context of recent
  turns in the same session (diagram FR3). Memory shapes *retrieval*; retrieved sources still
  control the final answer (no hallucination backdoor).

**Non-goals (this version)**
- Streaming (`/chat/stream`), browser UI, multi-format import, CLI/MCP, wiki generation. Stretch
  goals deferred until the core loop is verified.
- **Persistent chat history.** The diagram's `chats` store (`session_id, created_at, message,
  status, retry_times`) is modeled as a DB table; this version keeps session memory in-process
  only (see §6). Upgrade path: swap the in-memory store for a persistent one behind the same
  port — no behavior change to callers.
- **Hallucination retry loop** (`retry_times` in the diagram). Grounding + the cannot-confirm
  gate already prevent ungrounded answers; an answer→verify→retry loop is extra LLM cost for
  marginal benefit at this corpus size. Deferred; revisit if grounding proves leaky.
- Authentication, multi-tenancy, Postgres/Redis for KB content. The bot is read-only over the
  filesystem plus an outbound LLM call.

## 3. Retrieval design

**Retrieval unit: heading-section.** A single Markdown parse splits each file on
`^#{1,6} ` headings; each section is `{file, heading, anchor, body}` where `anchor` is the
GitHub-style slug of the heading. Citations are therefore section-level (`filename#anchor`),
which is exactly the required citation granularity. (Sub-chunking large sections is a future
optimization, not in scope.)

**Two strategies, one deterministic router.** The BM25 keyword score is itself the signal
for "is the question specific or ambiguous":

| Condition | Strategy | Rationale |
|-----------|----------|-----------|
| top BM25 score ≥ `strongThreshold` | **Markdown KB** (keyword) | sharp keyword match → specific question |
| top BM25 score < `strongThreshold`, vector index present | **Vector RAG** (semantic) | weak keyword match → ambiguous question, try embeddings |
| best score (chosen path) < `minThreshold` | **cannot-confirm** | nothing relevant enough; no LLM call, no citation |

The router uses **no LLM** (CLAUDE.md Rule 5: deterministic routing is code's job). Thresholds
are package constants and are the tuning knob; they are calibrated so the three sample docs
answer their in-scope questions and the out-of-scope "restaurants" query falls through to
cannot-confirm.

**Grounding prompt.** When a strategy yields top-K sections above `minThreshold`, the LLM is
called with a system instruction equivalent to: *"Answer ONLY using the context below. If the
answer is not in the context, say you cannot confirm it from the knowledge base. Cite sources
as filename#heading."* followed by the K sections (and recent conversation turns, see §6). The
response text plus the K citations form the answer.

## 4. HTTP API

Base: the bot's own entrypoint (`cmd/kb`), default port `8080` (template's `APP_PORT`; the
PROMPT.md examples use `8000` — port is illustrative). Routes are public (no auth). Note the
diagram names this `POST /chats`; we use the PROMPT.md's `POST /chat` since the assignment
verification is authoritative.

### `GET /health`
→ `200 {"status":"ok"}`

### `POST /index`
Builds the index from `docs/*.md`, persists both `.kb/index.json` and
`.kb/faiss_index/metadata.json`, and loads it into memory.
→ `200 {"files_indexed": <int>, "sections_indexed": <int>}`

### `POST /chat`
Request:
```json
{ "query": "<string, required, non-empty>", "session_id": "<optional string>" }
```
- Empty/missing `query` → `400 {"error":"query is required"}`
- `session_id` omitted → treated as a fresh single-turn conversation (a new id is generated and
  returned, so the caller can continue the thread).
- Not yet indexed (no `.kb/` and no `/index` since boot) → **`200`** with a body indicating the
  knowledge base has not been indexed yet (status 200 per PROMPT.md, not an error code).
- Otherwise → `200`:
  ```json
  {
    "session_id": "<echoed or newly generated>",
    "answer": "<grounded answer text>",
    "sources": ["refund_policy.md#refund-timeline"],
    "strategy": "markdown" | "vector"
  }
  ```
- Cannot-confirm → `200` with an answer stating it cannot confirm from the knowledge base and
  `"sources": []`.

All handlers wrap external calls in `context.WithTimeout` (R3.2).

## 5. Persistence

The bot is stateless across restarts except for these inspectable artifacts under `.kb/`:

- **`.kb/index.json`** — every section (`file`, `heading`, `anchor`, `body`) plus BM25
  corpus statistics (term frequencies, doc lengths, avg length). Human-inspectable.
- **`.kb/faiss_index/metadata.json`** — embedding model name and `id → vector` map for each
  section. (Go has no FAISS; vectors are stored as JSON and searched with brute-force cosine,
  which is adequate well past the sample corpus. The path/filename are kept for inspectability
  and parity with the PROMPT.md verification.)

On startup the service attempts to load both from `.kb/`. If absent, it logs a warning and
serves `/chat` as "not indexed yet" until `POST /index` runs. Conversation memory (§6) is
**not** persisted.

## 6. Conversation memory (multi-turn)

Behind a `SessionStore` port. Default implementation: in-process map `session_id → []turn`,
keeping the last **N turns** (default 5) per session; entries expire after an idle TTL (default
30 min) to bound memory. `ponytail:` in-memory + map; swap for Redis/DB when multi-instance.

How memory is used per request:
1. Load recent turns for `session_id` (empty for a new session).
2. **Retrieval contextualization:** the search query is built from the current query plus the
   previous user turn(s) in the session, so follow-ups like "what about gift cards?" resolve
   against the prior topic. Deterministic string composition — no extra LLM call.
3. **Generation:** recent turns are included in the LLM prompt as prior dialogue, but the system
   instruction still forbids answering from anything outside the retrieved sections. **Sources
   control the answer; memory only helps interpret the question** (PROMPT.md Conversation Memory
   rule).
4. After answering, append `{query, answer}` to the session.

Cannot-confirm and citation rules are identical to single-turn; memory never relaxes grounding.

## 7. Configuration

| Env | Purpose | Required |
|-----|---------|----------|
| `OPENAI_API_KEY` | OpenAI auth for embeddings + answer generation | yes (for `/index` and grounded `/chat`) |
| `APP_PORT` | HTTP port (default 8080) | no |

Models: `text-embedding-3-small` (embeddings), `gpt-4o-mini` (answers). `OPENAI_API_KEY` is
added to `.env.example`; the `.env` file is not created automatically (R0.2).

**Entrypoint (decided).** A dedicated thin entrypoint `cmd/kb/main.go` wires only the gin
engine + KB feature and boots with just `OPENAI_API_KEY` — no Postgres, Redis, or JWT. The
template's `cmd/api` (which boots the full infra and the user/order/payment slices) is left
untouched. Config loading for KB reads `OPENAI_API_KEY` and `APP_PORT` directly and does not
go through `config.validate()`'s DB/JWT requirements.

## 8. Error handling

- `ErrNotIndexed` (sentinel) → mapped to the 200 "not indexed yet" body, not an HTTP error.
- Empty query → 400.
- OpenAI / filesystem failures → wrapped with context (R3.3), surfaced as `500 {"error":...}`.
- Cannot-confirm is a normal 200 result, not an error.

## 9. Acceptance criteria

1. `make lint && make test` pass; KB service+handler unit tests cover routing (strong→markdown,
   weak→vector, both-weak→cannot-confirm), citation formatting, empty query, not-indexed, and
   multi-turn follow-up (a context-dependent second query retrieves the right section).
2. With `OPENAI_API_KEY` set and the server running:
   - `GET /health` → `200 {"status":"ok"}`.
   - `POST /chat` before indexing → `200`, indicates not indexed yet.
   - `POST /index` → `200 {"files_indexed":3,"sections_indexed":N}`; `.kb/index.json` and
     `.kb/faiss_index/metadata.json` are present and readable.
   - Restart without re-indexing → `.kb/` loads on startup; `/chat` still answers.
   - "How long do refunds take?" → cites `refund_policy.md#refund-timeline`.
   - "Can I change my email address?" → cites `account_help.md#change-email-address`.
   - "Which restaurants are nearby?" → cannot-confirm, `sources: []`.
3. Multi-turn: ask "How long do refunds take?", then in the same `session_id` ask a follow-up
   like "And which items can't be refunded?" — the second answer resolves against the refund
   topic and cites `refund_policy.md#non-refundable-items`.
4. Router observably splits: a sharp keyword query reports `"strategy":"markdown"`; a vague
   paraphrase of an in-scope topic reports `"strategy":"vector"`.

## 10. Implementation map (informative)

Single package `internal/kb/`, one file per responsibility per `CLAUDE.md` R2:
`domain.go` (Section, Citation, Answer, pure BM25 + cosine), `ports.go` (`SectionStore`,
`VectorStore`, `Embedder`, `LLM`, `SessionStore`), `service.go` (index + chat + routing +
memory), `errors.go`, `dto_http.go`, `dto_internal.go` (on-disk JSON), `handler_http.go`,
`routes.go`, `repo_markdown.go`, `repo_vector.go`, `memory_inproc.go` (SessionStore),
`adapter_openai.go`, and `*_test.go`. Tests use hand-written fakes, matching the existing
`internal/order/service_test.go` style.
```
HTTP → handler_http.go → service.go → ports.go → {repo_markdown, repo_vector, adapter_openai, memory_inproc}
```
```
docs/*.md → sections → {BM25 index → .kb/index.json, embeddings → .kb/faiss_index/metadata.json}
```
