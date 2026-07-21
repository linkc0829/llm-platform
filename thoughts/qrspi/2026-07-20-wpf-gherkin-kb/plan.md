# Implementation Plan

## Overview

Make Chinese questions retrievable and answerable against the real flat WPF replay Markdown bundle, using rank-fused BM25 + vector retrieval over a local Ollama. Four vertical phases plus an acceptance checkpoint.

**Commands**: `make verify` (= `golangci-lint run ./...` then `go test -race -short -count=1 ./...`), `make test`, `make lint`, `make run`.

**House style** (non-negotiable, from research): stdlib `testing` only — no testify/cmp/gomock. Hand-written fakes. `t.Run` + snake_case subtest names for table-driven tests only. No `t.Parallel`. Assertion format `"<Func>(<args>) <field> = %v, want %v"`.

---

## Phase 1: CJK correctness + anchor versioning

### Changes

#### 1. Rewrite the two tokenizers

**File**: `internal/kb/domain.go`
**Action**: modify

Delete `var nonSlug` (`:37`) and `var tokenSplit` (`:46`). Remove the now-unused `regexp` import. Add `unicode` to imports.

```go
const anchorVersion = 1

func isASCIIAlnum(r rune) bool {
	return (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

func isCJK(r rune) bool {
	return unicode.Is(unicode.Han, r) ||
		unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r) ||
		unicode.Is(unicode.Hangul, r)
}

// tokenize splits text into BM25 terms. ASCII alphanumeric runs stay whole so
// identifiers like LoginViewModel remain matchable; CJK runs become overlapping
// character bigrams because we have no segmenter.
func tokenize(text string) []string {
	runes := []rune(text)
	out := make([]string, 0, len(runes))
	for i := 0; i < len(runes); {
		switch {
		case isASCIIAlnum(runes[i]):
			j := i
			for j < len(runes) && isASCIIAlnum(runes[j]) {
				j++
			}
			out = append(out, strings.ToLower(string(runes[i:j])))
			i = j
		case isCJK(runes[i]):
			j := i
			for j < len(runes) && isCJK(runes[j]) {
				j++
			}
			out = append(out, bigrams(runes[i:j])...)
			i = j
		default:
			i++
		}
	}
	return out
}

func bigrams(run []rune) []string {
	if len(run) == 1 {
		return []string{string(run)}
	}
	out := make([]string, 0, len(run)-1)
	for i := 0; i+1 < len(run); i++ {
		out = append(out, string(run[i:i+2]))
	}
	return out
}

func slugify(h string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.TrimSpace(h) {
		switch {
		case isASCIIAlnum(r):
			b.WriteRune(unicode.ToLower(r))
			prevDash = false
		case isCJK(r):
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}
```

**Known limitation to leave as-is**: non-ASCII, non-CJK letters (accented Latin, Cyrillic) are still dropped — same as today. Out of scope.

#### 2. Anchor version stamp

**File**: `internal/kb/dto_internal.go` — add to `indexJSON`:
```go
type indexJSON struct {
	AnchorVersion int           `json:"anchor_version"`
	Sections      []sectionJSON `json:"sections"`
	Corpus        corpusJSON    `json:"corpus"`
}
```

**File**: `internal/kb/errors.go` — add sentinel:
```go
ErrIndexStale = errors.New("index was built with an older anchor scheme; re-run /index")
```

**File**: `internal/kb/repo_markdown.go`
- `Save` (`:49-52`): set `AnchorVersion: anchorVersion` on the `indexJSON` literal.
- `Load` (`:86`): after unmarshal, before building sections:
```go
	if in.AnchorVersion != anchorVersion {
		return nil, ErrIndexStale
	}
```

**File**: `internal/kb/service.go` — `LoadOnStartup` (`:64-71`): propagate alongside the existing `ErrNotIndexed` check:
```go
	if errors.Is(err, ErrNotIndexed) || errors.Is(err, ErrIndexStale) {
		return err
	}
```

