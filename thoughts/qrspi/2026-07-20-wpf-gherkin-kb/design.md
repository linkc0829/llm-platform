# Design Discussion

## Current State

The service works end-to-end for ASCII English docs and is dead for Chinese input.

- **Two ASCII-only regexes are the root cause.** `domain.go:37` `[^a-z0-9 -]` and `domain.go:46` `[^a-z0-9]+`. CJK runes are deleted by `slugify` and consumed as delimiters by `tokenize`; a purely-CJK string tokenizes to an empty slice. No test covers non-ASCII (`domain_test.go:8-41`).
- **BM25 is a hard pre-gate, not one of two parallel retrievers.** `service.go:101-103` runs before any embedding call. With zero tokens the top score can't reach `minThreshold` (1.2), so `deny` returns and the vector branch at `service.go:109` is unreachable. Vector retrieval only ever fires in the band `[1.2, 1.7)` (`service.go:105-107`).
- **`strategy` has exactly three values**: `"markdown"` (`service.go:106`), `"vector"` (`service.go:121`), `""` for every deny (`service.go:215`). A deny returns `err == nil`.
- **Ingestion can't see the KB.** `repo_markdown.go:25` globs `<docsDir>/*.md` non-recursively; `repo_markdown.go:107` stores `filepath.Base(path)` only; content before the first heading is silently dropped (`repo_markdown.go:135`); no image or frontmatter extraction exists; `Section` carries exactly four string fields (`domain.go:10-15`).
- **The model stamp is write-only.** Written at `service.go:159` → `repo_vector.go:51`; `repo_vector.go:40` returns only `Vectors` and drops `Model`. Nothing compares it anywhere.
- **Paths and models are hard-coded literals**, outside viper: `main.go:32-33` (`"docs"`, `".kb"`), `adapter_openai.go:22-23` (SDK model constants). No base-URL option is passed to `openai.NewClient` (`adapter_openai.go:21`).
- **`config.go:46` uses `!= "fake"` while `main.go:37` uses `strings.EqualFold`** — `KB_LLM_MODE=Fake` selects the fake client but is still required to supply an API key.

### Correction to the ticket's premise

Ticket §2.3 justifies an index version stamp by claiming the persisted `doc_freq`/`doc_len`/`avg_len` go stale. **Those fields have no reader.** `repo_markdown.go:87-91` returns only `in.Sections`, and both `service.go:60` and `service.go:81` call `BuildCorpus(secs)` fresh. The persisted `corpus` block is dead data.

What actually survives a re-load is **`anchor`** — `rehydrateSection` (`domain.go:24-26`) copies it verbatim and never re-runs `slugify`. So an index built before the slugify fix comes back with collapsed anchors (`"scenario--"`), while newly embedded vectors key on the corrected anchor, and `vecMap` lookups at `service.go:199` silently miss. The stamp must version the **anchor scheme**, not the tokenizer.

## Desired End State

Chinese questions retrieve and answer, against the real 9-folder flow bundle, with hybrid retrieval over a local Ollama.

Verifiable by:

1. `tokenize("動態密碼怎麼登入")` returns bigrams; `slugify` on a CJK heading returns a distinct, non-empty anchor. Unit tests.
2. A pure-Chinese query whose BM25 top score is 0 still returns an answer via the vector list rather than `deny`. Regression test naming the original bug.
3. An English identifier query (`LoginViewModel`) returns the `Engineering Context` chunk. Unit test.
4. `Parse` over `docs/output/<NN>_<Area>/*.md` finds every file; `Section.File()` is the relative path; frontmatter and `*(Image: x.jpg)*` anchors survive into the answer.
5. End-to-end against Ollama: `POST /index` then 5 queries (3 Chinese how-to, 2 English identifier) return correct `sources`, `strategy == "hybrid"`, and screenshot filenames.
6. `make verify` passes.

## Patterns to Follow

