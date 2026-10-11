# go-api

A Go framework for building internal/production HTTP APIs on
[chi](https://github.com/go-chi/chi), with observability, structured
logging, authentication, and [Cedar](https://www.cedarpolicy.com/)-based
role/policy authorization built in from the start — not bolted on per
service.

[:octicons-mark-github-16: View on GitHub](https://github.com/herlenashavi/go-api){ .md-button }

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
| `pkg/config` | Env-driven settings — see [Configuration](config.md) |
| `pkg/logging` | Always-JSON logging, trace/request-id correlation, built-in PII redaction — see [Logging](logging.md) |
| `pkg/telemetry` | OTel traces + metrics (OTLP), Prometheus scrape endpoint, RED metrics — see [Observability](observability.md) |
| `pkg/problem` | RFC 9457 `application/problem+json` error responses |
| `pkg/httpx` | Typed handler wrapper: path/query/header/body decode, validation, respond |
| `pkg/middleware` | Request ID, panic recovery, access log, metrics, tracing, timeout, body limit, CORS, security headers |
| `pkg/authn` | Authentication: `Principal`, JWT/JWKS verifier, dev HMAC verifier for local testing |
| `pkg/authz` | **Cedar-based authorization**: roles, policies, RBAC + ABAC — see [Authorization](authorization.md) |
| `pkg/health` | Liveness/readiness/metrics endpoints, timeout-bounded dependency checks |
| `pkg/crud` | Opt-in generic CRUD resource builder on top of a `Store[T, CreateIn, UpdateIn]` interface |
| `pkg/dbx` | pgx pool + OTel query tracing/metrics + explicit, handler-controlled transaction boundary |
| `pkg/grpcx` | **Interservice gRPC**: server interceptor chain, client dial helper, status mapping — see [gRPC](grpc.md) |
| `pkg/app` | Bootstraps all of the above into one lifecycle with a sane default middleware/interceptor chain |

## Quickstart

```shell
cd examples/heroes && go run .   # HTTP :8080
cd examples/villains && go run . # gRPC :9091, HTTP :8081 (health only)
```

Two example services demonstrate interservice communication: heroes
exposes generic CRUD + Cedar RBAC/ABAC over HTTP; villains exposes
list/get/create/capture over **gRPC only**, and heroes calls it —
`GET /v1/heroes/{id}/nemesis` looks up a hero's nemesis villain over
gRPC, forwarding the caller's request ID and bearer token so villains
authenticates and authorizes as the same principal. See the repo's
[examples/heroes/README.md](https://github.com/herlenashavi/go-api/blob/main/examples/heroes/README.md)
and [examples/villains/README.md](https://github.com/herlenashavi/go-api/blob/main/examples/villains/README.md)
for full walkthroughs.

## Scaffold a new service

```shell
go run github.com/herlenashavi/go-api/cmd/go-api@latest new orders -module github.com/you/orders -db -authz -authn hmac -crud Order
```

See [Scaffolding](scaffolding.md) for the full flag surface.

## Where to go next

- **New to the framework?** Start with [Configuration](config.md), then [Observability](observability.md).
- **Adding auth to a service?** [Authorization](authorization.md).
- **Calling another service?** [Interservice gRPC](grpc.md).
- **Generating a new service?** [Scaffolding](scaffolding.md).
- **Running locally against Kubernetes?** [Local Dev (Tilt + minikube)](tilt-dev.md).
- **Wondering what's deliberately not built yet?** [Roadmap](roadmap.md).