**File**: `cmd/kb/main.go` (`:48-54`) — extend the existing warn branch:
```go
	case errors.Is(err, kb.ErrNotIndexed):
		lg.Warn("knowledge base not indexed yet; POST /index to build it")
	case errors.Is(err, kb.ErrIndexStale):
		lg.Warn("index is stale; POST /index to rebuild it")
```

### Verification

#### Automated
- [x] `make verify` passes
- [x] `TestTokenize` extended to table-driven (`t.Run` + snake_case), covering: `pure_cjk` (`"動態密碼"` → `["動態","態密","密碼"]`), `single_cjk_char`, `mixed_cjk_ascii` (identifier survives whole), `ascii_only` (existing behavior unchanged), `punctuation_only` (empty)
- [x] `TestSlugify` extended: `cjk_preserved`, `cjk_with_punctuation` (`"Scenario: 登入 完整操作劇本"` → non-empty, contains CJK), `collapses_runs`, `trims_edges`, plus the two existing ASCII cases still pass
- [x] New `TestMarkdownRepoLoadStaleAnchorVersionReturnsErrIndexStale` in `repo_markdown_test.go` — write an `index.json` with `"anchor_version": 0` via `writeTestFile`, assert `errors.Is(err, ErrIndexStale)`
- [x] Existing `TestMarkdownRepoSaveLoadRoundTrip` still passes (Save now stamps, Load now checks)

#### Manual
- [x] `rm -rf .kb` then `KB_LLM_MODE=fake make run`; `POST /index`; `POST /chat` with a Chinese question against `docs/*.md` — response is no longer `"I cannot confirm that from the knowledge base."`
- [x] Hand-edit `.kb/index.json` to `"anchor_version": 0`, restart — startup logs the stale warning and does not serve the old anchors

---

## Phase 2: Ingest the real WPF replay bundle

### Changes

#### 1. Section carries metadata and images

**File**: `internal/kb/domain.go`
**Action**: modify

```go
type Section struct {
	file    string
	heading string
	anchor  string
	body    string
	meta    map[string]string
	images  []string
}

func NewSection(file, heading, body string, meta map[string]string, images []string) (Section, error) {
	if file == "" || heading == "" {
		return Section{}, ErrInvalidSection
	}
	return Section{
		file: file, heading: heading, anchor: slugify(heading),
		body: body, meta: meta, images: images,
	}, nil
}

func rehydrateSection(file, heading, anchor, body string, meta map[string]string, images []string) Section

func (s Section) Meta() map[string]string { return s.meta }
func (s Section) Images() []string        { return s.images }
```

Update every call site to pass `nil, nil` where it has no metadata: `repo_markdown.go`, `dto_internal.go`, `service_test.go` (`mustSampleSections`), `repo_markdown_test.go`, `domain_test.go`, `fake_llm_test.go`.

#### 2. Persist the new fields

**File**: `internal/kb/dto_internal.go`
```go
type sectionJSON struct {
	File    string            `json:"file"`
	Heading string            `json:"heading"`
	Anchor  string            `json:"anchor"`
	Body    string            `json:"body"`
	Meta    map[string]string `json:"meta,omitempty"`
	Images  []string          `json:"images,omitempty"`
}
```
Update `toSectionJSON` / `fromSectionJSON` to carry both through.

#### 3. Recursive walk + relative paths + WPF metadata + images

**File**: `internal/kb/repo_markdown.go`
**Action**: modify

Replace `Parse` (`:24-42`) — `filepath.Glob` → `filepath.WalkDir`:
```go
func (r *MarkdownRepo) Parse(ctx context.Context) ([]Section, int, error) {
	sections := make([]Section, 0)
	files := 0
	err := filepath.WalkDir(r.docsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.IsDir() || !strings.EqualFold(filepath.Ext(path), ".md") {
			return nil
		}
		rel, err := filepath.Rel(r.docsDir, path)
		if err != nil {
			return fmt.Errorf("relative path: %w", err)
		}
		fileSections, err := parseMarkdownFile(path, filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		files++
		sections = append(sections, fileSections...)
		return nil
	})
	if err != nil {
		return nil, 0, fmt.Errorf("walk docs: %w", err)
	}
	return sections, files, nil
}
```
Add `io/fs` to imports.

