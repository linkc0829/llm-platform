---
name: new-feature
description: Scaffold and wire up a new feature in this hexagonal Go backend (e.g. "add an inventory feature", "create a new shipments package", "implement the notifications module"). Walks through the 11-file per-feature structure, sqlc/migration entries, bootstrap wiring, and OpenAPI registration. Use when the user wants to add a new feature package under internal/.
---

# Adding a new feature

This repo uses hexagonal architecture with a feature-first layout. Each feature is a single Go package with **11 files**. The fastest path is to copy `internal/order/` as a scaffold, then rename and gut the bodies.

## Step 1 — Confirm scope with the user

Before scaffolding, confirm:
- Feature name (snake_case package, e.g. `inventory`)
- Endpoints (method + path), or point me at the OpenAPI section
- Whether it needs cross-feature ports (does it call `user`, `order`, `payment`?)
- Whether it needs Redis cache or just Postgres

## Step 2 — Scaffold the package

**Fast path:** `make new-feature name=<feature>` copies `internal/order/` to `internal/<feature>/`, rewrites identifiers (`Order` → `<Feature>`, `OrderID` → `<Feature>ID`, etc.), writes a stub `sql/queries/<feature>.sql`, and prefixes each `.go` file with a TODO banner. You still need to gut the bodies.

**Manual path:** copy `internal/order/` to `internal/<feature>/` yourself and rename. Create these 11 files (rename types, gut bodies):

- [ ] `internal/<feature>/domain.go` — entities, value objects, `New<Entity>` constructors (R3.7)
- [ ] `internal/<feature>/errors.go` — `ErrXxxNotFound` / `ErrXxxInvalid` sentinels
- [ ] `internal/<feature>/ports.go` — outbound interfaces (repo, cache, cross-feature)
- [ ] `internal/<feature>/service.go` — use-case orchestration, all methods `ctx context.Context` first
- [ ] `internal/<feature>/dto_http.go` — request/response structs + `to<Entity>` / `from<Entity>` mappers
- [ ] `internal/<feature>/dto_internal.go` — conversion helpers between domain entities and sqlc-generated row types (see `orderFromSqlc` / `orderToInsertParams` for the pattern; uses `internal/platform/postgres.UUIDToPg` / `TimeToPg`)
- [ ] `internal/<feature>/handler_http.go` — parse → validate → service → map → respond
- [ ] `internal/<feature>/routes.go` — `RegisterRoutes(rg *gin.RouterGroup, h *Handler)`
- [ ] `internal/<feature>/repo_postgres.go` — implements repo port; holds `*sqlc.Queries`, calls generated methods, maps via dto_internal.go. No hand-written SQL (R3.5).
- [ ] `internal/<feature>/service_test.go` — table-driven, mocked ports
- [ ] `internal/<feature>/handler_http_test.go` — `httptest` + mocked service

Optional, only when needed: `cache_redis.go`, `pub_kafka.go`, etc.

## Step 3 — SQL + migrations

- [ ] `sql/queries/<feature>.sql` — sqlc queries (`-- name: GetX :one` etc.). **Use `sqlc.arg(<column_name>)` for every parameter**, not positional `$N` — see `sql/queries/order.sql` for the canonical pattern. Keeping arg names = column names guarantees predictable Go field names (`sqlc.arg(user_id)` → `UserID`).
- [ ] `migrations/NNNN_create_<feature>.up.sql`
- [ ] `migrations/NNNN_create_<feature>.down.sql`
- [ ] Run `make sqlc-generate` to regenerate `internal/platform/postgres/sqlc/`

Use `make migrate-create name=create_<feature>` to generate migration file pairs with the right number.

## Step 4 — Wire it up

- [ ] Edit `internal/bootstrap/wire.go`:
  - Construct repo: `<feature>Repo := <feature>.NewPostgresRepo(pool)`
  - Construct service: `<feature>Svc := <feature>.NewService(<feature>Repo, ...)`
  - Construct handler: `<feature>Handler := <feature>.NewHandler(<feature>Svc)`
  - Call `<feature>.RegisterRoutes(apiGroup, <feature>Handler)`
- [ ] If cross-feature: define the capability port in the **caller's** `ports.go`, then add an adapter or direct injection in `wire.go`. See `userLookupAdapter` for reference.
- [ ] **Add a depguard block** in `.golangci.yml`: copy the `no-cross-feature-order` block, rename to `no-cross-feature-<name>`, and list the sibling features your new feature must not import. The `domain` / `service` / `handler` rules are glob-based and pick up the new feature automatically; cross-feature isolation is not.

## Step 5 — OpenAPI

- [ ] Add tag + endpoints to `api/openapi.yaml`
- [ ] Ensure request/response schemas match `dto_http.go` structs

## Step 6 — Mocks

- [ ] Add `//go:generate mockgen -source=ports.go -destination=mocks/mocks.go -package=mocks` to top of `ports.go` (or follow whatever convention exists in `internal/order/ports.go`)
- [ ] Run `make mock-gen`

## Step 7 — Verify

```bash
make sqlc-generate
make mock-gen
make lint     # depguard will catch R1.* violations
make test
make migrate-up   # apply migration locally
```

If `make lint` fails on depguard: the rules are glob-based (`internal/*/domain.go` etc.) so a new feature should be auto-enforced. If you see a violation, the rule is right — fix the code, don't loosen the rule.

## Reference: feature to copy from

`internal/order/` shows the full pattern including cross-feature ports (`PaymentCharger`, `UserLookup`). `internal/user/` shows JWT + bcrypt adapter. `internal/payment/` shows a stub external-gateway adapter.

## Common gotchas

- **Don't import another feature package.** Define a port in your own `ports.go` and inject from `bootstrap/wire.go`. (R1.4)
- **Don't return domain entities from handlers.** Map via `to<Entity>Response` in `dto_http.go`.
- **Don't put business rules in the handler or repo.** They live in `domain.go` or `service.go`.
- **Don't accept `*pgxpool.Pool` in the service constructor.** Only interfaces. (R3.4)
