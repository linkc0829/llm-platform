# Research Findings

## Q1: Trace `tokenize` and `slugify` in `internal/kb/domain.go`

### Findings

**Both are regex-driven over an ASCII-only character class.**

- `domain.go:37` — `var nonSlug = regexp.MustCompile(`[^a-z0-9 -]`)`. No `+` quantifier, so it matches and deletes **each individual rune** not in `{a-z, 0-9, space, hyphen}`.
- `domain.go:39-44` — `slugify`: `ToLower(TrimSpace(h))` → delete all `nonSlug` runes → `ReplaceAll(" ", "-")`.
- `domain.go:46` — `var tokenSplit = regexp.MustCompile(`[^a-z0-9]+`)`; Go regexp is rune-based, so any non-ASCII-alphanumeric rune is delimiter material, and `+` collapses runs.
- `domain.go:48-58` — `tokenize`: `ToLower` → `tokenSplit.Split(text, -1)` → in-place filter (`out := parts[:0]`) dropping empty strings.

**Behaviour on CJK / non-ASCII input (derived by tracing; no test covers it):**

- `slugify`: `ToLower` is a no-op on CJK; every CJK rune matches `nonSlug` and is deleted individually. Interior spaces survive the strip (space is in the allowed set) and then become `-`. So a heading mixing CJK and spaces collapses to its ASCII residue plus hyphens — e.g. `"Scenario: 登入 完整操作劇本"` → `"scenario--"`; a heading that is entirely CJK → `""`.
- `tokenize`: CJK runs are consumed as delimiters and never appear in a token. `tokenize("hello 你好 world")` → `["hello","world"]`. A purely CJK string → `Split` returns `["",""]`, both filtered out → **empty slice**.
- `domain_test.go:8-26` (`TestSlugify`) and `domain_test.go:28-41` (`TestTokenize`) exercise only ASCII punctuation/digit cases. No non-ASCII case exists anywhere in the test suite.

**Consumers of `slugify` output:**

- `domain.go:17-22` `NewSection` sets `Section.anchor = slugify(heading)` (field at `domain.go:11`).
- `domain.go:33-35` `Section.Citation()` = `file + "#" + anchor`.
- `dto_internal.go:27-29` `toSectionJSON` writes `Anchor` into the persisted `"anchor"` field. On load, `dto_internal.go:31-33` `fromSectionJSON` → `rehydrateSection` (`domain.go:24-26`) copies the anchor **literally**; `slugify` is *not* re-run on load.
- Downstream of `Citation()`: `service.go:157` (vector map key), `service.go:199` (cosine lookup key), `service.go:256` (`citationsFor` → `NewCitation(file, anchor)`), `adapter_openai.go:85` (prompt context), `fake_llm.go:21`, and `dto_http.go:24-30` → `ChatResponse.Sources` (`dto_http.go:20`).
- **Anchor collision consequence is structural**: the anchor is both the citation-display key *and* the vector-map key (`service.go:157`), so two sections with equal `file#anchor` overwrite each other in `vecMap`.

**Consumers of `tokenize` output:**

- `domain.go:108` — `BuildCorpus` tokenizes `heading + " " + body` per section → `Corpus.DocTokens` (`domain.go:91`), `DocLen` (`:93`), `AvgLen` (`:121-123`), `DocFreq` (`:113-119`, deduped per doc).
- `domain.go:132-153` — `BM25Score` builds per-doc term frequency from `DocTokens`, scores against `queryTokens`.
- `service.go:100` — `corpus.RankBM25(tokenize(contextualQuery))` — the only query-side tokenization.
- `fake_llm.go:35-51` — `fakeVector(text)` calls `tokenize` and switches on individual tokens to build a 4-dimensional vector.

---

## Q2: Trace a request through `Service.Chat`

### Findings

**Constants** (`service.go:14-20`):
```go
strongThreshold = 1.7
minThreshold    = 1.2
topK            = 3
embeddingModel  = "text-embedding-3-small"
```
BM25 tuning constants live separately at `domain.go:127-130`: `bm25K1 = 1.5`, `bm25B = 0.75`.

