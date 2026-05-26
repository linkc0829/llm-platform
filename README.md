# go-backend-template

Hexagonal Architecture Go backend template, designed for **AI vibe-coding** and
**system-design demos**. Optimized for solo developers building portfolio
projects targeting backend roles at international software companies and
crypto/web3 firms.

## Highlights

- **Hexagonal Architecture + feature-first package layout** — minimum AI
  context-switch cost, max cross-feature isolation. See
  [`docs/adr/0001`](docs/adr/0001-hexagonal-feature-first.md).
- **`CLAUDE.md`** at repo root: 24 numbered rules + anti-pattern examples for AI tools.
- **`depguard`** in `.golangci.yml` enforces architectural boundaries — wrong
  imports fail lint.
- **Realistic feature set**: user (JWT auth), order (lifecycle), payment
  (gateway port). Cross-feature ports demonstrated end-to-end.
- **Production-shaped infra**: pgx + sqlc, Redis, zap, OpenTelemetry,
  golang-migrate.

## Quick start

```bash
# 1. Start local Postgres (port 5432) and Redis (port 6379).
#    Defaults expect user=postgres / password=postgres / database=app.
#    See .env.example to override DSNs.

# 2. Apply migrations (requires golang-migrate CLI)
make migrate-up

# 3. Set required env (or copy .env.example to .env)
cp .env.example .env
# edit JWT_SECRET at minimum

# 4. Run the API
make run

# 5. Smoke test
curl -X POST http://localhost:8080/api/v1/auth/register \
  -H 'Content-Type: application/json' \
  -d '{"email":"a@b.co","password":"pa55word!","display_name":"A"}'
```

## Using this template

This repo is a starting point, not a library. When you fork it for a new project, follow these rules:

1. **Edit migration `0001` directly — don't stack new migrations on top.** Until you've shipped the first version, schema is not yet history. Modify `migrations/0001_*.up.sql` / `*.down.sql` in place to match your real domain. Only start creating `0002`, `0003`, … after the first real deploy.
2. **Copy `.env.example` to `.env` yourself** and fill in real values (`JWT_SECRET` is mandatory; DSNs only if you deviate from defaults). The template will not auto-create `.env` — it's intentionally in `.gitignore`.
3. **Delete template code you don't need.** The shipped features (`user`, `order`, `payment`) are demos. If your project has no payments, fully remove `internal/payment/` along with:
   - its block in `internal/bootstrap/wire.go`
   - the `PaymentCharger` port wiring in `internal/order/`
   - `sql/queries/payment.sql` and the payment tables in migration `0001`
   - the `payment` paths in `api/openapi.yaml`
   - the `no-cross-feature-payment` depguard block in `.golangci.yml`
   - any payment-related entries in `.env.example`

   Same rule for any other slice you don't use. `make lint && make test` after each removal catches dangling references.

## Directory layout

```
cmd/api/main.go              # 30-line entrypoint
internal/
  order/  payment/  user/    # feature slices (the hexagons)
  shared/                    # zero-dependency value objects (Money, IDs, …)
  bootstrap/                 # composition root — wires features together
  platform/                  # infrastructure (pgx, redis, jwt, otel, …)
migrations/                  # golang-migrate
sql/queries/                 # sqlc input
api/openapi.yaml             # API contract (source of truth)
docs/adr/                    # architecture decision records
test/integration/            # integration tests against local Postgres
CLAUDE.md                    # AI rules
.golangci.yml                # lint + depguard
sqlc.yaml
Makefile
```

## Make targets

```
make run                  # run api locally (requires local postgres+redis)
make build                # build ./bin/api
make test                 # unit tests
make test-integration     # integration tests (requires local postgres)
make test-cover           # html coverage report
make lint                 # golangci-lint
make sqlc-generate        # regenerate sqlc code
make migrate-up           # apply migrations
make migrate-create NAME=add_xxx
```

## Adding a new feature

1. Read [`CLAUDE.md`](CLAUDE.md) — section "R4. New-Feature Checklist".
2. Create `internal/<feature>/` with the standard 11-file layout.
3. Add SQL to `sql/queries/<feature>.sql` + migrations.
4. Wire it in `internal/bootstrap/wire.go`.
5. Update `api/openapi.yaml`.
6. `make lint && make test` — depguard will catch architecture violations.

## Cross-feature dependencies

Features must not import each other. Pattern (full example in `internal/order/ports.go`):

```go
// internal/order/ports.go — order declares what it needs
type PaymentCharger interface {
    Charge(ctx context.Context, userID shared.UserID, orderID shared.OrderID, amount shared.Money) error
}
```

`payment.Service.Charge(...)` structurally satisfies that interface.
`internal/bootstrap/wire.go` injects it. Order has zero knowledge of payment.

## Roadmap (not built in)

The template intentionally ships small. Recommended next moves for system-design demos:

- **Idempotency-Key middleware** under `internal/platform/idempotency/`
- **Outbox pattern** in `order` — add `outbox.go`, a `cmd/worker/main.go`, and a poller
- **CQRS** — split `service.go` into `command_service.go` + `query_service.go`
- **Circuit breaker** — wrap the `payment.Gateway` port with `sony/gobreaker`
- **Rate limiting** — token-bucket middleware in `internal/platform/httpserver/`
- **Saga** — multi-feature orchestration under `internal/saga/`

Each fits the existing structure with no architectural changes.
