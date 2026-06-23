# Implementation Plan — Knowledge Base Q&A Bot

## Overview

Add a standalone `internal/kb/` feature plus a `cmd/kb` entrypoint that indexes
`docs/*.md` into inspectable `.kb/` artifacts, answers questions grounded only in
retrieved heading-sections (citing `filename#anchor`), deterministically routes each
query (strong BM25 → markdown, weak BM25 + vectors → vector RAG, below threshold →
cannot-confirm), and supports multi-turn dialogue via an in-process session store.
Purely additive — no edits to `cmd/api` or the demo slices.

Module path: `github.com/linkc0829/go-knowledge-base-qa-bot`.
Verification commands (from `Makefile`): `make lint` (golangci-lint), `make test`
(`go test -race -short -count=1 ./...`).

Conventions to mirror (from `internal/order`):
- Handler defines a **local** `service` interface; `NewHandler(svc *Service)` takes the
  concrete `*Service` but stores it as the interface (`order/handler_http.go:15-27`).
- Per-handler `context.WithTimeout(c.Request.Context(), N)` around external calls.
- Error mapping via `errors.Is` switch in a `writeError(c, err)` helper using raw
  `c.JSON(http.StatusXxx, gin.H{"error": ...})` — **not** `httperr` (design decision 1).
- Domain constructors validate invariants, all-private fields, no `context`/IO imports.
- Hand-written fakes in tests, table-driven, `snake_case` case names, `testify`
  `assert`/`require`, `errors.Is` for sentinel assertions. **No gomock.**

---

## Phase 1: Skeleton + `/health` + config path