**Ordered flow** (`service.go:85-122`):

1. `service.go:86-88` — blank query → `ErrEmptyQuery`.
2. `service.go:89-91` — empty `sessionID` → `uuid.NewString()`.
3. `service.go:93` — `indexSnapshot()` (`service.go:174-178`) reads `indexed/corpus/vecMap/ready` under `s.mu.RLock()`. Writers (`Index` `service.go:46-62`, `LoadOnStartup` `service.go:64-83`) swap the whole snapshot under `storeIndexSnapshot` (`service.go:165-172`) with `s.mu.Lock()`. No generation counter — just a mutex-guarded field swap.
4. `service.go:94-96` — `!ready` → `ErrNotIndexed`.
5. `service.go:98` — `history()` (`service.go:218-223`); nil session store → `nil`.
6. `service.go:232-243` — `composeQuery`: with history, produces `"<turn1.Query> <turn2.Query> ... <query>"`; without, the query unchanged.
7. `service.go:100` — `corpus.RankBM25(tokenize(contextualQuery))` (`domain.go:176-188`), sorted desc, ties by ascending index.
8. **Gate 1** `service.go:101-103` — `len(ranked) == 0 || ranked[0].Score < minThreshold` → `deny`.
9. **Gate 2** `service.go:105-107` — `ranked[0].Score >= strongThreshold || len(vecMap) == 0 || s.embedder == nil` → `topSections(indexed, ranked, topK)` → `answerFrom(..., "markdown")`.
10. Vector path (only when score ∈ `[1.2, 1.7)` **and** vectors + embedder present):
    - `service.go:109-112` — `Embed` error → real error `"embed query: %w"` (not a deny).
    - `service.go:113-115` — zero vectors returned → `deny`.
    - `service.go:118-120` — `topByCosine` (`service.go:196-212`) discards cosine `<= 0` (`service.go:200-202`); empty result → `deny`.
    - `service.go:121` — `answerFrom(..., "vector")`.

**`topSections`** (`service.go:180-194`) — clamps `k` to length, skips `Score <= 0` and out-of-range indices.

**`answerFrom`** (`service.go:132-140`) — calls `s.llm.Answer`; error → `"llm answer: %w"`; success → `NewAnswer(text, citationsFor(sections), strategy)` then `appendTurn`.

**`deny`** (`service.go:125-129`) — `cannotConfirm()` (`service.go:214-216`) returns `NewAnswer("I cannot confirm that from the knowledge base.", nil, "")`. A deny is a **successful** call (`err == nil`); it is distinguished only by text, nil sources, and empty strategy.

**Exhaustive deny conditions:** empty ranking; `ranked[0].Score < 1.2`; `len(queryVectors) == 0`; `len(sections) == 0` from `topByCosine`.

**`strategy` values (complete set):**

| Value | Condition | Line |
|---|---|---|
| `"markdown"` | `score >= 1.7` OR `len(vecMap) == 0` OR `embedder == nil` | `service.go:106` |
| `"vector"` | score ∈ `[1.2, 1.7)`, vectors+embedder present, ≥1 section after cosine cut | `service.go:121` |
| `""` | every `deny` | `service.go:215` |

**Structural note:** BM25 is an unconditional pre-gate. If `tokenize` yields no tokens, `ranked[0].Score` cannot reach `minThreshold`, so control returns at `service.go:101-103` and the vector branch at `service.go:109` is unreachable.

**History append** (`service.go:225-230`) — `appendTurn` is called from both `deny` (`service.go:127`) and `answerFrom` (`service.go:138`); no-op if `s.sessions == nil`.

---

## Q3: `MarkdownRepo.Parse` — discovery and splitting

### Findings

**Discovery** (`repo_markdown.go:24-42`):

- `repo_markdown.go:25` — `filepath.Glob(filepath.Join(r.docsDir, "*.md"))`. Single directory, non-recursive; no `filepath.Walk`/`WalkDir` exists anywhere in the file. Subdirectories are never descended.
- Order is `filepath.Glob`'s — lexical by filename.
- `repo_markdown.go:32-34` — `ctx.Err()` checked before each file.
- `repo_markdown.go:36-38` — first `parseMarkdownFile` error aborts the whole `Parse`.
- `repo_markdown.go:41` — returns `len(paths)` as the file count regardless of sections produced.