`parseMarkdownFile(path, relName string)` replaces `fileName := filepath.Base(path)` with the passed `relName`. The source bundle uses no YAML frontmatter; it records file metadata and screenshots as standard Markdown:

```go
var metadataRE = regexp.MustCompile(`^\*\*([^*]+)\*\*:\s*(.+?)\s*$`)
var imageRE = regexp.MustCompile(`!\[[^]]*\]\(([^)\s]+)(?:\s+[^)]*)?\)`)

func parseMetadata(body string) map[string]string
func imagePaths(body string) []string
```

Read the file into lines and split it into heading sections. In `flush()`:
- skip when `strings.TrimSpace(body.String()) == ""` (drops bodyless sections)
- collect `**key**: value` metadata and standard Markdown image paths from the body
- pass the collected `meta` and `images` into `NewSection`

### Verification

#### Automated
- [x] `make verify` passes
- [x] `TestMarkdownRepoParseWalksNestedDirs` — `t.TempDir()` + `writeTestFile` creating `a/one.md` and `b/one.md`; assert both found and `File()` values differ (`"a/one.md"` vs `"b/one.md"`)
- [x] `TestMarkdownRepoParseExtractsWPFMetadataAndImages` — a file containing `**功能區**: 登入`, `**到達路徑**: start`, and `![登入](../screenshots/登入/00_動態密碼登入.png)`; assert metadata and image path
- [x] `TestMarkdownRepoParseSkipsEmptyBodySections` — `# H1` immediately followed by `## H2` yields one section, not two
- [x] `TestMarkdownRepoSaveLoadRoundTrip` extended to assert `Meta`/`Images` survive
- [x] `TestMarkdownRepoParseSplitsDocsIntoSections` updated for empty-body filtering

#### Manual
- [x] The 39 WPF replay Markdown files are under `docs/`; `POST /index` returns a plausible `sections` count
- [x] `POST /chat` returns a `sources` entry containing a WPF Markdown filename (e.g. `登入__00_動態密碼登入.md#登入-00_動態密碼登入`)

---

## Phase 3: Config + local Ollama + model stamp

### Changes

#### 1. VectorStore returns the model stamp

**File**: `internal/kb/ports.go` — **breaking signature change**:
```go
type VectorStore interface {
	Load(ctx context.Context) (string, map[string][]float32, error)
	Save(ctx context.Context, model string, vectors map[string][]float32) error
}
```

**File**: `internal/kb/repo_vector.go` — `Load` (`:20-41`) returns `in.Model` alongside `in.Vectors`; both empty-map early returns become `return "", map[string][]float32{}, nil`.

**File**: `internal/kb/service_test.go` — `fakeVectorStore` (`:74-90`) gains a `loadModel string` field and matches the new signature.

#### 2. Service knows its embed model; stamp is compared

**File**: `internal/kb/service.go`
**Action**: modify

Delete `embeddingModel` from the const block (`:19`). Add field + constructor param:
```go
type Service struct {
	// ...existing...
	embedModel string
}

func NewService(sections sectionStore, llm LLM, embedder Embedder, vectors VectorStore, sessions SessionStore, embedModel string) *Service
```
`embedSections` (`:159`): `s.vectors.Save(ctx, s.embedModel, vecMap)`.

**File**: `internal/kb/errors.go` — add:
```go
ErrVectorsIgnored = errors.New("vector index was built with a different embedding model; re-run /index")
```

