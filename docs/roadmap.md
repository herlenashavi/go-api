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
- **Transactional outbox + event consumers.** Publishing an event as a
  simple post-write lifecycle hook has no delivery guarantee — the write
  could commit and the publish could still fail silently. A correct
  version needs an outbox table written in the same transaction as the
  domain change, plus a relay process, plus a consumer runtime sharing
  the app's observability middleware. None of that exists yet;
  `dbx.WithTx` is the building block it would be built on.
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
- **gRPC streaming.** `pkg/grpcx`'s interceptor chain (recovery,
  request-ID, access logging, mTLS peer identity, bearer-token auth) is
  entirely unary (`grpc.UnaryServerInterceptor`/`UnaryClientInterceptor`).
  A streaming RPC needs its own interceptor wired via
  `grpc.StreamInterceptor` — not provided, and not exercised by either
  example service.
- **mTLS CA rotation and issuer integration.** mTLS itself is supported
  (`grpcx.TLSConfig`/`LoadMTLS`, `authn.ServiceIdentity`,
  `Authorizer.CheckService` — see `docs/grpc.md`), including hot-reload
  of leaf certificates. Not supported: rotating the CA itself on a
  long-lived client connection (by design — see that doc's "What's
  asymmetric about reload"), and any integration with a cert issuer
  (SPIRE, cert-manager) to derive `TLSConfig.ServiceRoles` automatically
  rather than maintaining that map in Go.

## Known limitations worth stating plainly

- `pkg/httpx.Decode`'s struct-tag binding covers common scalar types,
  `uuid.UUID`, `time.Time` (RFC 3339), and `[]string` query params — it is
  not a general-purpose encoder. Anything more exotic is a case for a
  hand-written handler, which `pkg/httpx.Respond`/`problem` still support
  directly.
- `crud.Resource`'s `Update` method is shared by both PUT and PATCH; true
  partial-update semantics depend on your `UpdateIn` type using pointer
  fields (as `examples/heroes/hero.go`'s `UpdateHero` does). There's no
  separate "full replace" schema distinguishing a PUT body from a PATCH
  body.