- **Hand-written fakes, stdlib `testing` only.** `fakeSectionStore` (`service_test.go:11-32`), `fakeLLM` (`:34-52`), `fakeEmbedder` (`:54-72`), `fakeVectorStore` (`:74-90`). No testify, no cmp, no gomock. Assertion format `"<Func>(<args>) <field> = %v, want %v"`.
- **`t.Run` with snake_case subtest names, only for table-driven tests** — `TestSlugify` (`domain_test.go:8-26`) is the exemplar. Straight-line otherwise. `t.Parallel` is used nowhere; don't introduce it.
- **Pure functions in `domain.go`, orchestration in `service.go`.** `RankBM25` (`domain.go:176-188`) is the shape `RankVector` and `FuseRRF` should match: take data, return sorted `[]ScoredSection`, no `ctx`, no I/O.
- **Deterministic sort with explicit tie-break** — `RankBM25` sorts desc by score, ties by ascending index. `FuseRRF` must do the same or tests won't be reproducible.
- **Snapshot swap under `s.mu`** — `storeIndexSnapshot` (`service.go:165-172`) / `indexSnapshot` (`service.go:174-178`). Any new snapshot field goes through the same pair, not a separate lock.
- **Ports are narrow and hand-satisfied** (`ports.go:6-22`); `NewService` takes only interfaces.

### Patterns NOT to follow

- **`CLAUDE.md` R5.1 says gomock. The repo does not use it.** Rule 11 — follow the codebase, hand-write fakes. Flagging the doc as stale, not forking.
- **Don't imitate the write-only stamp** (`service.go:159` → `repo_vector.go:40`). Anything persisted for validation must have a reader.
- **Don't persist derived data with no reader** — the `corpus` block in `index.json` is the cautionary example. New persisted fields must be read back or not written.
- **Don't add a second declaration of a model name.** There are already three (`service.go:19`, `adapter_openai.go:23`, `fake_llm.go:35-51`) and none are reconciled.
- **depguard is a deny-list, not an allow-list** (`.golangci.yml`, `domain` rule names 5 specific packages). "`domain.go` uses stdlib only" is a convention lint will NOT catch — self-police it.

## Design Decisions

1. **CJK tokenization: character bigrams, stdlib only.** Per-rune scan; ASCII alphanumeric runs stay whole and lowercased (preserves `LoginViewModel` for identifier queries); CJK runs emit overlapping bigrams; length-1 runs emit the single rune; everything else is a separator. No dictionary, no external segmenter, no new import into `domain.go`.

2. **`slugify` preserves CJK and alphanumerics**, maps everything else to `-`, collapses runs, trims edges. Anchors become distinct per heading text.

3. **Index version stamp targets the anchor scheme.** `index.json` gains `anchor_version`; `LoadOnStartup` refuses a mismatched snapshot and returns a "re-index required" error rather than serving stale anchors. Chosen over stamping `tokenizer_version` because the tokenizer leaves no persisted residue, and over silently recomputing anchors on load because the vectors keyed to old anchors would still mismatch.

4. **`vecMap` stays keyed by `Section.Citation()`.** Correct once decisions 2 and 5 land. Consequence: **Phase 3's relative-path change is not optional and cannot lag** — while `file` is `filepath.Base` (`repo_markdown.go:107`), two same-named headings in different flow folders collide and one vector silently overwrites the other.

5. **`Section.File()` becomes the path relative to `docsDir`**, and discovery moves to `filepath.WalkDir`. Together these make `file#anchor` unique across the bundle.

6. **`VectorStore.Load` returns `(model string, vectors map[string][]float32, error)`.** Makes "forgot to compare the stamp" a compile error. Touches one port signature, one repo, one fake. Chosen over a separate `LoadMeta` method, which a caller can forget to call.

7. **True hybrid via RRF over rank, at `K = 60`.** BM25 scores are unbounded and corpus-dependent; cosine is `[-1,1]`. RRF needs no normalization or weight tuning. `candidateK = 20` per retriever feeding the fusion, `topK = 3` to the LLM — fusing two 3-item lists leaves RRF nothing to do.

8. **The BM25 pre-gate is removed; the confidence gate moves after retrieval and is per-retriever.** Deny iff `bm25_max < minThreshold` AND `best_cosine < cosineMin`. `minThreshold` keeps its value, `cosineMin` starts at `0.30` pending calibration, `strongThreshold` is deleted. This is what makes a zero-BM25 Chinese query answerable.