**Splitting** (`repo_markdown.go:98-147`):

- `repo_markdown.go:98` — `headingRE = ^(#{1,6})\s+(.*)$`. All six heading levels are treated **identically** as boundaries; no hierarchy or nesting is tracked, so an `##` under a `#` is a sibling section, not a child.
- Heading line itself is never written to any body (`continue` at the match branch).
- **Content before the first heading is dropped** — the `if heading != ""` guard at `repo_markdown.go:135` is false until a heading is seen. Silent; no warning.
- **Empty bodies are emitted**: `flush()` (`repo_markdown.go:112-123`) only refuses when `heading == ""`, and `NewSection` (`domain.go:17-22`) validates only `file`/`heading`. A heading immediately followed by another heading yields a `Section` with `Body() == ""`.
- **No code-fence state tracking**: a ` ``` `-fenced line beginning with `#` + whitespace still matches `headingRE` and splits a section.
- **No image handling**: `![alt](url)` or `*(Image: x.jpg)*` lines are ordinary body text (or dropped if before the first heading). Nothing extracts them.
- `repo_markdown.go:107` — `fileName := filepath.Base(path)`: the **bare filename**, no directory component. Two same-named files in different directories would be indistinguishable in citations (not currently reachable, since Parse is non-recursive).
- `repo_markdown.go:116` — body is `strings.TrimSpace`-d before `NewSection`.
- `repo_markdown.go:143-145` — final `flush()` after EOF.

**`Section` fields** (`domain.go:10-15`, all unexported, getters at `domain.go:28-31`):

| Field | Type | Source |
|---|---|---|
| `file` | `string` | `filepath.Base(path)` |
| `heading` | `string` | trimmed `headingRE` group 2 |
| `anchor` | `string` | `slugify(heading)` in `NewSection`; passed through verbatim in `rehydrateSection` |
| `body` | `string` | trimmed accumulated lines |

No field carries heading level, line number, directory path, front-matter, or images.

---

## Q4: Persistence — JSON shapes, `LoadOnStartup` validation, model identifier

### Findings

**`dto_internal.go` shapes** (verbatim tags):

```go
type sectionJSON struct {
	File    string `json:"file"`
	Heading string `json:"heading"`
	Anchor  string `json:"anchor"`
	Body    string `json:"body"`
}
type indexJSON struct {
	Sections []sectionJSON `json:"sections"`
	Corpus   corpusJSON    `json:"corpus"`
}
type corpusJSON struct {
	DocFreq map[string]int `json:"doc_freq"`
	DocLen  []int          `json:"doc_len"`
	AvgLen  float64        `json:"avg_len"`
	N       int            `json:"n"`
}
type vectorMetaJSON struct {
	Model   string               `json:"model"`
	Vectors map[string][]float32 `json:"vectors"`
}
```
`corpusJSON` omits `Corpus.DocTokens` (`domain.go:91`) — tokens are not persisted.

**Paths:**

- `repo_markdown.go:94-96` — `indexPath()` = `<indexDir>/index.json`. `Save` (`repo_markdown.go:44-68`): `MkdirAll 0o755`, `WriteFile 0o600`, `json.MarshalIndent`. `Load` (`repo_markdown.go:70-92`): missing file → `ErrNotIndexed` (`repo_markdown.go:76-78`).
- `repo_vector.go:62-68` — `vectorDir()` = `<indexDir>/faiss_index`; `metadataPath()` = `<indexDir>/faiss_index/metadata.json`. `Save` at `repo_vector.go:43-60`; `Load` at `repo_vector.go:20-41` returns an **empty map, not an error**, when the file is missing or `Vectors` is nil.

