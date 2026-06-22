# Research Questions

## Context
Focus on the existing feature packages under `internal/` (`order`, `user`, `payment`),
the composition root in `internal/bootstrap/`, the entrypoint in `cmd/api/`, and the
infrastructure utilities in `internal/platform/` (config, httpserver, httperr, logger).
The goal is to document existing conventions, wiring, and test patterns.

## Questions
1. For one existing feature package (e.g. `internal/order`), trace the full request flow file
   by file — how do `routes.go`, `handler_http.go`, `service.go`, `ports.go`, and
   `repo_postgres.go` connect, and what does each file contain versus exclude?
2. How are features wired together and registered in `internal/bootstrap/` and `cmd/api/`,
   including how routes are mounted on the gin engine and how cross-feature ports are injected?
3. How does configuration loading work in `internal/platform/config/` — how are environment
   variables read, what does validation require, and how do entrypoints consume the config?
4. What are the established testing patterns in the feature packages — does the repo use gomock
   or hand-written fakes, how are service and handler tests structured, and what build tags or
   directories separate integration tests?
5. How do handlers map domain/sentinel errors to HTTP responses, and what does
   `internal/platform/httperr/` and the error files (`errors.go`) provide?
6. How are outbound dependencies (Postgres repos, Redis cache, external calls) defined as ports
   and implemented as adapters, and where do timeouts and `context.Context` get applied?
7. What does the domain layer look like in existing features — how are entities, value objects,
   constructors, and pure functions structured in `domain.go` and `internal/shared/`?