9. **`strategy` gains `"hybrid"`** when both lists contributed; `"markdown"` when BM25-only (no embedder, empty `vecMap`, or stamp mismatch); `""` on deny. Existing values keep their meaning, so old clients don't break.

10. **Embedder failure degrades to BM25-only with a warning, not a 500.** Today `service.go:109-112` propagates the error and fails the request; a dead Ollama shouldn't take down English retrieval.

11. **Stamp mismatch is treated as "no vectors"** — warn, serve BM25-only, ask for a re-index. Never mix vectors from two embedding models.

12. **Ollama needs no new adapter.** `openai-go` accepts `option.WithBaseURL`, and `ChatModel`/`EmbeddingModel` are string types. `OPENAI_BASE_URL`, `KB_CHAT_MODEL`, `KB_EMBED_MODEL`, `KB_DOCS_DIR`, `KB_INDEX_DIR` join the viper binds; `service.go:19`'s constant is deleted in favor of the configured value; `main.go:32-33`'s literals read config.

13. **No reranker — not even the port.** At 9 flows / 20–40 chunks, `candidateK=20` is nearly the whole corpus, so there is nothing for a reranker to reorder; and Ollama doesn't serve cross-encoders, so a real one means a second service to operate. Revisit when the KB reaches hundreds of flows and an eval set shows noise in top-K — the interface will be better informed then than it would be now.

14. **Empty-body sections are filtered at parse time.** An H1 immediately followed by H2 currently emits a bodyless `Section` (`repo_markdown.go:112-123`), which is pure BM25 noise.

## What We're NOT Doing

- No reranker port, adapter, or `KB_RERANK_MODE` config.
- No MCP `search_kb` server.
- No Dockerfile, CI, or Terraform. The `Makefile`'s `build`/`clean` keep their cmd.exe syntax (`Makefile:19-21,68-71`) — Linux CI is a later problem.
- No pgvector. `.kb/faiss_index/metadata.json` stays JSON + brute-force cosine; `VectorStore` already abstracts it.
- No chunking-strategy change. Heading-split already yields the two axes hybrid exploits (`### Scenario:` narrative vs `### Engineering Context` identifiers).
- No retrieval eval set built in this cycle — but `cosineMin = 0.30` is explicitly a placeholder, and calibrating it is the first thing the next cycle owes.
- No fix to the `config.go:46` / `main.go:37` case-sensitivity split beyond what Phase 4's config rework touches incidentally.
- No dimension validation between fake (4-dim) and real (1536-dim) vectors — the model stamp catches the realistic version of this.

## Open Risks

- **`cosineMin = 0.30` is a guess.** Set without an eval set, it is the single knob most likely to be wrong in both directions — too low and Chinese queries get confidently wrong answers, too high and they still get denied. Phase 5 must at minimum record observed cosine values for the 5 acceptance queries.
- **Whether a non-Chinese-trained embedder is good enough for Traditional Chinese is unverified.** The whole hybrid design assumes the vector path rescues zero-BM25 Chinese queries. If the chosen Ollama embed model is weak on zh-TW, decision 8 doesn't deliver and the fallback is worse than today (denies become bad answers). Cheapest early signal: embed two paraphrases of one flow's scenario text and check they're closer to each other than to an unrelated flow.
- **Bigrams inflate the corpus vocabulary** — every CJK run of length n yields n-1 tokens, and `DocFreq` (`domain.go:113-119`) grows accordingly. Fine at 20–40 chunks; unmeasured at hundreds.
- **Existing tests will break by design**: `TestServiceChat`'s `strong_score_uses_markdown` case (`service_test.go:157`), `TestServiceChatWeakScoreUsesVectorRetrieval` (`:251`), and the `embeddingModel` assertion (`:112-113`). These encode the behavior being replaced — they get rewritten, not deleted, and the rewrite must still assert *why* (Rule 9).
- **`.kb/` currently holds fake-mode 4-dim vectors stamped with an OpenAI model name.** It is untracked and must be deleted before the first real `/index`, or decision 11 will reject it on every startup — correct behavior, confusing symptom.
- **Phase ordering is load-bearing, not cosmetic.** Decision 4 depends on decision 5; shipping hybrid before the relative-path change means debugging silent vector collisions.
