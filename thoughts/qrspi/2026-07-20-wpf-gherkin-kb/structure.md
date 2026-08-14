# Structure Outline

## Approach

Four vertical slices, each ending at a working `POST /index` → `POST /chat` round trip, plus a closing acceptance phase. Correctness first (Chinese tokens), then the real corpus, then real embeddings, then fusion — so that each slice's verification runs against something real rather than a stub.

**Deviation from design.md's phase numbering, deliberate:** design Phase 2 (hybrid) is sequenced *last*, after real-KB ingestion and Ollama. Two reasons. (a) Decision 4 keys `vecMap` on `Citation()`, which is only unique once `file` is a relative path — building fusion on colliding keys means debugging silent overwrites. (b) The design's top risk is that a non-Chinese-trained embedder can't rescue zero-BM25 Chinese queries; that assumption must be falsifiable *before* the fusion code exists, not after. Content is unchanged — only the order.

---

## Phase 1: CJK correctness + anchor versioning

Chinese queries produce tokens and score above `minThreshold`, so `/chat` answers instead of denying. Stale-anchor indexes are rejected rather than silently mis-keyed.

**Files**: `internal/kb/domain.go`, `internal/kb/dto_internal.go`, `internal/kb/repo_markdown.go`, `internal/kb/service.go`, `internal/kb/domain_test.go`, `internal/kb/repo_markdown_test.go`, `internal/kb/service_test.go`

**Key changes**:
- `tokenize(text string) []string` — rewritten: per-rune scan; ASCII alphanumeric runs → whole lowercased token; CJK runs → overlapping character bigrams (len-1 run → the single rune); all else separator. Imports stay `strings`/`unicode`/`sort` only.
- `slugify(h string) string` — rewritten: keep CJK + alphanumerics, everything else → `-`, collapse runs, trim edges.
- `indexJSON { AnchorVersion int `json:"anchor_version"`; ... }` — new field.
- `const anchorVersion = 1` — new, in `domain.go`.
- `ErrIndexStale` — new sentinel in `errors.go`, per the `ErrXxx` convention.
- `MarkdownRepo.Load(ctx) ([]Section, error)` → returns `ErrIndexStale` when the persisted `anchor_version` ≠ current.
- `Service.LoadOnStartup` — propagates `ErrIndexStale`; `cmd/kb/main.go` warns "re-index required" rather than fatal, matching its existing `ErrNotIndexed` handling (`main.go:48-54`).

**Verify**: `make verify` passes. New table-driven cases in `TestTokenize`/`TestSlugify` cover CJK, mixed CJK+ASCII, identifier preservation (`LoginViewModel` survives whole), and pure-punctuation input. Manual: delete `.kb/`, `KB_LLM_MODE=fake make run`, `POST /index`, then `POST /chat` with a Chinese question against the sample docs — response is no longer the refusal string. Then restore a hand-edited `index.json` with `anchor_version: 0` and confirm startup warns instead of serving it.

---

## Phase 2: Ingest the real flow bundle

`Parse` walks nested folders, citations are unique across them, and frontmatter + screenshot filenames reach the answer.

**Files**: `internal/kb/repo_markdown.go`, `internal/kb/domain.go`, `internal/kb/dto_internal.go`, `internal/kb/repo_markdown_test.go`, `internal/kb/domain_test.go`

**Key changes**:
- `MarkdownRepo.Parse` — `filepath.Glob` → `filepath.WalkDir`, `.md` only; `file` becomes the slash-normalized path relative to `docsDir` (not `filepath.Base`).
- `Section` gains `meta map[string]string` and `images []string`, with `Meta()` / `Images()` getters matching the existing getter block (`domain.go:28-31`).
- `NewSection(file, heading, body string, meta map[string]string, images []string) (Section, error)` — signature extended; `rehydrateSection` likewise.
- `sectionJSON` gains `Meta map[string]string `json:"meta"`` and `Images []string `json:"images"``.
- `parseFrontmatter(lines) (map[string]string, int)` — new pure helper in `repo_markdown.go`; leading `---` block only, applied to every section in that file.
- Image extraction via `\*\(Image:\s*([^)]+\.jpg)\)\*` on body lines.
- Empty-body sections dropped in `flush()`.

**Verify**: `make verify` passes. New `repo_markdown_test.go` cases using `t.TempDir()` + `writeTestFile` (`repo_markdown_test.go:87`): nested dirs are found; two files with the same basename in different dirs produce different `File()`; frontmatter keys land on every section of that file; image filenames extracted; bodyless section omitted. Manual: copy the 9-folder bundle under `docs/`, `POST /index` — `sections` count is plausible and `POST /chat` returns a `sources` entry containing a folder path.

---

## Phase 3: Config + local Ollama + model stamp

Docs dir, index dir, base URL, and both model names come from config; the embedding-model stamp is read back and compared.

**Files**: `internal/platform/config/config.go`, `internal/kb/ports.go`, `internal/kb/repo_vector.go`, `internal/kb/adapter_openai.go`, `internal/kb/service.go`, `cmd/kb/main.go`, `.env.example`, `internal/platform/config/config_test.go`, `internal/kb/service_test.go`