`LoadOnStartup` (`:73-79`) — mismatch drops the vectors but still serves BM25-only, signalling the caller:
```go
	vecMap := map[string][]float32{}
	stale := false
	if s.vectors != nil {
		model, loaded, err := s.vectors.Load(ctx)
		if err != nil {
			return fmt.Errorf("load vectors: %w", err)
		}
		if len(loaded) > 0 && model != s.embedModel {
			stale = true // never mix vectors from two embedding models
		} else {
			vecMap = loaded
		}
	}

	s.storeIndexSnapshot(secs, BuildCorpus(secs), vecMap, true)
	if stale {
		return ErrVectorsIgnored
	}
	return nil
```

**File**: `cmd/kb/main.go` — add a third warn case for `kb.ErrVectorsIgnored` (service is ready; this is not fatal).

> **Note**: the service has no logger and this plan does not add one — the repo has no precedent for it. Startup-time degradation is surfaced through `main.go`'s existing warn pattern. The runtime embed-failure degradation added in Phase 4 is observable only via the response's `strategy` field. Accepted limitation; do not add a logger to `Service` to "fix" it.

#### 3. Configurable base URL, models, and directories

**File**: `internal/kb/adapter_openai.go`
```go
func NewOpenAIClient(apiKey, baseURL, chatModel, embedModel string) *OpenAIClient {
	opts := []option.RequestOption{option.WithAPIKey(apiKey)}
	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	return &OpenAIClient{
		client:     openai.NewClient(opts...),
		chatModel:  openai.ChatModel(chatModel),
		embedModel: openai.EmbeddingModel(embedModel),
	}
}
```

**File**: `internal/platform/config/config.go`
```go
type OpenAIConfig struct {
	APIKey     string `mapstructure:"api_key"`
	LLMMode    string `mapstructure:"llm_mode"`
	BaseURL    string `mapstructure:"base_url"`
	ChatModel  string `mapstructure:"chat_model"`
	EmbedModel string `mapstructure:"embed_model"`
}

type KBConfig struct {
	DocsDir  string `mapstructure:"docs_dir"`
	IndexDir string `mapstructure:"index_dir"`
}
```
Add `KB KBConfig` to `Config`. New defaults: `openai.chat_model=gpt-4o-mini`, `openai.embed_model=text-embedding-3-small`, `kb.docs_dir=docs`, `kb.index_dir=.kb`. New binds: `OPENAI_BASE_URL`, `KB_CHAT_MODEL`, `KB_EMBED_MODEL`, `KB_DOCS_DIR`, `KB_INDEX_DIR`.

Relax the API-key rule (`:46`) and fix the case-sensitivity split against `main.go:37`:
```go
	if !strings.EqualFold(cfg.OpenAI.LLMMode, "fake") &&
		cfg.OpenAI.BaseURL == "" && cfg.OpenAI.APIKey == "" {
		return nil, fmt.Errorf("OPENAI_API_KEY is required")
	}
```

**File**: `cmd/kb/main.go` (`:32-33`, `:42-45`) — literals → `cfg.KB.DocsDir` / `cfg.KB.IndexDir`; `NewOpenAIClient(cfg.OpenAI.APIKey, cfg.OpenAI.BaseURL, cfg.OpenAI.ChatModel, cfg.OpenAI.EmbedModel)`; pass `cfg.OpenAI.EmbedModel` as `NewService`'s new last argument.

**File**: `.env.example` — add the five new variables with Ollama-pointing comments.

### Verification

#### Automated
- [x] `make verify` passes
- [x] `TestLoadKBAllowsBaseURLWithoutAPIKey` in `config_test.go`, following the existing `unsetEnv` helper pattern
- [x] `TestLoadKBReadsEnvFileAliases` extended with one new variable
- [x] `TestServiceLoadOnStartupIgnoresMismatchedVectorModel` — `fakeVectorStore` returns model `"old-model"`, service configured with `"new-model"`; assert `errors.Is(err, ErrVectorsIgnored)`, and that a subsequent `Chat` still answers with `strategy == "markdown"`
- [x] Existing `TestServiceIndexBuildsAndPersistsIndex` updated — the `embeddingModel` assertion (`service_test.go:112-113`) now asserts the configured value

