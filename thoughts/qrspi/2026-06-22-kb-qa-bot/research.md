# Research Findings

Scope: existing template feature slices (`order`, `user`, `payment`), composition root
(`internal/bootstrap`), entrypoint (`cmd/api`), and platform utilities. Facts only.

## Q1: Full request flow for one feature (`internal/order`)

### Findings
File-by-file, request enters at the bottom of `order` and flows down through ports:

- `routes.go:10` — `RegisterRoutes(rg *gin.RouterGroup, h *Handler, authMW *auth.Manager)`. Groups under `/orders`, applies `auth.Middleware(authMW)` to the whole group, maps 5 verbs to handler methods (`routes.go:14-18`). Contains only routing; no logic.
- `handler_http.go` — defines a **local** inbound `service interface` (`handler_http.go:15-21`), `Handler{svc service}`, and `NewHandler(svc *Service)` (concrete in, interface field). Each handler: extract user via `mustUserID` (`:170`), bind/parse, `context.WithTimeout` (3–10s, per-endpoint), call service, map result with `toOrderResponse`, or `writeError`. `writeError` (`:184-199`) is the domain→HTTP switch. No business logic.
- `service.go` — `Service{repo, users, payment}` (`:10`), `NewService` takes 3 interfaces (`:16`). Use-case orchestration: `Create` checks user exists → `shared.NewMoney` → `NewOrder` → `repo.Save` (`:29-52`). `Pay` (`:57`) loads order, ownership check returns `ErrOrderNotFound` to avoid leaking existence (`:63`), charges, `o.MarkPaid()`, `repo.Update`. Wraps errors with `fmt.Errorf("...: %w", err)`. No SQL, no HTTP.
- `ports.go` — outbound `Repo` interface (`:14`) plus cross-feature capability ports `UserLookup` (`:31`) and `PaymentCharger` (`:38`), named by capability not provider. `//go:generate mockgen` directive at `:13` (mocks not present in tree).
- `repo_postgres.go` — `PostgresRepo{q *sqlc.Queries}` (`:22`), `NewPostgresRepo(pool *pgxpool.Pool)` (`:26`). Each method calls sqlc queries, maps via `dto_internal.go` helpers, translates `pgx.ErrNoRows`→`ErrOrderNotFound` (`:51`), `rows==0`→`ErrOrderNotFound` (`:43`). No business rules.
- Supporting: `domain.go` (entity), `dto_http.go` (request/response + `toOrderResponse`), `dto_internal.go` (sqlc↔domain conversions), `errors.go` (sentinels).

Flow: `routes.go → handler_http.go → service.go → ports.go(Repo) → repo_postgres.go → sqlc`.

## Q2: Feature wiring & registration (`bootstrap`, `cmd/api`)

### Findings
- `cmd/api/main.go` is thin: `config.Load()` (`:16`) → `signal.NotifyContext` (`:21`) → `bootstrap.NewApp` (`:24`) → run server in goroutine (`:31`) → wait on signal/error → `app.Shutdown` with `cfg.App.ShutdownTimeout` (`:47-50`).
- `bootstrap/app.go:44` `NewApp` constructs in order: logger → otel → postgres pool → redis → auth manager → gin engine (`httpserver.New`) → metrics → health/metrics routes (`:96-97`, no auth) → `wireFeatures` (`:101`) → `httpserver.Wrap` (`:104`). Returns `*App` holding every resource.
- `bootstrap/wire.go:24` `wireFeatures` is documented as the **only** place importing >1 feature package (`:20-23`). Mounts everything under `engine.Group("/api/v1")` (`:31`). Per feature: build repo → adapters/hasher/gateway → `NewService` → `NewHandler` → `RegisterRoutes(api, handler, authMgr)` (user `:36-40`, payment `:48-52`, order `:65-69`).
- Cross-feature injection: `payment.Service.Charge` structurally satisfies `order.PaymentCharger`, injected directly (`wire.go:67`). `user.Service.GetByID` returns `*User` but `order.UserLookup` wants `bool`, so a bootstrap-only `userLookupAdapter` (`wire.go:78-91`) bridges, mapping `user.ErrUserNotFound`→`(false,nil)`.
- `redis.Client` and `*zap.Logger` are passed to `wireFeatures` but currently unused (`wire.go:27,29` — `_` params, "reserved for cache adapters").
- `bootstrap/shutdown.go:13` `Shutdown` closes in reverse order (server → otel → redis → pool → logger.Sync), never returns early, logs each error.
- Auth context key: middleware sets `"auth.userID"` = JWT subject (`auth/middleware.go:30`); handlers read via `auth.UserIDFromContext` (`:37`).