**Key changes**:
- `VectorStore.Load(ctx context.Context) (string, map[string][]float32, error)` — **breaking port change**, returns the persisted model alongside vectors. `VectorRepo` and `fakeVectorStore` (`service_test.go:74-90`) updated.
- `Service` gains an `embedModel string` field; `const embeddingModel` (`service.go:19`) deleted.
- `LoadOnStartup` — stamp mismatch ⇒ warn, drop vectors, mark BM25-only. Never mixes.
- `NewOpenAIClient(apiKey, baseURL, chatModel, embedModel string) *OpenAIClient` — signature extended; `option.WithBaseURL` applied when `baseURL != ""`; SDK model constants become defaults, not hard-codes.
- `OpenAIConfig` gains `BaseURL`, `ChatModel`, `EmbedModel`; new `KBConfig { DocsDir, IndexDir string }`.
- New binds: `OPENAI_BASE_URL`, `KB_CHAT_MODEL`, `KB_EMBED_MODEL`, `KB_DOCS_DIR`, `KB_INDEX_DIR`. API-key requirement relaxed when a base URL is set; `!= "fake"` → `strings.EqualFold` to match `main.go:37`.
- `main.go:32-33` literals → config values.

**Verify**: `make verify` passes; config tests cover the new binds and the relaxed API-key rule. Manual: `ollama pull` the chosen embed + chat models, point `OPENAI_BASE_URL` at `http://localhost:11434/v1`, `POST /index` — completes without an OpenAI key. Then **the embedder sanity check the design flagged as top risk**: embed two paraphrases of one flow's scenario text plus one unrelated flow's, and confirm `Cosine(paraphraseA, paraphraseB) > Cosine(paraphraseA, unrelated)`. If that fails, stop and revisit the model choice before Phase 4. Finally, re-run `/index` with a different `KB_EMBED_MODEL` and confirm startup warns about the stamp instead of serving mixed vectors.

---

## Phase 4: True hybrid (RRF fusion + per-retriever gate)

BM25 and vector run as peers and are fused by rank; the pre-gate that made Chinese unanswerable is gone.

**Files**: `internal/kb/domain.go`, `internal/kb/service.go`, `internal/kb/domain_test.go`, `internal/kb/service_test.go`

**Key changes**:
- `RankVector(indexed []Section, vecMap map[string][]float32, q []float32, limit int) []ScoredSection` — new pure function, mirrors `RankBM25` (`domain.go:176-188`): sort desc, ties by ascending index.
- `FuseRRF(lists [][]ScoredSection, rrfK int) []ScoredSection` — new pure function; accumulates `1/(K+rank)` keyed by section index; `sort.SliceStable`, ties by ascending index.
- `const candidateK = 20`, `const rrfK = 60`, `const cosineMin = 0.30`; `strongThreshold` deleted; `topK` unchanged at 3.
- `Service.Chat` — rewritten middle: both retrievers to depth `candidateK`, fuse when both present, gate *after* retrieval as `bm25Max < minThreshold && bestCosine < cosineMin`, then `topSections(..., topK)`.
- Embed failure ⇒ warn + BM25-only, no longer a 500 (`service.go:109-112`).
- `strategy` gains `"hybrid"`; `"markdown"` when BM25-only; `""` on deny.

**Verify**: `make verify` passes. New tests: `FuseRRF` with known rank lists produces the computed order; a document in only one list still ranks; ties are deterministic across runs. **Regression test named for the original bug** — pure-Chinese query, BM25 scores all zero, vector list non-empty ⇒ answer returned, not `deny`. English identifier query ⇒ `Engineering Context` chunk present. `embedder == nil` ⇒ `strategy == "markdown"`, behavior identical to pre-change. Rewritten (not deleted): `TestServiceChat`'s `strong_score_uses_markdown` case (`service_test.go:157`), `TestServiceChatWeakScoreUsesVectorRetrieval` (`:251`).

---

## Phase 5: Acceptance (not a vertical slice — a checkpoint)

No production code. Runs the design's success criteria end to end and records the numbers that Phase 4's constants were guessed at.

**Files**: `thoughts/qrspi/2026-07-20-wpf-gherkin-kb/acceptance.md` (new)

**Verify**: Fresh `.kb/`, real bundle in `docs/`, Ollama up, `POST /index`, then the 5 acceptance queries (3 Chinese how-to, 2 English identifier). For each, record: answer correctness, `sources`, `strategy`, whether a screenshot filename came back, and **the observed `bm25Max` and `bestCosine`** — the last is the only evidence for whether `cosineMin = 0.30` is anywhere near right.

---

## Testing Checkpoints

| After | Should be true |
|---|---|
| **1** | `make verify` green. Chinese input tokenizes to bigrams; CJK headings yield distinct non-empty anchors; an index with a stale `anchor_version` is refused at startup. Chinese `/chat` no longer universally denies against the sample docs. |
| **2** | `make verify` green. Nested `.md` files discovered; `Section.File()` is relative and unique across folders; frontmatter and `*(Image: …jpg)*` survive a save/load round trip; bodyless sections absent. |
| **3** | `make verify` green. Runs against Ollama with no OpenAI key. Embed-model stamp mismatch downgrades to BM25-only with a warning. **The zh-TW embedder sanity check passed** — if not, Phase 4 is built on a false premise and the model choice must change first. |
| **4** | `make verify` green. A zero-BM25 Chinese query is answered via the vector path. `strategy == "hybrid"` when both retrievers contribute. A dead embedder degrades instead of 500-ing. |
| **5** | The 5 acceptance queries pass, and `acceptance.md` records real `bestCosine` values so `cosineMin` stops being a guess. |

**Resumption note**: Phases 1 and 2 are independently valuable and shippable — Chinese retrieval works and the real KB is ingestible even if 3 and 4 never land. Phase 3 is the go/no-go: its embedder sanity check decides whether Phase 4's core assumption holds.
