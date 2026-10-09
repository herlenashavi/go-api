# go-api

A Go framework for building internal/production HTTP APIs on [chi](https://github.com/go-chi/chi),
with observability, structured logging, authentication, and
[Cedar](https://www.cedarpolicy.com/)-based role/policy authorization built
in from the start — not bolted on per service.

It is a spiritual successor to `canvass-fastapi`, rebuilt in Go with the
same goal (make it fast to start a new API, hard to skip observability) but
without that library's specific flaws — see [Flaws fixed](#flaws-fixed-from-canvass-fastapi)
below.

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
| `pkg/app` | Bootstraps all of the above into one chi router with a sane default middleware chain |

## Quickstart

```shell
cd examples/heroes
go run .
```

This runs a complete example — roles, Cedar policies, generic CRUD, a
hand-written route, and a dev endpoint for minting test tokens — with zero
external dependencies (in-memory store, HMAC "JWT" for local auth). See
[examples/heroes/README.md](examples/heroes/README.md) for a full curl
walkthrough, and [store_pg.go](examples/heroes/store_pg.go) for the
dbx/pgx-backed shape a real service's store takes.

## Wire format

- Errors: [RFC 9457](https://www.rfc-editor.org/rfc/rfc9457) `application/problem+json`, every response carrying `requestId`/`traceId` for support correlation (`pkg/problem`).
- Bodies: camelCase JSON.
- Lists: `{"data": [...], "meta": {...}}`, cursor pagination by default with offset (`?offset=&limit=`) as a documented fallback (`pkg/httpx.Page`, `pkg/crud.ListParams`).

## Authorization

Every API action is governed by a [Cedar](https://www.cedarpolicy.com/)
policy, not by role checks scattered through handler code. Roles form a
hierarchy (e.g. `admin > editor > viewer`) compiled into Cedar entities;
policies live in `.cedar` files and are hot-reloadable. See
**[docs/authorization.md](docs/authorization.md)** for the full design,
including the RBAC/ABAC split and how to do row-level checks.

## Flaws fixed from canvass-fastapi

go-api was designed after reading `canvass-fastapi` end to end. Specific
things it does differently, and why:

| canvass-fastapi | go-api |
|---|---|
| Identity from a client-settable `X-User-ID` header, written straight into `created_by` | `authn.Principal` comes only from a verified JWT (or your own `Verifier`); no header is ever trusted as identity |
| No authorization framework — permissions were ad hoc per route | Cedar policies + role hierarchy enforced at the route level, with an ABAC escape hatch for row-level checks (`pkg/authz`) |
| `filter_factory` allowed filtering on *any* model column via raw `getattr` | `crud.Config.FilterableFields`/`SortableFields` are explicit allowlists |
| Metrics path label was the resolved URL, substituting only UUIDs — any other path param still exploded cardinality | Metrics are always labeled by the **route pattern** (`/v1/heroes/{id}`), never the resolved path |
| `AsyncSessionMiddleware` committed/rolled back a transaction based on the *HTTP response status code*, after the handler ran | `dbx.WithTx` commits iff your function returns `nil`, decided explicitly where the transaction is opened — never inferred from a status code |
| `ResponseLoggerMiddleware`'s ignore-list was bypassed entirely in debug mode | `middleware.AccessLog`'s `IgnorePaths` always applies; debug-only body logging is a separate, additive concern |
| `schema_factory` synthesized throw-away SQLModel table classes at runtime (via `types.new_class` + `sys.modules` mutation) to diff struct attributes | Request/response shapes are plain Go generics (`crud.Store[T, CreateIn, UpdateIn]`); no runtime type synthesis anywhere |
| Readiness checks (`smtplib`, `psycopg2`) did blocking I/O with no timeout, able to hang the health endpoint | `health.Router` bounds every check with `PerCheckTimeout` and never waits on a hung checker past it |
| `ModelRouter.settings` read `self.app` before `self.app` existed — always `None` | N/A by construction — Go generics don't have an init-order equivalent to get wrong |

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