## Q3: Configuration loading (`internal/platform/config`)

### Findings
- `config.go:12` `Config` is nested structs: App, HTTP, DB, Redis, JWT, OTel, Logger, all with `mapstructure` tags.
- `Load()` (`:63`) uses `viper`. Sets defaults (`:67-81`), `SetEnvKeyReplacer(".", "_")` + `AutomaticEnv` (`:84-85`), then **manual `BindEnv`** for every key (`:89-111`) because nested-key auto-binding is unreliable for unset envs. Env names are custom, not derived: e.g. `http.port`←`APP_PORT`, `db.dsn`←`POSTGRES_DSN`, `db.max_conns`←`POSTGRES_MAX_CONNS` (`:90-107`).
- Optional `.env` read via viper config type `env` from `.` (`:114-117`), error ignored if missing.
- Validation (`validate()`, `:130-138`) requires only two fields: `DB.DSN` (`POSTGRES_DSN`) and `JWT.Secret` (`JWT_SECRET`); both empty→error. Everything else has a default.
- Consumed by `main.go:16` and passed into `bootstrap.NewApp(ctx, cfg)`; sub-configs map directly to platform `Config` structs (`app.go:46-89`).

## Q4: Testing patterns

### Findings
- **Hand-written fakes, not gomock.** `ports.go` has `//go:generate mockgen` directives (`order/ports.go:13`, `payment/ports.go:11`) but no `mocks/` dir exists; tests define their own structs.
- Service tests: `order/service_test.go` defines `fakeRepo`/`fakeUserLookup`/`fakeCharger` (`:18-62`) implementing the ports, with settable result/err fields and call counters. Table-driven, `snake_case` case names (`:78,83,89`), `testify` `assert`/`require`, `errors.Is` for sentinel assertions (`:111`). Covers happy/validation/port-error/ownership/domain-transition cases. Domain transitions tested directly (`:196-217`).
- Handler tests: `order/handler_http_test.go` defines a `mockSvc` (`:22-46`) satisfying the local `service` interface, builds a real `gin` engine via `newTestRouter` (`:57`) with a `withAuth` middleware that sets the same `"auth.userID"` key (`:50-55`), drives with `httptest` (`:84-88`), asserts status + JSON shape (`:90-96`).
- Integration: `test/integration/order_repo_test.go` is build-tagged `//go:build integration` (`:1`), package `integration`, currently a **skeleton stub** (`:38-60`) that only asserts `true`. DSN from `POSTGRES_TEST_DSN` with documented localhost fallback (`:14-15`). `make test-integration` / `-tags=integration`.
- `test/contract/` holds cross-feature contract tests + `stubs_test.go` (not read in detail).

## Q5: Error mapping & error infrastructure

### Findings
- Each feature's `errors.go` declares sentinel `errors.New` values: `order/errors.go:5-12` (`ErrOrderNotFound`, `ErrInvalidUserID`, `ErrInvalidAmount`, `ErrInvalidStatusTransition`, `ErrPaymentFailed`, `ErrUserNotFound`). Naming follows `ErrXxxNotFound`/`ErrXxxInvalid` convention loosely.
- Mapping lives in each handler's `writeError` via `errors.Is` switch: `order/handler_http.go:184-199`, `user/handler_http.go:106-121`. Maps to status codes (NotFound, BadRequest, Conflict, Unauthorized, PaymentRequired) with a `default` → 500 "internal error".
- `internal/platform/httperr/httperr.go` provides a canonical envelope `{"error","code?"}` (`:38-41`) and helpers (`BadRequest`, `NotFound`, `Conflict`, `PaymentRequired`, `Internal`, etc., `:54-60`) that call `c.AbortWithStatusJSON`. Package doc states domain→status mapping must stay in feature handlers (`:4-7`).
- **Observed inconsistency:** despite `httperr` existing, current handlers write errors with raw `c.JSON(..., gin.H{"error": ...})` (`order/handler_http.go:187`, `user/handler_http.go:109`) and do **not** use the `httperr` helpers. `auth/middleware.go:21,27` also uses raw `gin.H`.

## Q6: Outbound ports & adapters; timeouts & context

