# Roadmap

This framework was scoped deliberately: a working, tested server kit plus
opt-in CRUD, Cedar authz, and one worked example — rather than a long list
of half-built capabilities. The decisions below were made without
stopping to ask, where the choice was a reasonable default rather than
something only the project owner could decide; they're recorded here so
they're visible and revisitable, not buried in a chat transcript.

## Decisions made (not re-litigated unless you want to)

- **Wire format**: RFC 9457 `application/problem+json` errors, camelCase
  JSON bodies, cursor pagination as the default with offset as a
  documented fallback. See `pkg/problem`, `pkg/httpx.Page`.
- **Onboarding**: a worked example (`examples/heroes`) plus docs, no
  scaffolding CLI yet (see below).
- **In scope now**: request timeouts, body-size limits, panic recovery,
  graceful shutdown, CORS, security headers (`pkg/middleware`); the
  `Principal.TenantID` field exists and reaches Cedar policies, though
  per-tenant query scoping is still the `Store` implementation's job (see
  `docs/authorization.md`).

## Deferred — phase 2/3

Each of these is a real, known gap, not an oversight:

- **Scaffolding CLI** (`go-api new myservice`, `go-api add resource X`).
  The example service + docs prove the shape first; a CLI is worth
  building once that shape has stopped moving, not before.
- **Transactional outbox + event consumers.** canvass-fastapi's
  `PublishAction` (`pkg/actions/kafka.py`) published Kafka messages as a
  lifecycle hook with no delivery guarantee — the write could commit and
  the publish could still fail silently. A correct version needs an
  outbox table written in the same transaction as the domain change, plus
  a relay process, plus a consumer runtime sharing the app's
  observability middleware. None of that exists yet; `dbx.WithTx` is the
  building block it would be built on.
- **Idempotency keys** for unsafe methods (POST/PATCH/PUT), so a retried
  request after a dropped connection doesn't double-create or
  double-charge.
- **ETag / conditional requests** (`If-Match`/`If-None-Match`) for
  optimistic concurrency and cheap re-validation.
- **Response caching** via a pluggable (in-memory/Redis) cache interface.
- **A generated typed Go client** per service, from its OpenAPI-equivalent
  contract. go-api's `httpx.Handle` currently has no OpenAPI emitter at
  all — request/response shapes are real Go types, but nothing walks them
  into a spec yet. That's the prerequisite for a generated client, not
  just the client itself.
- **gRPC streaming and mTLS.** `pkg/grpcx` (added for interservice
  communication — see `docs/grpc.md`) covers unary RPCs and
  bearer-token auth/token-forwarding only. A streaming RPC needs its own
  interceptor wired via `grpc.StreamInterceptor`; mutual TLS between
  services, and a service-identity story for calls not made on a specific
  user's behalf (a cron job, a reconciler), are both unaddressed —
  `grpcx.Dial` connects without TLS by default.

## Known limitations worth stating plainly

- `pkg/httpx.Decode`'s struct-tag binding covers common scalar types,
  `uuid.UUID`, `time.Time` (RFC 3339), and `[]string` query params — it is
  not a general-purpose encoder. Anything more exotic is a case for a
  hand-written handler, which `pkg/httpx.Respond`/`problem` still support
  directly.
- `crud.Resource`'s `Update` method is shared by both PUT and PATCH; true
  partial-update semantics depend on your `UpdateIn` type using pointer
  fields (as `examples/heroes/hero.go`'s `UpdateHero` does). There's no
  separate "full replace" schema the way canvass-fastapi's
  `update_schema` vs `partial_schema` distinguished them.
