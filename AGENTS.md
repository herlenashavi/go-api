# AGENTS.md

Guidance for AI coding agents (Claude Code, Codex, Cursor, etc.) working in this repository.

## Commands

This is a single Go module for the whole repo (one `go.mod` at the root) — `examples/`, `cmd/`, and `pkg/` all build and test together, there is nothing to `cd` into first.

```shell
make build       # go build ./...
make test        # go test ./...
make test-race   # go test -race ./...
make vet fmt tidy  # go vet ./..., gofmt -l -w ., go mod tidy
```

Run a single test:
```shell
go test ./pkg/authz/... -run TestAuthorizer_RoleHierarchy -v
```

Run a worked example (no external dependencies needed):
```shell
make run                        # == cd examples/heroes && go run .
cd examples/villains && go run . # gRPC :9091, HTTP :8081 (health only)
```

Postgres-backed examples (optional; both default to an in-memory store without `DATABASE_URL`):
```shell
make heroes-migrate-up     # go run ./examples/heroes/migrate up — needs DATABASE_URL
make villains-migrate-up   # same, for examples/villains
```

Local Kubernetes loop via Tilt (needs `minikube start --container-runtime=docker` first — see docs/tilt-dev.md for why that flag matters):
```shell
make tilt-up tilt-down
make tilt-validate   # static validation, no cluster needed; needs kubeconform + hadolint
```

Scaffold a new service (see docs/scaffolding.md):
```shell
go run ./cmd/go-api new <name> -module <path> [-db] [-authz] [-authn none|hmac|jwt] [-crud <Name>]...
```

CI (`.github/workflows/ci.yml`) runs exactly `go vet`, a `gofmt -l` check, `go build`, then `go test -race` — mirror that locally before pushing rather than guessing what it checks.

Developer docs ([herlenashavi.github.io/go-api](https://herlenashavi.github.io/go-api/)) are built from `docs/*.md` + `mkdocs.yml` by `.github/workflows/docs.yml` on every push to `main` that touches `docs/` — there is no separate source to keep in sync, editing a file under `docs/` is the whole change:
```shell
pip install mkdocs-material
mkdocs serve           # live preview at localhost:8000
mkdocs build --strict  # what CI runs; catches a broken internal link
```

## Architecture

**`pkg/app.New` is the composition root.** It wires config, logging, the OTel telemetry pipeline, the default middleware chain (request ID → panic recovery → tracing → RED metrics → access log → timeout → body limit → CORS → security headers), health endpoints, and — if `Options.GRPCEnabled` — a gRPC server, into one `App` with one graceful-shutdown lifecycle. Read `pkg/app/app.go` first; most "how do these packages fit together" questions are answered by what it calls, in what order.

**Server kit + opt-in layers, not a monolith.** `config`/`logging`/`telemetry`/`problem`/`httpx`/`middleware`/`health` work standalone and compose via `app.New`. `crud`, `authz`, `dbx`, and `grpcx` are opt-in on top — a service can always drop to a raw chi handler instead of `crud.Resource`, and `app.Options.Verifier`/`Authorizer` are both nil-able. Nothing here is "the only way" to build a route; see the package doc comments, which spell this out explicitly (e.g. `pkg/crud`'s and `pkg/grpcx`'s).

**authn and authz are deliberately separate.** `authn.Verifier` (HMAC for dev, JWT/JWKS for production) establishes a `Principal` from a bearer token; `authz.Authorizer` (Cedar policies + a role hierarchy) decides what that principal can do. `crud.Resource` and the gRPC server wire authentication automatically from `app.Options`; authorization is applied explicitly per resource/handler (`crud.Config.Authorizer`, or a direct `Authorizer.Check`/`Authorize` call for hand-written routes needing row-level ABAC — see `examples/heroes/retire.go`).

**One OTel pipeline spans HTTP, gRPC, and the database.** `pkg/telemetry` builds a single `TracerProvider`/`MeterProvider` pair; HTTP middleware (`middleware.Tracing`/`Metrics`), the gRPC server/client (`otelgrpc` stats handler in `pkg/grpcx`), and DB queries (`dbx`'s `queryTracer`/`queryMetrics`) all consume the *same* instances rather than building their own, so a trace or metric from any layer lands in one pipeline. `pkg/logging`'s `Fielder` mechanism correlates log lines with `trace_id`/`request_id` without the logging package knowing telemetry or request-handling concepts exist — see `docs/observability.md` for the full picture and `docs/metrics.md` for adding your own instruments to the same `Meter`.

**Every log line passes through PII/secret redaction unconditionally** (`pkg/logging/pii.go`, built into the handler chain `logging.New` returns) — this is architectural, not an opt-in helper a call site can forget to use. See `docs/logging.md` for what it catches and its honest limits.

**Interservice calls go over gRPC (`pkg/grpcx`), not HTTP.** The client dial helper forwards the calling request's ID and the caller's own bearer token, so a downstream service's Cedar policies see the same principal rather than a separate service identity — `examples/heroes` calling `examples/villains` for a nemesis lookup is the worked example. mTLS (`authn.ServiceIdentity`, `Authorizer.CheckService`) is a parallel, composable mechanism answering "which *service*," not "which user" — see `docs/grpc.md`.

**`examples/heroes` and `examples/villains` are the two reference implementations**, together exercising the whole framework: heroes is HTTP CRUD + Cedar RBAC/ABAC with an optional Postgres backend; villains is gRPC-only, called by heroes. Each has a `store_pg.go` showing the hand-written, parameterized-SQL `crud.Store` shape a real service's Postgres store takes — there is no generic SQL builder or sqlc codegen wired into this repo; filtering/sorting SQL is hand-written per store with an explicit column allowlist (never interpolating a client-supplied field name into SQL text).

**`cmd/go-api` (`go-api new`) is a code generator, not a runtime dependency.** It renders Go source from templates in `cmd/go-api/internal/scaffold/templates/` into a *new, separate* module requiring `github.com/herlenashavi/go-api` — it never imports or is imported by anything under `pkg/`. See `docs/scaffolding.md` for what it generates and what's still a roadmap item.

**Known deliberate gaps** (not oversights — see `docs/roadmap.md` for the full list and reasoning): no transactional outbox/idempotency keys/ETags/response caching, no generated OpenAPI client, no gRPC streaming, and `go-api new` doesn't yet wire gRPC/mTLS/Tilt manifests or add resources to an *existing* generated service.