### Findings
- Ports defined per feature in `ports.go` as interfaces taking `ctx context.Context` first: `order.Repo` (`:14`), `payment.Repo` + `payment.Gateway` (external port, `:22`). Cross-feature ports also live here (Q1).
- Postgres adapters: `*PostgresRepo` wraps `*sqlc.Queries` built from `*pgxpool.Pool` (`order/repo_postgres.go:22-28`). All SQL via sqlc; conversions in `dto_internal.go` using `postgres.UUIDToPg`/`PgToUUID`/`TimeToPg` helpers (`order/dto_internal.go:14-47`).
- External-call adapter: `payment.StubGateway` implements `Gateway`, dev-only always-approves (`gateway_stub.go:18-23`); CLAUDE-style note says add `gateway_stripe.go` rather than edit the stub (`:12-13`).
- Redis: a `redis.Client` is constructed (`redis/redis.go:19`) and wired but **no cache adapter exists yet**; `wireFeatures` marks it reserved (`wire.go:27`).
- **Timeout placement:** request timeouts are applied at the **handler**, not the repo — `context.WithTimeout(c.Request.Context(), N)` per endpoint (order: 3/5/10s; user: 3/5s). Platform connection setup applies its own 5s timeouts on `Ping` (`postgres.go:38`, `redis.go:26`). Repos/services propagate the incoming `ctx` without adding timeouts. R3.2 ("all external calls have a timeout") is satisfied via the handler-level context.

## Q7: Domain layer (`domain.go`, `internal/shared`)

### Findings
- `order/domain.go`: `Order` aggregate with all-private fields (`:20-27`), `Status` string enum (`:13-17`). `NewOrder` constructor validates invariants (non-zero user, non-zero amount) and stamps UTC timestamps (`:31-47`). Unexported `rehydrate` for trusted repo reconstruction without re-validation (`:50-56`). State transitions are domain methods enforcing invariants (`MarkPaid` `:60`, `Cancel` `:69`), returning `ErrInvalidStatusTransition`. Getters only; no setters; no `context`/IO imports. Same pattern in `payment/domain.go`, `user/domain.go` (constructors + `MarkSucceeded`/`MarkFailed`).
- `internal/shared` value objects (zero-dependency kernel, doc rule `ids.go:3-5`):
  - `Money` (`money.go:12`): private `amount int64` (smallest unit) + `currency`, `NewMoney` rejects negative/empty (`:25-33`), `MustNewMoney` panics for tests (`:36`), immutable `Add`/`Subtract` with currency-mismatch guard, float storage forbidden by doc.
  - Strongly-typed IDs `UserID`/`OrderID`/`PaymentID` as `type X uuid.UUID` (`ids.go:27-29`), each with `New*`, `String`, `IsZero`, text (JSON) marshalling, `sql.Scanner`/`driver.Valuer`, and `Parse*` returning `ErrInvalidID`.
  - `Pagination` (`pagination.go:5`) with `NewPagination` clamping (default 20, max 100, `:16-27`) and generic `Page[T]` response wrapper (`:30-35`), aliased as `order.ListOrdersResponse` (`dto_http.go:35`).

## Cross-Cutting Observations

- **Strict layering matches CLAUDE.md R1–R3:** services take only interfaces (`order/service.go:16`, `user/service.go:19` comment cites R3.4), domain has no IO, repos hold no business rules, error wrapping uses `%w`.
- **Dual inbound interface pattern:** handler defines a private `service` interface for mocking (`handler_http.go:15`), while `bootstrap` injects the concrete `*Service`. `NewHandler(svc *Service)` takes concrete but stores as interface.
- **Capability ports named by capability:** `UserLookup`, `PaymentCharger`, `Gateway` — provider-agnostic, satisfied structurally; bootstrap is the only coupling point.
- **Per-endpoint context timeouts** are the consistent convention (no shared middleware timeout).
- **Template scaffolding present:** `scripts/new-feature/main.go`, mockgen directives without generated mocks, integration test stub — all indicate an unstarted template.

## Open Areas

- `test/contract/cross_feature_test.go` and `stubs_test.go` were not read in detail (referenced for completeness).
- Generated `mocks/` directories do not exist despite `//go:generate mockgen` directives — whether mocks are expected to be generated is not determinable from code.
- Redis cache port/adapter is wired but unimplemented; no `cache_redis.go` exists in any feature.
- `httperr` helpers exist but are unused by current handlers — intent (future migration vs. dead code) not determinable from code.
