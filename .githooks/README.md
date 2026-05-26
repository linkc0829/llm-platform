# Git hooks

This directory holds the project's git hooks. They live in-repo so they're versioned and discoverable, rather than living invisibly inside `.git/hooks/`.

## Install

```bash
make hooks-install
```

This is equivalent to `git config core.hooksPath .githooks` — it tells your local clone to look here for hooks.

## Hooks

- `pre-commit` — runs `gofmt` check → `sqlc generate` (if sql/queries changed) → `golangci-lint` → `go test -short`. About 5–15 seconds depending on cache. Skip once with `git commit --no-verify` if you have a good reason.

## Why not [husky](https://typicode.github.io/husky/) / [pre-commit](https://pre-commit.com/) / [lefthook](https://github.com/evilmartians/lefthook)?

This is a Go project with no Node or Python toolchain. A bash script + `git config` keeps the dependency surface to: bash, git, and the tools the project already requires (sqlc, golangci-lint, go). Add a framework if/when the hook set grows beyond what a single script handles cleanly.