#### Manual
- [x] `ollama pull` the chosen chat + embed models; set `OPENAI_BASE_URL=http://localhost:11434/v1`, `KB_CHAT_MODEL`, `KB_EMBED_MODEL`; `rm -rf .kb`; `make run`; `POST /index` completes with **no OpenAI API key set**
- [x] **Go/no-go — zh-TW embedder sanity check.** Embed three strings: two paraphrases of one flow's scenario text (A, A′) and one unrelated flow's (B). Confirm `Cosine(A, A′) > Cosine(A, B)`. **If this fails, stop — Phase 4's core premise is false and the embed model must change first.** Record all three values; they feed the Phase 5 `cosineMin` calibration.
- [x] Re-run with a different `KB_EMBED_MODEL` without re-indexing — startup warns and serves BM25-only rather than mixing vectors

---

## Phase 4: True hybrid (RRF fusion + per-retriever gate)

### Changes

#### 1. Two new pure functions

**File**: `internal/kb/domain.go`
**Action**: modify — both mirror `RankBM25` (`:176-188`): sort desc by score, ties by ascending index.

```go
// RankVector scores every indexed section against the query vector by cosine,
// dropping non-positive scores, and returns at most limit results.
func RankVector(indexed []Section, vecMap map[string][]float32, q []float32, limit int) []ScoredSection {
	ranked := make([]ScoredSection, 0, len(indexed))
	for i, section := range indexed {
		score := Cosine(q, vecMap[section.Citation()])
		if score <= 0 {
			continue
		}
		ranked = append(ranked, ScoredSection{Index: i, Score: score})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].Score == ranked[j].Score {
			return ranked[i].Index < ranked[j].Index
		}
		return ranked[i].Score > ranked[j].Score
	})
	if limit < len(ranked) {
		ranked = ranked[:limit]
	}
	return ranked
}

// FuseRRF combines ranked lists by reciprocal rank, which needs no score
// normalization — BM25 is unbounded, cosine is [-1,1], and the two are not
// comparable on scale.
func FuseRRF(lists [][]ScoredSection, rrfK int) []ScoredSection {
	acc := make(map[int]float64)
	for _, list := range lists {
		for rank, scored := range list {
			acc[scored.Index] += 1.0 / float64(rrfK+rank+1)
		}
	}
	fused := make([]ScoredSection, 0, len(acc))
	for idx, score := range acc {
		fused = append(fused, ScoredSection{Index: idx, Score: score})
	}
	sort.SliceStable(fused, func(i, j int) bool {
		if fused[i].Score == fused[j].Score {
			return fused[i].Index < fused[j].Index
		}
		return fused[i].Score > fused[j].Score
	})
	return fused
}
```
The index tie-break makes the result deterministic despite map iteration order.

#### 2. Rewrite Chat's retrieval middle

**File**: `internal/kb/service.go`
**Action**: modify

Const block: delete `strongThreshold`, add:
```go
	candidateK = 20   // depth each retriever contributes to fusion
	rrfK       = 60
	cosineMin  = 0.30 // placeholder — calibrate against the Phase 5 numbers
```