Server boots via `cmd/kb` with only `OPENAI_API_KEY`/`APP_PORT`, gin engine + KB
handler wired, `/health` answering. Establishes the package and the no-infra boot path
(resolves design Open Risk #1).

### Changes

#### 1. Config: add OpenAI field + `LoadKB()` that skips the DB/JWT gate
**File**: `internal/platform/config/config.go`
**Action**: modify

Add an `OpenAI` sub-config and a `LoadKB()` that reuses the same viper setup as `Load()`
but validates only `OPENAI_API_KEY`. Extract the shared viper construction so both paths
stay in sync.

```go
type Config struct {
	App    AppConfig
	HTTP   HTTPConfig
	DB     DBConfig
	Redis  RedisConfig
	JWT    JWTConfig
	OTel   OTelConfig
	Logger LoggerConfig
	OpenAI OpenAIConfig
}

type OpenAIConfig struct {
	APIKey string `mapstructure:"api_key"`
}
```

Add one entry to the `binds` map (harmless for `cmd/api`, required for `cmd/kb`):

```go
"openai.api_key": "OPENAI_API_KEY",
```

Refactor the viper build out of `Load()` into a private helper, then add `LoadKB()`:

```go
// newViper builds the viper instance with defaults, env binds, and optional .env.
// Shared by Load (full api) and LoadKB (kb-only, no DB/JWT).
func newViper() *viper.Viper { /* defaults + binds (incl. openai.api_key) + .env */ }

// LoadKB loads config for cmd/kb. It does NOT require POSTGRES_DSN/JWT_SECRET —
// the bot has no DB or auth. Only OPENAI_API_KEY is required.
func LoadKB() (*Config, error) {
	v := newViper()
	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}
	if cfg.OpenAI.APIKey == "" {
		return nil, fmt.Errorf("OPENAI_API_KEY is required")
	}
	return &cfg, nil
}
```

`Load()` keeps calling `cfg.validate()` (unchanged behavior for `cmd/api`).

#### 2. KB ports (initial)
**File**: `internal/kb/ports.go`
**Action**: create

Start with the interfaces this phase needs (none yet beyond a placeholder); ports grow
each phase. For Phase 1 the file can declare the package and the eventual port set as it
is filled in. Minimal Phase-1 content:

```go
package kb

// Outbound ports for the kb feature. Implementations live in repo_*/adapter_*/memory_*.
// Filled in across phases: SectionStore (P2), LLM (P3), Embedder+VectorStore (P4),
// SessionStore (P5).
```

#### 3. KB service (skeleton)
**File**: `internal/kb/service.go`
**Action**: create

```go
package kb

type Service struct {
	// dependencies injected in later phases (sections, vectors, llm, embedder, sessions)
}

func NewService() *Service { return &Service{} }
```

`NewService`'s signature will gain interface parameters in later phases.

#### 4. KB errors (initial sentinel)
**File**: `internal/kb/errors.go`
**Action**: create

```go
package kb

import "errors"

var (
	ErrNotIndexed = errors.New("knowledge base not indexed yet")
	ErrEmptyQuery = errors.New("query is required")
)
```

#### 5. KB HTTP DTOs (health only for now)
**File**: `internal/kb/dto_http.go`
**Action**: create

```go
package kb

type HealthResponse struct {
	Status string `json:"status"`
}
```

#### 6. KB handler + local service interface
**File**: `internal/kb/handler_http.go`
**Action**: create

```go
package kb

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// service is the local inbound interface the handler depends on (mocked in tests).
// Grows as endpoints are added (Index in P2, Chat in P3).
type service interface{}

type Handler struct {
	svc service
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

func (h *Handler) health(c *gin.Context) {
	c.JSON(http.StatusOK, HealthResponse{Status: "ok"})
}
```

#### 7. KB routes
**File**: `internal/kb/routes.go`
**Action**: create

Public routes (no auth, no `/api/v1` group — kb is its own service). Stubs for `/index`
and `/chat` are added in P2/P3; this phase wires only `/health`.

```go
package kb

import "github.com/gin-gonic/gin"

// RegisterRoutes wires kb endpoints. All routes are public (no auth).
func RegisterRoutes(rg *gin.RouterGroup, h *Handler) {
	rg.GET("/health", h.health)
	// rg.POST("/index", h.index)  // P2
	// rg.POST("/chat", h.chat)    // P3
}
```

#### 8. `cmd/kb` entrypoint
**File**: `cmd/kb/main.go`
**Action**: create

Mirrors `cmd/api/main.go`'s signal/shutdown shape but boots only logger + gin + kb. No
Postgres/Redis/auth/otel. Uses `httpserver.New`/`httpserver.Wrap` and the default config
logger settings.

```go
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/kb"
	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/platform/config"
	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/platform/httpserver"
	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/platform/logger"
)

func main() {
	cfg, err := config.LoadKB()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	lg, err := logger.New(logger.Config{Level: cfg.Logger.Level, Encoding: cfg.Logger.Encoding})
	if err != nil {
		log.Fatalf("logger: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	svc := kb.NewService() // gains deps in later phases
	h := kb.NewHandler(svc)

	engine := httpserver.New(lg)
	kb.RegisterRoutes(engine.Group(""), h)

	srv := httpserver.Wrap(engine, httpserver.Config{Port: cfg.HTTP.Port}, lg)

	errs := make(chan error, 1)
	go func() {
		if err := srv.Start(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errs <- err
		}
		close(errs)
	}()

	select {
	case <-ctx.Done():
	case err := <-errs:
		if err != nil {
			lg.Sugar().Fatalf("server: %v", err)
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.App.ShutdownTimeout)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	_ = lg.Sync()
}
```

> Note: in later phases `kb.NewService(...)` gains arguments and `main.go` also calls
> `svc.LoadOnStartup(ctx)` (P2) — update this call site then.

#### 9. `.env.example` — add OpenAI key
**File**: `.env.example`
**Action**: modify — append:

```
# ============================================================================
# OpenAI (kb bot)
# ============================================================================
OPENAI_API_KEY=
```

(R0.2: do not create `.env`.)

### Verification
#### Automated
- [x] `make lint` passes (no depguard violations on the new package).
- [x] `make test` passes.
- [x] `go build ./cmd/kb` succeeds.

#### Manual
- [x] `OPENAI_API_KEY=x go run ./cmd/kb` boots with **no** Postgres/Redis running.
- [x] `curl localhost:8080/health` → `200 {"status":"ok"}`.
- [x] Running `go run ./cmd/kb` with no `OPENAI_API_KEY` → fatal "OPENAI_API_KEY is required".

---

## Phase 2: Indexing → BM25 → `.kb/index.json`

`POST /index` parses `docs/*.md` into heading-sections, builds the BM25 corpus, persists
`.kb/index.json`, loads it into memory; reloaded on startup.

### Changes

#### 1. Domain: Section, slug, tokenizer, BM25
**File**: `internal/kb/domain.go`
**Action**: create

Pure, no IO/context. `NewSection` computes a GitHub-style anchor slug.

```go
package kb

import (
	"math"
	"regexp"
	"strings"
)

type Section struct {
	file    string
	heading string
	anchor  string
	body    string
}

func NewSection(file, heading, body string) (Section, error) {
	if file == "" || heading == "" {
		return Section{}, ErrInvalidSection
	}
	return Section{file: file, heading: heading, anchor: slugify(heading), body: body}, nil
}

func (s Section) File() string    { return s.file }
func (s Section) Heading() string { return s.heading }
func (s Section) Anchor() string  { return s.anchor }
func (s Section) Body() string    { return s.body }

// Citation string: filename#anchor (spec §3).
func (s Section) Citation() string { return s.file + "#" + s.anchor }

var nonSlug = regexp.MustCompile(`[^a-z0-9 -]`)

// slugify mimics GitHub heading anchors: lowercase, strip punctuation,
// spaces → hyphens.
func slugify(h string) string {
	s := strings.ToLower(strings.TrimSpace(h))
	s = nonSlug.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, " ", "-")
	return s
}

var tokenSplit = regexp.MustCompile(`[^a-z0-9]+`)

func tokenize(text string) []string {
	text = strings.ToLower(text)
	parts := tokenSplit.Split(text, -1)
	out := parts[:0]
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
```

BM25 corpus + scoring (Okapi BM25, `k1=1.5`, `b=0.75`):

```go
type Corpus struct {
	DocTokens [][]string         // per-section tokens (index aligns with []Section)
	DocFreq   map[string]int     // term → number of sections containing it
	DocLen    []int              // token count per section
	AvgLen    float64
	N         int                // number of sections
}

func BuildCorpus(sections []Section) Corpus { /* tokenize bodies, fill DocFreq/DocLen/AvgLen */ }

const (
	bm25K1 = 1.5
	bm25B  = 0.75
)

// BM25Score scores one section (docIdx) against query tokens.
func (c Corpus) BM25Score(docIdx int, queryTokens []string) float64 {
	score := 0.0
	tf := map[string]int{}
	for _, t := range c.DocTokens[docIdx] {
		tf[t]++
	}
	for _, q := range queryTokens {
		n := c.DocFreq[q]
		if n == 0 {
			continue
		}
		idf := math.Log(1 + (float64(c.N)-float64(n)+0.5)/(float64(n)+0.5))
		f := float64(tf[q])
		denom := f + bm25K1*(1-bm25B+bm25B*float64(c.DocLen[docIdx])/c.AvgLen)
		score += idf * (f * (bm25K1 + 1)) / denom
	}
	return score
}
```

> The router/topK ranking helper that returns the top score + top-K section indices is
> used by the service; add a small `func (c Corpus) Rank(queryTokens []string, k int) []ScoredSection`
> or rank inline in the service. Keep the pure math here, the routing decision in the service.

#### 2. Errors: add section sentinels
**File**: `internal/kb/errors.go`
**Action**: modify — add:

```go
ErrInvalidSection = errors.New("invalid section")
```

#### 3. Port: SectionStore
**File**: `internal/kb/ports.go`
**Action**: modify — add:

```go
import "context"

type SectionStore interface {
	Load(ctx context.Context) ([]Section, error) // returns ErrNotIndexed if absent
	Save(ctx context.Context, sections []Section) error
}
```

#### 4. On-disk DTOs
**File**: `internal/kb/dto_internal.go`
**Action**: create

JSON shapes for `.kb/index.json`. Includes sections + BM25 corpus stats so the file is
human-inspectable (spec §5).

```go
package kb

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

func toSectionJSON(s Section) sectionJSON { /* ... */ }
func fromSectionJSON(j sectionJSON) Section { /* uses NewSection or rehydrate */ }
```

> `fromSectionJSON` should reconstruct without re-validating slug (anchor already stored);
> add an unexported `rehydrateSection(file, heading, anchor, body string) Section` to
> `domain.go` mirroring `order`'s `rehydrate` pattern (`order/domain.go:50`).

#### 5. Markdown repo: parse + persist
**File**: `internal/kb/repo_markdown.go`
**Action**: create

Implements `SectionStore`. Parses `docs/*.md` on `^#{1,6} ` headings; reads/writes
`.kb/index.json`.

```go
package kb

type MarkdownRepo struct {
	docsDir  string // "docs"
	indexDir string // ".kb"
}

func NewMarkdownRepo(docsDir, indexDir string) *MarkdownRepo {
	return &MarkdownRepo{docsDir: docsDir, indexDir: indexDir}
}

// Parse reads docs/*.md and splits each file into heading-sections.
// Returns sections and the count of files parsed.
func (r *MarkdownRepo) Parse(ctx context.Context) (sections []Section, files int, err error) {
	// filepath.Glob(docsDir/*.md); for each file, scan lines, split on ^#{1,6} ,
	// accumulate body until next heading; NewSection(file, heading, body).
}

func (r *MarkdownRepo) Save(ctx context.Context, sections []Section) error {
	// build indexJSON{sections, BuildCorpus stats}; os.MkdirAll(.kb); write index.json (0644)
}

func (r *MarkdownRepo) Load(ctx context.Context) ([]Section, error) {
	// read .kb/index.json; os.IsNotExist → ErrNotIndexed; unmarshal → []Section
}
```

Heading regex: `^(#{1,6})\s+(.*)$`. Lines before the first heading in a file are ignored
(or attached to a synthetic file-level section — **ignore** them to keep parsing simple;
`ponytail:` preamble dropped, revisit if docs rely on it).

> `Parse` is separate from `SectionStore` (which is just Load/Save) because parsing is a
> markdown-repo concern the service calls during `Index`. The service depends on the
> concrete `*MarkdownRepo` for `Parse` **or** we widen the port — keep `Parse` on the
> concrete type and have `NewService` accept an interface that includes it. Decision:
> define the service's dependency as a local interface in `service.go` that includes
> `Parse`, `Load`, `Save` (capability-named `sectionStore`), satisfied by `*MarkdownRepo`.

#### 6. Service: Index + loadOnStartup
**File**: `internal/kb/service.go`
**Action**: modify

```go
type sectionStore interface {
	Parse(ctx context.Context) (sections []Section, files int, err error)
	Save(ctx context.Context, sections []Section) error
	Load(ctx context.Context) ([]Section, error)
}

type Service struct {
	sections sectionStore
	corpus   Corpus
	indexed  []Section
	ready    bool
}

func NewService(sections sectionStore) *Service {
	return &Service{sections: sections}
}

// Index parses docs, builds the corpus, persists, loads into memory.
func (s *Service) Index(ctx context.Context) (filesIndexed, sectionsIndexed int, err error) {
	secs, files, err := s.sections.Parse(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("parse docs: %w", err)
	}
	if err := s.sections.Save(ctx, secs); err != nil {
		return 0, 0, fmt.Errorf("save index: %w", err)
	}
	s.indexed = secs
	s.corpus = BuildCorpus(secs)
	s.ready = true
	return files, len(secs), nil
}

// LoadOnStartup attempts to load a prior index; logs (caller) a warning if absent.
func (s *Service) LoadOnStartup(ctx context.Context) error {
	secs, err := s.sections.Load(ctx)
	if errors.Is(err, ErrNotIndexed) {
		return ErrNotIndexed // caller logs warning, serves "not indexed"
	}
	if err != nil {
		return fmt.Errorf("load index: %w", err)
	}
	s.indexed = secs
	s.corpus = BuildCorpus(secs)
	s.ready = true
	return nil
}
```

#### 7. Handler: index endpoint + service interface
**File**: `internal/kb/handler_http.go`
**Action**: modify — extend local `service` interface and add handler:

```go
type service interface {
	Index(ctx context.Context) (filesIndexed, sectionsIndexed int, err error)
}

func (h *Handler) index(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
	defer cancel()
	files, sections, err := h.svc.Index(ctx)
	if err != nil {
		writeError(c, err) // writeError added in P3; for P2 a minimal version is fine
		return
	}
	c.JSON(http.StatusOK, IndexResponse{FilesIndexed: files, SectionsIndexed: sections})
}
```

`IndexResponse` in `dto_http.go`:

```go
type IndexResponse struct {
	FilesIndexed    int `json:"files_indexed"`
	SectionsIndexed int `json:"sections_indexed"`
}
```

> Index timeout is 60s (embeds nothing yet in P2; P4 adds OpenAI embedding calls under the
> same handler — 60s leaves headroom for the sample corpus).
> If `writeError` doesn't exist yet, add a minimal version mapping any error → 500 in P2 and
> expand it in P3.

#### 8. Routes: enable `/index`
**File**: `internal/kb/routes.go`
**Action**: modify — uncomment `rg.POST("/index", h.index)`.

#### 9. cmd/kb: build repo, inject, load on startup
**File**: `cmd/kb/main.go`
**Action**: modify

```go
repo := kb.NewMarkdownRepo("docs", ".kb")
svc := kb.NewService(repo)
if err := svc.LoadOnStartup(ctx); err != nil {
	if errors.Is(err, kb.ErrNotIndexed) {
		lg.Warn("knowledge base not indexed yet; POST /index to build it")
	} else {
		lg.Fatal("load index", zap.Error(err))
	}
}
h := kb.NewHandler(svc)
```

#### 10. Sample docs
**Files**: `docs/refund_policy.md`, `docs/account_help.md`, `docs/shipping.md` (3 docs)
**Action**: create

Must contain the headings the acceptance criteria cite. Required anchors:
- `refund_policy.md` → headings yielding `#refund-timeline` and `#non-refundable-items`.
- `account_help.md` → heading yielding `#change-email-address`.
- A third doc (e.g. `shipping.md`) for corpus breadth.

Example `docs/refund_policy.md`:

```markdown
# Refund Policy

## Refund Timeline
Refunds are processed within 5–7 business days after we receive the returned item.

## Non-Refundable Items
Gift cards, digital downloads, and final-sale items cannot be refunded.
```

### Verification
#### Automated
- [x] `make test` passes; unit tests cover: `slugify` (heading → GitHub anchor),
      markdown parse (3 docs → expected section count), `tokenize`, `BM25Score` ordering
      (a section containing the query terms scores above one that doesn't).
- [x] `make lint` passes.

#### Manual
- [x] `POST /index` → `200 {"files_indexed":3,"sections_indexed":N}`.
- [x] `.kb/index.json` exists, is valid JSON, and lists every section with `file`,
      `heading`, `anchor`, `body` plus corpus stats.
- [x] Restart `cmd/kb` without re-indexing → startup log shows the index loaded (no
      "not indexed" warning).

---

## Phase 3: Single-turn `/chat` — markdown strategy + grounding + cannot-confirm

`POST /chat` runs BM25; top score ≥ `strongThreshold` → call LLM with grounding prompt
over top-K sections, returning answer + `filename#anchor` citations. Below `minThreshold`
→ cannot-confirm (no LLM call). Vector branch stubbed until Phase 4.

### Changes

#### 1. Add `openai-go` dependency
**File**: `go.mod`
**Action**: modify
**Command**: `go get github.com/openai/openai-go@latest && go mod tidy`

> If `go get` is unavailable offline, add the require line manually and run `go mod tidy`
> when connectivity returns. The SDK is confined to `adapter_openai.go`; the rest of the
> package stays SDK-agnostic (design decision 2). Confirm no depguard rule denies it
> (`.golangci.yml` has no global third-party deny; only domain/service/handler globs — the
> adapter is none of those, so it's allowed).

#### 2. Domain: Answer, Citation, ranking helper
**File**: `internal/kb/domain.go`
**Action**: modify

```go
type Citation struct {
	file   string
	anchor string
}

func (c Citation) String() string { return c.file + "#" + c.anchor }

type Answer struct {
	text     string
	sources  []Citation
	strategy string // "markdown" | "vector" | "" (cannot-confirm)
}

func NewAnswer(text string, sources []Citation, strategy string) Answer {
	return Answer{text: text, sources: sources, strategy: strategy}
}

func (a Answer) Text() string        { return a.text }
func (a Answer) Sources() []Citation { return a.sources }
func (a Answer) Strategy() string    { return a.strategy }
```

Ranking helper used by the service:

```go
type ScoredSection struct {
	Index int
	Score float64
}

// RankBM25 returns section indices sorted by descending BM25 score.
func (c Corpus) RankBM25(queryTokens []string) []ScoredSection { /* score all, sort desc */ }
```

#### 3. LLM port
**File**: `internal/kb/ports.go`
**Action**: modify — add:

```go
type LLM interface {
	Answer(ctx context.Context, query string, sections []Section, history []Turn) (string, error)
}
```

> `Turn` is introduced in P5. For P3 either define `Turn` now (empty-history slice passed)
> or use `history []Turn` with `Turn` declared in `domain.go` early. **Decision:** declare
> `type Turn struct{ Query, Answer string }` in `domain.go` now and pass `nil` history in
> P3; P5 fills it. This avoids a signature change later.

#### 4. OpenAI adapter (LLM)
**File**: `internal/kb/adapter_openai.go`
**Action**: create

```go
package kb

// OpenAIClient implements LLM (P3) and Embedder (P4).
type OpenAIClient struct {
	client openai.Client
	chatModel  string // "gpt-4o-mini"
	embedModel string // "text-embedding-3-small"
}

func NewOpenAIClient(apiKey string) *OpenAIClient {
	return &OpenAIClient{
		client:     openai.NewClient(option.WithAPIKey(apiKey)),
		chatModel:  "gpt-4o-mini",
		embedModel: "text-embedding-3-small",
	}
}

const groundingSystem = `You answer questions ONLY using the provided context sections. ` +
	`If the answer is not contained in the context, reply that you cannot confirm it from ` +
	`the knowledge base. Cite sources as filename#heading.`

func (o *OpenAIClient) Answer(ctx context.Context, query string, sections []Section, history []Turn) (string, error) {
	// Build messages: system(groundingSystem), prior turns (history) as user/assistant,
	// then a user message containing the K sections (file#anchor + body) and the query.
	// Call o.client.Chat.Completions.New(ctx, ...); return choice text.
	// Wrap errors: fmt.Errorf("openai chat: %w", err).
}
```

> Exact SDK call shapes (`openai.ChatCompletionNewParams`, `option.WithAPIKey`) follow the
> installed `openai-go` version — consult its README if the symbols differ. Keep all SDK
> types inside this file.

#### 5. Routing constants + Service.Chat
**File**: `internal/kb/service.go`
**Action**: modify

```go
const (
	strongThreshold = 6.0 // top BM25 ≥ this → markdown strategy (CALIBRATE, Open Risk #2)
	minThreshold    = 2.0 // best score < this → cannot-confirm
	topK            = 3
)

type Service struct {
	sections sectionStore
	llm      LLM
	corpus   Corpus
	indexed  []Section
	ready    bool
}

func NewService(sections sectionStore, llm LLM) *Service {
	return &Service{sections: sections, llm: llm}
}

// Chat answers a single query (P3: markdown + cannot-confirm only).
func (s *Service) Chat(ctx context.Context, query, sessionID string) (Answer, string, error) {
	if strings.TrimSpace(query) == "" {
		return Answer{}, sessionID, ErrEmptyQuery
	}
	if !s.ready {
		return Answer{}, sessionID, ErrNotIndexed
	}

	ranked := s.corpus.RankBM25(tokenize(query))
	if len(ranked) == 0 || ranked[0].Score < minThreshold {
		return s.cannotConfirm(), sessionID, nil
	}

	if ranked[0].Score >= strongThreshold {
		secs := s.topSections(ranked, topK)
		text, err := s.llm.Answer(ctx, query, secs, nil)
		if err != nil {
			return Answer{}, sessionID, fmt.Errorf("llm answer: %w", err)
		}
		return NewAnswer(text, citationsFor(secs), "markdown"), sessionID, nil
	}

	// weak BM25 but ≥ minThreshold: vector branch added in P4.
	// P3 stub: fall back to markdown over the top sections so the loop is usable.
	secs := s.topSections(ranked, topK)
	text, err := s.llm.Answer(ctx, query, secs, nil)
	if err != nil {
		return Answer{}, sessionID, fmt.Errorf("llm answer: %w", err)
	}
	return NewAnswer(text, citationsFor(secs), "markdown"), sessionID, nil
}

func (s *Service) cannotConfirm() Answer {
	return NewAnswer("I cannot confirm that from the knowledge base.", nil, "")
}
```

Helpers `topSections(ranked, k)` and `citationsFor(secs)` map indices → sections →
`[]Citation`.

> The P3 weak-but-above-min fallback to markdown is replaced in P4 by the real vector
> branch. Mark it `// ponytail: P3 stub, replaced by vector branch in P4`.

#### 6. Errors + writeError switch
**File**: `internal/kb/errors.go` (already has `ErrNotIndexed`, `ErrEmptyQuery`)
**File**: `internal/kb/handler_http.go`
**Action**: modify — add `writeError` and `chat` handler.

```go
func writeError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrNotIndexed):
		c.JSON(http.StatusOK, gin.H{
			"answer":  "The knowledge base has not been indexed yet. POST /index first.",
			"sources": []string{},
		})
	case errors.Is(err, ErrEmptyQuery):
		c.JSON(http.StatusBadRequest, gin.H{"error": "query is required"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
	}
}
```

`chat` handler + DTOs:

```go
func (h *Handler) chat(c *gin.Context) {
	var req ChatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "query is required"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	ans, sid, err := h.svc.Chat(ctx, req.Query, req.SessionID)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, toChatResponse(ans, sid))
}
```

**File**: `internal/kb/dto_http.go` — add:

```go
type ChatRequest struct {
	Query     string `json:"query"`
	SessionID string `json:"session_id"`
}

type ChatResponse struct {
	SessionID string   `json:"session_id"`
	Answer    string   `json:"answer"`
	Sources   []string `json:"sources"`
	Strategy  string   `json:"strategy"`
}

func toChatResponse(a Answer, sessionID string) ChatResponse {
	srcs := make([]string, 0, len(a.Sources()))
	for _, c := range a.Sources() {
		srcs = append(srcs, c.String())
	}
	return ChatResponse{SessionID: sessionID, Answer: a.Text(), Sources: srcs, Strategy: a.Strategy()}
}
```

> The handler's local `service` interface gains
> `Chat(ctx, query, sessionID string) (Answer, string, error)`. Empty query is rejected
> both at bind (no `binding:"required"` tag, so handle in `Chat` via `ErrEmptyQuery`) — keep
> the 400 path consistent. Per spec §4, empty query → `400 {"error":"query is required"}`.

#### 7. Routes: enable `/chat`
**File**: `internal/kb/routes.go`
**Action**: modify — uncomment `rg.POST("/chat", h.chat)`.

#### 8. cmd/kb: inject LLM
**File**: `cmd/kb/main.go`
**Action**: modify

```go
llm := kb.NewOpenAIClient(cfg.OpenAI.APIKey)
svc := kb.NewService(repo, llm)
```

### Verification
#### Automated
- [x] `make test` passes with a hand-written `fakeLLM` (settable answer/err, call counter)
      and a `fakeSectionStore`. Cases (table-driven, `snake_case`):
      `strong_score_uses_markdown`, `both_weak_cannot_confirm` (`sources:[]`, LLM not
      called), `empty_query_returns_err_empty_query`, `not_indexed_returns_err_not_indexed`,
      `citations_formatted_as_file_hash_anchor`.
- [x] Handler test (`httptest` + mock service): empty query → 400; not-indexed → 200 with
      "not indexed" body; happy path → 200 with `answer`/`sources`/`strategy`.
- [x] `make lint` passes.

#### Manual (with `OPENAI_API_KEY` + indexed)
- [x] "How long do refunds take?" → cites `refund_policy.md#refund-timeline`,
      `strategy:"markdown"`.
- [x] "Can I change my email address?" → cites `account_help.md#change-email-address`.
- [x] "Which restaurants are nearby?" → cannot-confirm, `sources:[]`.
- [x] `POST /chat` before indexing → `200`, "not indexed yet" body.

---

## Phase 4: Vector RAG strategy

When top BM25 < `strongThreshold` and a vector index is present, embed the query and
retrieve by brute-force cosine; persist `.kb/faiss_index/metadata.json` at index time.
Makes `strategy:"vector"` observable (acceptance criterion 4).

### Changes

#### 1. Domain: cosine
**File**: `internal/kb/domain.go`
**Action**: modify

```go
// Cosine returns cosine similarity of two equal-length vectors. Returns 0 if either
// is zero-length or norms are zero.
func Cosine(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}
```

#### 2. Ports: Embedder, VectorStore
**File**: `internal/kb/ports.go`
**Action**: modify — add:

```go
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

type VectorStore interface {
	Load(ctx context.Context) (map[string][]float32, error) // id (file#anchor) → vector
	Save(ctx context.Context, model string, vectors map[string][]float32) error
}
```

#### 3. OpenAI adapter: Embed
**File**: `internal/kb/adapter_openai.go`
**Action**: modify — implement `Embed` via `o.client.Embeddings.New(ctx, ...)` with
`o.embedModel`; return `[][]float32` aligned with input order. Wrap errors
`fmt.Errorf("openai embed: %w", err)`.

#### 4. Vector repo
**File**: `internal/kb/repo_vector.go`
**Action**: create — implements `VectorStore`, reads/writes
`.kb/faiss_index/metadata.json`.

```go
type VectorRepo struct {
	indexDir string // ".kb"
}

func NewVectorRepo(indexDir string) *VectorRepo { return &VectorRepo{indexDir: indexDir} }
```

**File**: `internal/kb/dto_internal.go` — add:

```go
type vectorMetaJSON struct {
	Model   string               `json:"model"`
	Vectors map[string][]float32 `json:"vectors"` // file#anchor → embedding
}
```

`Save` writes `.kb/faiss_index/metadata.json` (`os.MkdirAll(.kb/faiss_index)`); `Load`
returns an empty map + no error if the file is absent (vector index optional — the router
only uses it when present).

#### 5. Service: embed at index time + vector branch
**File**: `internal/kb/service.go`
**Action**: modify

`NewService` gains `embedder Embedder, vectors VectorStore`. `Index` additionally:

```go
// after Save(sections):
texts := bodiesOf(secs) // []string of section bodies
embs, err := s.embedder.Embed(ctx, texts)
if err != nil {
	return 0, 0, fmt.Errorf("embed sections: %w", err)
}
vecMap := map[string][]float32{}
for i, sec := range secs {
	vecMap[sec.Citation()] = embs[i]
}
if err := s.vectors.Save(ctx, "text-embedding-3-small", vecMap); err != nil {
	return 0, 0, fmt.Errorf("save vectors: %w", err)
}
s.vecMap = vecMap
```

`LoadOnStartup` also loads vectors (`s.vectors.Load`). `Chat` replaces the P3 weak-branch
stub with the real vector path:

```go
// weak BM25 (≥ minThreshold, < strongThreshold) AND vectors present → vector RAG
if len(s.vecMap) > 0 {
	qEmb, err := s.embedder.Embed(ctx, []string{contextualQuery}) // contextualQuery=query in P4
	if err != nil {
		return Answer{}, sessionID, fmt.Errorf("embed query: %w", err)
	}
	secs := s.topByCosine(qEmb[0], topK) // rank s.indexed by Cosine vs s.vecMap[citation]
	if len(secs) == 0 { // safety
		return s.cannotConfirm(), sessionID, nil
	}
	text, err := s.llm.Answer(ctx, query, secs, nil)
	if err != nil {
		return Answer{}, sessionID, fmt.Errorf("llm answer: %w", err)
	}
	return NewAnswer(text, citationsFor(secs), "vector"), sessionID, nil
}
// no vectors → fall back to markdown over top BM25 sections
```

Add `topByCosine(queryVec []float32, k int) []Section` ranking `s.indexed` by
`Cosine(queryVec, s.vecMap[section.Citation()])`.

#### 6. cmd/kb: inject embedder + vector repo
**File**: `cmd/kb/main.go`
**Action**: modify

```go
vecRepo := kb.NewVectorRepo(".kb")
oai := kb.NewOpenAIClient(cfg.OpenAI.APIKey) // implements both LLM and Embedder
svc := kb.NewService(repo, oai, oai, vecRepo)
```

### Verification
#### Automated
- [x] `make test` passes; `Cosine` unit test (orthogonal→0, identical→1, mismatched
      length→0). Service test with `fakeEmbedder` (deterministic vectors): a weak-BM25
      query routes to vector and `topByCosine` returns the section whose stored vector is
      nearest → `strategy:"vector"`.
- [x] `make lint` passes.

#### Manual
- [x] `POST /index` writes `.kb/faiss_index/metadata.json` (valid JSON, `model` +
      `vectors` map keyed by `file#anchor`).
- [x] A vague in-scope paraphrase (e.g. "I'm unhappy with my purchase, can I get money
      back?") reports `strategy:"vector"` and still cites the refund section.

---

## Phase 5: Multi-turn memory

`SessionStore` holds last N=5 turns per `session_id` (idle TTL 30min). Memory composes
the retrieval query (deterministic string concat) and is passed as prior dialogue to the
LLM — grounding unchanged.

### Changes

#### 1. Domain: Turn (already declared in P3)
**File**: `internal/kb/domain.go`
**Action**: confirm `type Turn struct{ Query, Answer string }` exists (added in P3).

#### 2. Port: SessionStore
**File**: `internal/kb/ports.go`
**Action**: modify — add:

```go
type SessionStore interface {
	Get(ctx context.Context, id string) []Turn
	Append(ctx context.Context, id string, t Turn)
}
```

#### 3. In-process store
**File**: `internal/kb/memory_inproc.go`
**Action**: create

```go
package kb

// InProcStore is a per-session ring of the last maxTurns turns, evicted after idleTTL.
// ponytail: in-memory map + mutex, swap for Redis behind SessionStore when multi-instance.
type InProcStore struct {
	mu       sync.Mutex
	sessions map[string]*sessionEntry
	maxTurns int
	idleTTL  time.Duration
	now      func() time.Time
}

type sessionEntry struct {
	turns    []Turn
	lastSeen time.Time
}

func NewInProcStore() *InProcStore {
	return &InProcStore{
		sessions: map[string]*sessionEntry{},
		maxTurns: 5,
		idleTTL:  30 * time.Minute,
		now:      time.Now,
	}
}

func (s *InProcStore) Get(ctx context.Context, id string) []Turn { /* sweep expired, return copy */ }
func (s *InProcStore) Append(ctx context.Context, id string, t Turn) { /* append, trim to maxTurns, stamp lastSeen */ }
```

Expiry is lazy: on each `Get`/`Append`, drop entries whose `lastSeen` is older than
`idleTTL`. `ponytail: lazy sweep on access, add a background ticker only if idle sessions
pile up.`

#### 4. Service: wire memory into Chat
**File**: `internal/kb/service.go`
**Action**: modify

`NewService` gains `sessions SessionStore`. `Chat`:

```go
func (s *Service) Chat(ctx context.Context, query, sessionID string) (Answer, string, error) {
	if strings.TrimSpace(query) == "" {
		return Answer{}, sessionID, ErrEmptyQuery
	}
	if sessionID == "" {
		sessionID = uuid.NewString() // new thread
	}
	if !s.ready {
		return Answer{}, sessionID, ErrNotIndexed
	}

	history := s.sessions.Get(ctx, sessionID)
	contextualQuery := composeQuery(history, query) // deterministic string concat

	// routing uses tokenize(contextualQuery) for BM25 and contextualQuery for embeddings;
	// LLM.Answer receives `query` (the raw question) + `history` as prior dialogue.
	// ... markdown / vector / cannot-confirm as before, but pass `history` to llm.Answer ...

	// on success, before returning:
	s.sessions.Append(ctx, sessionID, Turn{Query: query, Answer: ans.Text()})
	return ans, sessionID, nil
}

// composeQuery prepends recent user turns so follow-ups resolve against the prior topic.
func composeQuery(history []Turn, query string) string {
	if len(history) == 0 {
		return query
	}
	var b strings.Builder
	for _, t := range history {
		b.WriteString(t.Query)
		b.WriteByte(' ')
	}
	b.WriteString(query)
	return b.String()
}
```

> Use `tokenize(contextualQuery)` for BM25 ranking and embed `contextualQuery` for the
> vector branch (both retrieval steps see context). The LLM still answers only from
> retrieved sections; `history` is supplied as prior dialogue, never as a grounding source
> (spec §6 — memory shapes retrieval, sources control the answer). Append the turn for
> **every** non-error outcome including cannot-confirm.

`uuid` import: `github.com/google/uuid` (already in `go.mod`).

#### 5. cmd/kb: inject session store
**File**: `cmd/kb/main.go`
**Action**: modify

```go
sessions := kb.NewInProcStore()
svc := kb.NewService(repo, oai, oai, vecRepo, sessions)
```

#### 6. dto_http: ensure session_id echoed
**File**: `internal/kb/dto_http.go`
**Action**: confirm `ChatResponse.SessionID` is populated from the returned `sessionID`
(already wired in P3 via `toChatResponse(ans, sid)`).

### Verification
#### Automated
- [x] `make test` passes; multi-turn test: a `fakeSectionStore` with refund sections, a
      `fakeLLM` that records the sections it received. First query "How long do refunds
      take?" then same-session "And which items can't be refunded?" → the second call's
      `composeQuery` causes BM25 to rank `non-refundable-items` top → asserts the fake LLM
      received that section / citation is `refund_policy.md#non-refundable-items`.
- [x] Test: omitted `session_id` → response returns a non-empty generated `session_id`.
- [x] `make lint` passes.

#### Manual
- [x] "How long do refunds take?" then (same `session_id`) "And which items can't be
      refunded?" → second answer cites `refund_policy.md#non-refundable-items`.
- [x] Full acceptance criteria 1–4 (spec §9) pass end-to-end.

---

## Testing Checkpoints (from structure.md)

- **After P1**: package compiles, lints; `cmd/kb` boots with no DB; `/health`→200.
- **After P2**: indexing produces `.kb/index.json`; reload-on-startup works; BM25/slug unit-tested.
- **After P3**: grounded single-turn answers + citations + cannot-confirm; `strategy:"markdown"`;
  error mapping correct. *Independently shippable core loop.*
- **After P4**: `strategy:"vector"` observable; `.kb/faiss_index/metadata.json` present.
- **After P5**: full acceptance criteria 1–4 met, incl. multi-turn.

## Calibration note

`strongThreshold`/`minThreshold` (and the P3/P4 values `6.0`/`2.0` above) are **empirical
placeholders**. After P2 produces real `.kb/index.json`, run the three in-scope queries and
the out-of-scope "restaurants" query, observe actual top BM25 scores, and tune the
constants so: in-scope sharp queries score ≥ `strongThreshold` (markdown), a vague in-scope
paraphrase lands between the thresholds (vector), and "restaurants" falls below
`minThreshold` (cannot-confirm). This is design Open Risks #2–3; expect iteration in P3/P4.
```