**Corpus is written but never read back**: `MarkdownRepo.Load` unmarshals `indexJSON` including `Corpus` but returns only `in.Sections` (`repo_markdown.go:87-91`). Both `Index` (`service.go:60`) and `LoadOnStartup` (`service.go:81`) call `BuildCorpus(secs)` fresh, so the persisted `doc_freq`/`doc_len`/`avg_len` are dead weight recomputed at every load.

**`LoadOnStartup` validation** (`service.go:64-83`): only error propagation — `ErrNotIndexed` passthrough, `"load index: %w"`, `"load vectors: %w"`. There is **no** check of vector dimensionality, of key correspondence between sections and vectors, of schema version, or of count matching. On success it unconditionally marks the snapshot `ready`.

**Embedding model identifier:**

- Written: `service.go:159` — `s.vectors.Save(ctx, embeddingModel, vecMap)` with the constant from `service.go:19`. Lands in `vectorMetaJSON.Model` (`repo_vector.go:51`) → the `"model"` key on disk.
- Read: `repo_vector.go:20-41` unmarshals `in.Model` but returns only `in.Vectors` (`repo_vector.go:40`). The model string never leaves the function.
- Compared: **nowhere**. The only other references to `embeddingModel` are the write site and assertions in `service_test.go:112-113`.
- Separately, `adapter_openai.go:23` hard-codes `openai.EmbeddingModelTextEmbedding3Small` as the client's embed model — a second, independent declaration of the same model name.

---

## Q5: Ports, implementers, wiring, OpenAI adapter

### Findings

**`ports.go:6-22`** — four interfaces:

```go
type LLM interface {
	Answer(ctx context.Context, query string, sections []Section, history []Turn) (string, error)
}
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}
type VectorStore interface {
	Load(ctx context.Context) (map[string][]float32, error)
	Save(ctx context.Context, model string, vectors map[string][]float32) error
}
type SessionStore interface {
	Get(ctx context.Context, id string) []Turn
	Append(ctx context.Context, id string, turn Turn)
}
```

| Port | Production impls | Test impls |
|---|---|---|
| `LLM` | `*OpenAIClient` (`adapter_openai.go:31`), `*FakeLLM` (`fake_llm.go:13`) | `*fakeLLM` (`service_test.go:34`) |
| `Embedder` | `*OpenAIClient` (`adapter_openai.go:51`), `*FakeLLM` (`fake_llm.go:27`) | `*fakeEmbedder` (`service_test.go:54`) |
| `VectorStore` | `*VectorRepo` (`repo_vector.go:20,43`) | `*fakeVectorStore` (`service_test.go:74`) |
| `SessionStore` | `*InProcStore` (`memory_inproc.go:32,50`) | none |

There is a **fifth, unexported** port not in `ports.go`: `sectionStore` at `service.go:22-26`, satisfied by `*MarkdownRepo` (`Parse`/`Save`/`Load`) and by `fakeSectionStore` in tests.

**`cmd/kb/main.go` wiring order:**

- `main.go:19` `config.LoadKB()` → `main.go:24` logger → `main.go:29-30` `signal.NotifyContext`.
- `main.go:32` — `kb.NewMarkdownRepo("docs", ".kb")` — **both directories are string literals**, not config.
- `main.go:33` — `kb.NewVectorRepo(".kb")` — literal.
- `main.go:34` — `kb.NewInProcStore()`.
- `main.go:37` — selector: `strings.EqualFold(cfg.OpenAI.LLMMode, "fake")`. True → `kb.NewFakeLLM()` assigned to **both** `llm` and `embedder` (`main.go:38-41`). False → `kb.NewOpenAIClient(cfg.OpenAI.APIKey)` assigned to both (`main.go:42-45`).
- `main.go:47` — `kb.NewService(repo, llm, embedder, vecRepo, sessions)`.
- `main.go:48-54` — `LoadOnStartup`; `ErrNotIndexed` → warn only; any other error → fatal.
- `main.go:55-60` — handler, gin engine, `RegisterRoutes(engine.Group(""), h)`, `httpserver.Wrap`.
- `main.go:62-82` — goroutine start, select on signal/error, timed shutdown.

**OpenAI adapter** (`adapter_openai.go:19-25`):

