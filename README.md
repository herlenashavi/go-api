# go-api

A Go framework for building internal/production HTTP APIs on [chi](https://github.com/go-chi/chi),
with observability, structured logging, authentication, and
[Cedar](https://www.cedarpolicy.com/)-based role/policy authorization built
in from the start — not bolted on per service.

## Design

A service built on go-api is a **server kit** (config, logging, tracing,
metrics, errors, middleware, health, lifecycle) that works completely on
its own, plus an **opt-in generic CRUD layer** on top of it. You can drop
to a raw chi handler anywhere, at any time — nothing here is the only way
to build a route.

```go
application, err := app.New(ctx, app.Options{
    Config:     cfg.Base,
    Verifier:   authn.NewJWTVerifier(ctx, jwtCfg),   // or NewHMACVerifier for dev
    Authorizer: authorizer,                          // Cedar policies + roles
})

application.
    Mount("/v1/heroes", crud.NewResource(heroResourceConfig, cfg.Debug)). // generated CRUD
    Mount("/v1/reports", myHandwrittenRouter).                           // anything else
    MountPublic("/webhooks/stripe", stripeWebhookRouter)                 // skip auth

application.ListenAndServe(ctx)
```

| Package | Responsibility |
|---|---|
| `pkg/config` | Env-driven settings, embed `config.Base` in your own struct |
| `pkg/logging` | `log/slog` JSON logging, trace/request-id correlation, secret redaction |
| `pkg/telemetry` | OTel traces + metrics (OTLP), Prometheus scrape endpoint, RED metrics |
| `pkg/problem` | RFC 9457 `application/problem+json` error responses |
| `pkg/httpx` | Typed handler wrapper: path/query/header/body decode, validation, respond |
| `pkg/middleware` | Request ID, panic recovery, access log, metrics, tracing, timeout, body limit, CORS, security headers |
| `pkg/authn` | Authentication: `Principal`, JWT/JWKS verifier, dev HMAC verifier for local testing |
| `pkg/authz` | **Cedar-based authorization**: roles, policies, RBAC + ABAC — see [docs/authorization.md](docs/authorization.md) |
| `pkg/health` | Liveness/readiness/metrics endpoints, timeout-bounded dependency checks |
| `pkg/crud` | Opt-in generic CRUD resource builder on top of a `Store[T, CreateIn, UpdateIn]` interface |
| `pkg/dbx` | pgx pool + OTel query tracing + explicit, handler-controlled transaction boundary |
| `pkg/grpcx` | **Interservice gRPC**: server interceptor chain, client dial helper, gRPC↔`problem.Problem` status mapping — see [docs/grpc.md](docs/grpc.md) |
| `pkg/app` | Bootstraps all of the above — HTTP and, if enabled, gRPC — into one lifecycle with a sane default middleware/interceptor chain |

## Quickstart

```shell
cd examples/heroes && go run .   # HTTP :8080
cd examples/villains && go run . # gRPC :9091, HTTP :8081 (health only)
```

Two example services, demonstrating interservice communication: heroes
exposes generic CRUD + Cedar RBAC/ABAC over HTTP, same as before; villains
exposes list/get/create/capture over **gRPC only** (`pkg/grpcx`), and
heroes calls it — `GET /v1/heroes/{id}/nemesis` looks up a hero's nemesis
villain over gRPC, forwarding the caller's request ID and bearer token so
villains authenticates and authorizes (via its own Cedar policies) as the
same principal. See [examples/heroes/README.md](examples/heroes/README.md)
and [examples/villains/README.md](examples/villains/README.md) for full
walkthroughs, [docs/grpc.md](docs/grpc.md) for the gRPC patterns
themselves, and [store_pg.go](examples/heroes/store_pg.go) for the
dbx/pgx-backed shape a real service's store takes.

## Wire format

- Errors: [RFC 9457](https://www.rfc-editor.org/rfc/rfc9457) `application/problem+json`, every response carrying `requestId`/`traceId` for support correlation (`pkg/problem`).
- Bodies: camelCase JSON.
- Lists: `{"data": [...], "meta": {...}}`, cursor pagination by default with offset (`?offset=&limit=`) as a documented fallback (`pkg/httpx.Page`, `pkg/crud.ListParams`).

## Interservice communication

Services talk to each other over gRPC (`pkg/grpcx`), not HTTP: a server
with the same observability/auth posture as the HTTP stack (request ID,
panic recovery, access logging, OTel tracing/metrics via the exact same
providers, bearer-token authentication), and a client dial helper that
propagates the calling request's ID and — deliberately, not by accident —
the caller's own bearer token, so a downstream service's own Cedar
policies see the same principal rather than a separate service identity.
See [docs/grpc.md](docs/grpc.md).

## Authorization

Every API action is governed by a [Cedar](https://www.cedarpolicy.com/)
policy, not by role checks scattered through handler code. Roles form a
hierarchy (e.g. `admin > editor > viewer`) compiled into Cedar entities;
policies live in `.cedar` files and are hot-reloadable. See
**[docs/authorization.md](docs/authorization.md)** for the full design,
including the RBAC/ABAC split and how to do row-level checks.

## Running in Kubernetes (Tilt + minikube)

Both example services also run in a local Kubernetes cluster with a fast
edit/rebuild loop:

```shell
minikube start --container-runtime=docker  # see docs/tilt-dev.md for why this flag matters
tilt up
```

See [docs/tilt-dev.md](docs/tilt-dev.md) — it covers the two things
actually worth learning from this setup (a fast live-update loop for a
compiled Go binary, and Cedar policy hot reload wired through a
`ConfigMap`), not just the run commands.

## Development

```shell
make build        # go build ./...
make test         # go test ./...
make test-race    # go test -race ./...
make vet fmt tidy
```

## Roadmap

See [docs/roadmap.md](docs/roadmap.md) for what's deliberately deferred
(transactional outbox/consumers, idempotency keys, ETags, response
caching, a generated typed client, a scaffolding CLI) and why.
