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
| `pkg/config` | Env-driven settings: one `config.Base`, embed it in your own struct to extend it — see [docs/config.md](docs/config.md) |
| `pkg/logging` | Always-JSON `log/slog` logging, trace/request-id correlation, and a built-in PII/secret-redacting handler every log line passes through — see [docs/logging.md](docs/logging.md) |
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

## Configuration

Every service loads settings the same way: one env-driven struct,
parsed once at startup. `config.Base` covers everything generic
(listen addresses, log level, timeouts, CORS, OTel, Cedar policy
directory, mTLS file paths); a service adds its own settings by
embedding `Base` in its own `Settings` struct, not by reading extra
env vars by hand — see [docs/config.md](docs/config.md), including why
that replaced the ad hoc per-service helpers (`devJWTSecret()`,
`loadMTLS()`, `grpcPort()`) earlier versions of the example services
used.

## Wire format

- Errors: [RFC 9457](https://www.rfc-editor.org/rfc/rfc9457) `application/problem+json`, every response carrying `requestId`/`traceId` for support correlation (`pkg/problem`).
- Bodies: camelCase JSON.
- Lists: `{"data": [...], "meta": {...}}`, cursor pagination by default with offset (`?offset=&limit=`) as a documented fallback (`pkg/httpx.Page`, `pkg/crud.ListParams`).

## Logging

Every service logs structured JSON, unconditionally — there's no "pretty
text for local dev" mode, in debug or otherwise. Every log line, from any
package, passes through a built-in PII/secret-redacting handler before
it's encoded: known-sensitive field names (`password`, `email`, `ssn`,
...) are masked outright, and every string is additionally scanned for
emails, phone numbers, IPv4 addresses, and Luhn-validated payment card
numbers, wherever they appear — not just under a matching key name. See
[docs/logging.md](docs/logging.md), including the honest limit of what a
regex-based filter can and can't catch.

## Interservice communication

Services talk to each other over gRPC (`pkg/grpcx`), not HTTP: a server
with the same observability/auth posture as the HTTP stack (request ID,
panic recovery, access logging, OTel tracing/metrics via the exact same
providers, bearer-token authentication, optional mutual TLS), and a
client dial helper that propagates the calling request's ID and —
deliberately, not by accident — the caller's own bearer token, so a
downstream service's own Cedar policies see the same principal rather
than a separate service identity.

Two identity mechanisms, answering two different questions, compose on
the same call: a forwarded bearer token says *which user* a call is on
behalf of; mTLS (`grpcx.TLSConfig`/`LoadMTLS`, `authn.ServiceIdentity`,
`Authorizer.CheckService`) says *which service* is calling, verified by
the TLS handshake itself rather than an assertion either side could
fabricate — the right mechanism for a call with no user behind it at all
(a cron job, a reconciler). See [docs/grpc.md](docs/grpc.md).

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