```go
return &OpenAIClient{
	client:     openai.NewClient(option.WithAPIKey(apiKey)),
	chatModel:  openai.ChatModelGPT4oMini,
	embedModel: openai.EmbeddingModelTextEmbedding3Small,
}
```
Auth is `option.WithAPIKey` only; **no base-URL option is passed**, so the SDK default endpoint applies. Models are SDK constants, used at `adapter_openai.go:40` (`Model: o.chatModel`) and `adapter_openai.go:56` (`Model: o.embedModel`). Neither is configurable.

---

## Q6: Configuration loading and validation

### Findings

**Structs** (`config.go:12-37`): `Config{App, HTTP, Logger, OpenAI}` (`:13-16`, untagged — viper lowercases field names to `app.*`, `http.*`, `logger.*`, `openai.*`); `AppConfig{Env, Name, ShutdownTimeout}` (`:20-22`); `HTTPConfig{Port int}` (`:26`); `LoggerConfig{Level, Encoding}` (`:30-31`); `OpenAIConfig{APIKey, LLMMode}` (`:35-36`).

**Env bindings** (`binds` map, `config.go:66-75`; bound in a loop at `:76-78`, return values discarded):

| viper key | env var |
|---|---|
| `app.env` | `APP_ENV` |
| `app.name` | `APP_NAME` |
| `app.shutdown_timeout` | `APP_SHUTDOWN_TIMEOUT` |
| `http.port` | `APP_PORT` |
| `logger.level` | `LOG_LEVEL` |
| `logger.encoding` | `LOG_ENCODING` |
| `openai.api_key` | `OPENAI_API_KEY` |
| `openai.llm_mode` | `KB_LLM_MODE` |

Also `SetEnvKeyReplacer(".", "_")` + `AutomaticEnv()` at `config.go:63-64`.

**No env var exists for docs dir, index dir, chat model, embed model, or base URL** — consistent with those being literals at `main.go:32-33` and SDK constants at `adapter_openai.go:22-23`.

**Defaults** (`config.go:55-61`): `app.env=development`, `app.name=knowledge-base-qa-bot`, `app.shutdown_timeout=10s`, `http.port=8080`, `logger.level=info`, `logger.encoding=json`, `openai.llm_mode=openai`. No default for `openai.api_key`.

**Error paths in `LoadKB()`** (`config.go:39-50`) — exactly two:
1. `config.go:43-45` — `v.Unmarshal` failure → `"unmarshal config: %w"`.
2. `config.go:46-48` — `cfg.OpenAI.LLMMode != "fake" && cfg.OpenAI.APIKey == ""` → `"OPENAI_API_KEY is required"`.

`_ = v.ReadInConfig()` at `config.go:83` discards its error, so a missing/unreadable `.env` never surfaces.

**`.env` reconciliation** (`config.go:80-84` setup; CWD-relative — confirmed by `config_test.go:25` doing `os.Chdir`):

```go
func applyEnvFileAliases(v *viper.Viper, binds map[string]string) {
	for key, env := range binds {
		if _, ok := os.LookupEnv(env); ok {
			continue
		}
		if v.InConfig(env) {
			v.Set(key, v.Get(env))
		}
	}
}
```
(`config.go:89-98`) Real process env wins; otherwise a `.env` line keyed by the env-var name (e.g. `KB_LLM_MODE=fake`) is copied onto the dotted viper key.

**Case-sensitivity inconsistency (observed fact):** `config.go:46` compares `LLMMode != "fake"` exactly, while `main.go:37` uses `strings.EqualFold(cfg.OpenAI.LLMMode, "fake")`. The same value is tested against the same literal with different semantics — `KB_LLM_MODE=Fake` passes the `EqualFold` selector at `main.go:37` but fails the exact check at `config.go:46`, so config load would demand an API key for a run that then uses the fake client.

---

## Q7: Test conventions and lint restrictions

### Findings

**Test files:** `internal/kb/{domain,service,repo_markdown,handler_http,fake_llm}_test.go`, `internal/platform/config/config_test.go`. No tests exist under `internal/platform/httpserver` or `internal/platform/logger`; there is no `test/integration/` directory.

