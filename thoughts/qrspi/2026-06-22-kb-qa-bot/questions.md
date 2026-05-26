# Research Questions

## Context
Focus on the conventions of a single existing feature package under `internal/`
(e.g. `internal/order/`), the composition root in `internal/bootstrap/`, the
entrypoints in `cmd/`, configuration loading in `internal/platform/config/`, the
shared/platform utility packages, the lint/depguard rules in `.golangci.yml`, and
the HTTP server + error-mapping plumbing in `internal/platform/`.

## Questions
1. Trace the full request lifecycle through an existing feature package
   (`internal/order/`): how do `handler_http.go`, `service.go`, `ports.go`, and the
   adapter files relate, what does each file contain, and how are domain errors
   mapped to HTTP responses?

2. How does `internal/bootstrap/wire.go` construct and connect a feature's
   service, handler, and routes, and how does an entrypoint in `cmd/` build config
   and start the HTTP server?

3. How does `internal/platform/config/config.go` load and validate configuration
   from environment variables, and what does it require versus treat as optional?

4. What outbound-port and adapter patterns exist for calls to external systems
   (e.g. databases, payment gateways, hashers) — how are interfaces declared in
   `ports.go` and implemented in adapter files, and how are timeouts/context applied?

5. How are unit tests structured for an existing feature — what do the hand-written
   fakes/stubs in `service_test.go`, `handler_http_test.go`, and `test/contract/`
   look like, and how do they mock ports and the service interface?

6. What do `.golangci.yml` depguard rules enforce per file type and per feature,
   and what files/sections must change when a new feature package is added (lint
   blocks, OpenAPI, bootstrap)?

7. What persistence and serialization patterns already exist (sqlc rows, DTO
   mapping in `dto_internal.go`, any on-disk/JSON or cache marshalling), and how is
   the `internal/shared/` value-object kernel structured?