Replace `Chat` (`:100-121`) from the BM25 call to the end:
```go
	bm25List := corpus.RankBM25(tokenize(contextualQuery))
	if len(bm25List) > candidateK {
		bm25List = bm25List[:candidateK]
	}
	bm25Max := 0.0
	if len(bm25List) > 0 {
		bm25Max = bm25List[0].Score
	}

	var vecList []ScoredSection
	if s.embedder != nil && len(vecMap) > 0 {
		// A dead embedder degrades to BM25-only rather than failing the request.
		if queryVectors, err := s.embedder.Embed(ctx, []string{contextualQuery}); err == nil && len(queryVectors) > 0 {
			vecList = RankVector(indexed, vecMap, queryVectors[0], candidateK)
		}
	}
	bestCosine := 0.0
	if len(vecList) > 0 {
		bestCosine = vecList[0].Score
	}

	// Per-retriever gate: a Chinese query may score near zero on BM25 and still
	// be answerable from the vector side, so both must fail before we refuse.
	if bm25Max < minThreshold && bestCosine < cosineMin {
		return s.deny(ctx, sessionID, query)
	}

	ranked, strategy := bm25List, "markdown"
	switch {
	case len(vecList) > 0 && len(bm25List) > 0:
		ranked, strategy = FuseRRF([][]ScoredSection{bm25List, vecList}, rrfK), "hybrid"
	case len(vecList) > 0:
		ranked, strategy = vecList, "vector"
	}

	sections := topSections(indexed, ranked, topK)
	if len(sections) == 0 {
		return s.deny(ctx, sessionID, query)
	}
	return s.answerFrom(ctx, sessionID, query, sections, history, strategy)
```

Delete `topByCosine` (`:196-212`) — now unreachable, and the `unused` linter will fail the build otherwise.

### Verification

#### Automated
- [x] `make verify` passes
- [x] `TestFuseRRF` and `TestRankVector`
- [x] `TestServiceChatAnswersChineseQueryWithZeroBM25`
- [x] `TestServiceChatEnglishIdentifierUsesBM25`
- [x] `TestServiceChatWeakScoreUsesVectorRetrieval` covers hybrid results
- [x] `TestServiceChatDegradesWhenEmbedderFails`
- [x] Existing nil-embedder and history tests pass unchanged

#### Manual
- [x] `POST /chat` with a Chinese how-to question returns `strategy == "hybrid"` and correct `sources`
- [x] Stop Ollama mid-session; `POST /chat` still answers English identifier queries instead of returning 500

---

## Phase 5: Acceptance checkpoint

No production code. **File**: `thoughts/qrspi/2026-07-20-wpf-gherkin-kb/acceptance.md` (create).

### Verification

#### Manual
- [x] `rm -rf .kb`; real bundle in `docs/`; Ollama running; `make run`; `POST /index`
- [x] Run 5 queries — 3 Chinese how-to, 2 English identifier — recording per query: answer correct (y/n), `sources`, `strategy`, screenshot filename returned (y/n), **observed `bm25Max` and `bestCosine`**
- [x] `strategy == "hybrid"` on at least the 3 Chinese queries
- [x] Write `acceptance.md` with the table above plus a one-line verdict on whether `cosineMin = 0.30` is defensible against the observed `bestCosine` spread

---

## Ticket cross-check

Every `ticket.md` requirement is either planned above or explicitly excluded in `design.md` § "What We're NOT Doing":

| Ticket | Where |
|---|---|
| §2 CJK tokenize / slugify / index stamp / tests | Phase 1 (stamp retargeted to `anchor_version` — see design.md correction) |
| §3.1–3.7 RRF, candidateK, RankVector, FuseRRF, Chat rewrite, per-retriever gate, degradation | Phase 4 |
| §3.8 Reranker port + HTTP adapter + `KB_RERANK_MODE` | **Excluded** — design decision 13 |
| §3.9 tests incl. updating `TestServiceChatWeakScoreUsesVectorRetrieval` | Phase 4 |
| §4 WalkDir, relative paths, frontmatter, meta/images, dto persistence, empty-body filter | Phase 2 |
| §5 base URL, config vars, model-stamp drift, `main.go` dirs | Phase 3 |
| §6.1–6.2 unit tests + end-to-end 5 queries | Phases 1–4, Phase 5 |
| §6.3 rerank on/off comparison | **Excluded** — no reranker exists to toggle |
| §6.4 20–30 question eval set | **Excluded** — design.md defers it; Phase 5 records raw cosine values so the next cycle can calibrate |
| §7 MCP, Terraform, Dockerfile/CI, pgvector | **Excluded** — out of scope |