**Style, uniform across the suite:**
- **stdlib `testing` only** — no testify, no gomock, no cmp. Assertions are `t.Fatalf`/`t.Errorf`.
- Message format is consistently `"<Func>(<args>) <field> = %v, want %v"`.
- Subtest names are **snake_case**: `"empty_query_returns_400"`, `"strong_score_uses_markdown"`, `"spaces_to_hyphens"`, `"orthogonal_returns_zero"`.
- `t.Run` is used **only** in the four table-driven tests: `TestSlugify` (`domain_test.go:8`), `TestCosine` (`domain_test.go:67`), `TestHandlerChat` (`handler_http_test.go:33`), `TestServiceChat` (`service_test.go:157`). All other tests are straight-line.
- `t.Parallel` is **not used anywhere**.
- `t.Helper()` in `writeTestFile` (`repo_markdown_test.go:88`), `mustSampleSections` (`service_test.go:355`), `unsetEnv` (`config_test.go:42`).
- `t.TempDir()` in `repo_markdown_test.go` and `config_test.go`.

Exemplar (`domain_test.go:8-26`):
```go
func TestSlugify(t *testing.T) {
	tests := []struct {
		name    string
		heading string
		want    string
	}{
		{name: "spaces_to_hyphens", heading: "Refund Timeline", want: "refund-timeline"},
		{name: "strips_punctuation", heading: "Change Email Address!", want: "change-email-address"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := slugify(tt.heading)
			if got != tt.want {
				t.Errorf("slugify(%q) = %q, want %q", tt.heading, got, tt.want)
			}
		})
	}
}
```

**Fakes and helpers:**

| Type | Location | Stands in for |
|---|---|---|
| `fakeSectionStore` | `service_test.go:11-32` | unexported `sectionStore` (`service.go:22-26`) |
| `fakeLLM` | `service_test.go:34-52` | `LLM`; records `calls`, `query`, `sections`, `history` |
| `fakeEmbedder` | `service_test.go:54-72` | `Embedder`; returns vectors keyed by input text |
| `fakeVectorStore` | `service_test.go:74-90` | `VectorStore`; records `savedModel`, `saved` |
| `fakeHandlerService` | `handler_http_test.go:14-31` | the `Handler.svc` service interface |
| `FakeLLM` (exported) | `fake_llm.go:9-51` — **production file**, not a test file | both `LLM` and `Embedder`; constructed in `service_test.go:132,294,309` |
| `writeTestFile` | `repo_markdown_test.go:87-92` | fixture helper |
| `mustSampleSections` | `service_test.go:354-375` | fixture helper |
| `citationStrings` / `sameStrings` | `service_test.go:377-395` | assertion helpers |
| `unsetEnv` | `config_test.go:41-58` | env helper with `t.Cleanup` restore |

**Existing tests that assert on the current retrieval behaviour** (i.e. would need revision if that behaviour changed): `TestServiceChat` (`service_test.go:157`, includes a `strong_score_uses_markdown` case), `TestServiceChatWeakScoreUsesVectorRetrieval` (`service_test.go:251`), `TestServiceChatUsesHistoryForFollowUpRetrieval` (`service_test.go:306`), and the `embeddingModel` assertion at `service_test.go:112-113`.

**depguard rules** (`.golangci.yml`):

| Rule | Glob | Denied imports |
|---|---|---|
| `domain` | `**/internal/*/domain.go` | `github.com/jackc/pgx`, `github.com/redis/go-redis`, `github.com/gin-gonic/gin`, `github.com/golang-jwt/jwt`, `github.com/linkc0829/go-knowledge-base-qa-bot/internal/platform` |
| `service` | `**/internal/*/service.go` | `github.com/jackc/pgx`, `github.com/redis/go-redis`, `github.com/gin-gonic/gin` |
| `handler` | `**/internal/*/handler_*.go`, excluding `!**/internal/*/handler_*_test.go` | `github.com/jackc/pgx`, `github.com/redis/go-redis` |

