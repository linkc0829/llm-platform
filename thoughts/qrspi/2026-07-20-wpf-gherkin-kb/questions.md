# Research Questions

## Context

Focus on the `internal/kb` package (its `domain.go`, `service.go`, `ports.go`,
`repo_markdown.go`, `repo_vector.go`, `dto_internal.go`, adapters, and tests),
plus `internal/platform/config` and the `cmd/kb` composition root. Also look at
`.golangci.yml` (depguard), the `Makefile`, and the on-disk index artifacts
under `.kb/`.

## Questions

1. Trace `tokenize` and `slugify` in `internal/kb/domain.go` — what exactly is
   the character-classification logic in each, what does each emit for
   non-ASCII input, and which other functions and persisted fields consume
   their output?

2. Trace a request through `Service.Chat` end to end: what retrieval steps run
   in what order, which thresholds/constants gate each branch, under what
   conditions is a refusal returned instead of an answer, and how is the
   `strategy` value on the response decided?

3. How does `MarkdownRepo.Parse` discover and read source files, and how does
   it split a file's contents into `Section`s — what is included, what is
   dropped, and what fields does a `Section` carry?

4. What is persisted to disk by the index and vector save/load paths — the
   exact JSON shapes in `dto_internal.go`, what `LoadOnStartup` validates
   before accepting a snapshot, and where the embedding model identifier is
   written and compared?

5. What outbound interfaces exist in `internal/kb/ports.go`, which concrete
   types satisfy each, how are they selected and wired in `cmd/kb/main.go`,
   and how does the OpenAI adapter construct its client and choose model
   names?

6. How is configuration loaded and validated in `internal/platform/config` —
   which environment variables are bound, what defaults exist, what causes a
   load to fail, and how is `.env` reconciled with real environment variables?

7. What do the existing tests in `internal/kb` cover and in what style — which
   fakes/helpers exist, what naming and table-driven conventions are used —
   and what import restrictions does `.golangci.yml` place on `domain.go`,
   `service.go`, and `handler_*.go`?