Note: these are **deny-lists of named packages**, not allow-lists — an arbitrary new third-party import into `domain.go` would not be caught unless it appears in the deny list. `domain.go` currently imports `regexp`, `sort`, `strings`, `math` (stdlib only).

**Other linters enabled:** `errcheck`, `gosimple`, `govet`, `ineffassign`, `staticcheck`, `unused`, `depguard`, `gosec`, `misspell`, `revive`, `bodyclose`, `errorlint`, `nilerr`. `run.tests: true`; `issues.exclude-rules` exempts `_test\.go` from `gosec` and `errcheck`.

**Makefile:**
- `verify: lint test` (`Makefile:63`) — no own recipe.
- `lint:` → `golangci-lint run ./...` (`Makefile:42`).
- `test: test-unit`; `test-unit:` → `go test -race -short -count=1 ./...` (`Makefile:29-32`).
- `build:` → `@if not exist $(BUILD_DIR) mkdir $(BUILD_DIR)` + `go build -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/kb` (`Makefile:19-21`) — **cmd.exe syntax**.
- `clean:` → `@if exist ... rmdir /s /q` / `del` (`Makefile:68-71`) — **cmd.exe syntax**.
- `run:` → `go run ./cmd/kb` (`Makefile:23-24`).
- Also present: `test-cover` (`:34-36`), `fmt` (`:44-45`), `vet` (`:47-48`), `tidy` (`:50-51`), `hooks-install` (`:56-58`).

---

## Cross-Cutting Observations

- **The ASCII-only character class in `domain.go:37,46` is the single root of two independent behaviours** — empty tokenization and anchor collapse — because `slugify` feeds the vector-map key (`service.go:157`) and `tokenize` feeds the only query-side scoring path (`service.go:100`).
- **BM25 is a hard pre-gate, not a parallel retriever.** `service.go:101-103` runs before any embedding call, so vector retrieval is reachable only in the narrow band `[minThreshold, strongThreshold)`. The two retrievers are alternatives, never combined.
- **Three separate declarations of "which embedding model"** exist and none are reconciled: `service.go:19` (stamped into the file), `adapter_openai.go:23` (actually used for API calls), and `fake_llm.go:35-51` (4-dim vectors, unstamped). `repo_vector.go:40` drops the persisted stamp on load, so no mismatch can be detected.
- **Persisted corpus statistics are written and then ignored** (`repo_markdown.go:87-91` vs `service.go:60,81`). The index file's `corpus` block has no reader.
- **Path/model configuration is split between two mechanisms**: viper-bound env vars for app/HTTP/logger/OpenAI-key, but hard-coded literals for docs dir, index dir (`main.go:32-33`) and model names (`adapter_openai.go:22-23`).
- **Testing house style is deliberately dependency-free**: stdlib `testing`, hand-rolled fakes in `service_test.go`, snake_case subtests, `t.Run` only when table-driven. No gomock despite `CLAUDE.md` R5.1 naming it — the repo's actual convention is hand-written fakes.
- **depguard is a deny-list**, so it constrains which *known* infrastructure packages may appear per layer but does not enforce "stdlib only" on `domain.go`.

## Open Areas

- **`.kb/` on-disk artifacts were not opened.** Their shapes are known from the DTOs, but the actual persisted contents (e.g. whether the current `metadata.json` carries a `model` stamp inconsistent with the vectors it holds) were not inspected.
- **`Corpus` mutation safety**: `toCorpusJSON` (`dto_internal.go`) copies map/slice references rather than deep-copying; whether any writer mutates a corpus after snapshot publication was not traced beyond `storeIndexSnapshot`.
- **`InProcStore` expiry semantics** (`memory_inproc.go`, `sweepExpired`) were inventoried but not traced in detail — the questions did not ask about session lifetime.
- **`FakeLLM`'s 4-dimension vectors vs OpenAI's 1536** — there is no dimension check anywhere in the load path (`service.go:64-83`), so a `.kb/` built in fake mode and loaded in OpenAI mode was not verified for what actually happens at `Cosine` (`domain.go:155-169`).
