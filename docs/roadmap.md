# Roadmap

This framework was scoped deliberately: a working, tested server kit plus
opt-in CRUD, Cedar authz, and one worked example — rather than a long list
of half-built capabilities. The decisions below were made without
stopping to ask, where the choice was a reasonable default rather than
something only the project owner could decide; they're recorded here so
they're visible and revisitable rather than left undocumented.

## Decisions made (not re-litigated unless you want to)

- **Wire format**: RFC 9457 `application/problem+json` errors, camelCase
  JSON bodies, cursor pagination as the default with offset as a
  documented fallback. See `pkg/problem`, `pkg/httpx.Page`.
- **Onboarding**: a worked example (`examples/heroes`) plus docs, plus
  `go-api new` (`cmd/go-api`, see `docs/scaffolding.md`) to generate a new
  service with authn/authz/Postgres/CRUD wired per flag — built once the
  shape of `examples/heroes`/`examples/villains` had stopped moving,
  rather than before. gRPC/mTLS/Tilt manifests/the custom-metrics
  example/`add resource` are still deferred (see `docs/scaffolding.md`'s
  "What `new` doesn't do yet").
- **In scope now**: request timeouts, body-size limits, panic recovery,
  graceful shutdown, CORS, security headers (`pkg/middleware`); the
  `Principal.TenantID` field exists and reaches Cedar policies, though
  per-tenant query scoping is still the `Store` implementation's job (see
  `docs/authorization.md`).
- **Config**: one `config.Base`, loaded once via `config.Load[T]`;
  extend it by embedding, not by reading extra env vars by hand. See
  `docs/config.md`, including what moved into `Base` (gRPC listen
  address, dev JWT secret, mTLS file paths) versus what deliberately
  stayed out (mTLS `ServiceRoles`, a peer's address) and why.
- **CI**: test, build each example's production image, scan it with
  Trivy, publish a report — no registry push (there's nowhere to push
  to yet), and the scan is report-only, not a merge gate. See
  `docs/ci.md` for how to turn either of those on later.

## Deferred — phase 2/3

Each of these is a real, known gap, not an oversight:

- **`go-api add resource X`** — adding a CRUD resource to an *existing*
  generated service. `go-api new` (see `docs/scaffolding.md`) only
  scaffolds at creation time; `add` would write the same
  model/store/migration files into a directory the tool didn't create,
  then print (not apply) the two-line `main.go` snippet to wire it in —
  auto-editing existing hand-written source is exactly the kind of magic
  this framework avoids elsewhere.
- **`go-api new` add-ons not yet wired**: `-grpc`/`-mtls`
  (`app.Options.GRPCEnabled`/`grpcx` mTLS — blocked on deciding whether/how
  to also scaffold a starter `.proto`, since a gRPC server with no RPCs to
  register is a mostly-empty skeleton), `-tilt` (the
  `examples/deploy`-style Tiltfile + k8s manifests), and a
  `-metrics-example` flag generating the `docs/metrics.md` Store-decorator
  pattern. None of these block a generated service from working — see
  `docs/scaffolding.md`.
- **`-db` as an enum, not a bool.** If a non-Postgres backend (e.g.
  DynamoDB) is ever added, `-db` on `go-api new` needs to become
  `-db=postgres|dynamo` rather than the current boolean — noted here so
  it isn't a surprise breaking change later; not needed until a second
  backend actually exists.
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
- **gRPC client-side streaming.** `grpcx.NewServer` runs streaming RPCs
  through the same interceptor chain as unary ones (recovery, request-ID,
  access logging, mTLS peer identity, bearer-token auth). The client side
  is still unary-only: `grpcx.Dial` forwards the request ID and bearer
  token on unary calls, not on streams. Neither example service exercises
  streaming.
- **mTLS CA rotation and issuer integration.** mTLS itself is supported
  (`grpcx.TLSConfig`/`LoadMTLS`, `authn.ServiceIdentity`,
  `Authorizer.CheckService` — see `docs/grpc.md`), including hot-reload
  of leaf certificates. Not supported: rotating the CA itself on a
  long-lived client connection (by design — see that doc's "What's
  asymmetric about reload"), and any integration with a cert issuer
  (SPIRE, cert-manager) to derive `TLSConfig.ServiceRoles` automatically
  rather than maintaining that map in Go.

## Known limitations worth stating plainly

- `pkg/logging`'s PII filter (`docs/logging.md`) is pattern- and
  key-name-based. It catches PII with a recognizable shape (emails,
  phone numbers, globally-routable IPs, issuer-prefixed Luhn-valid card
  numbers) or an identifiable field name — not a bare person's name in
  free text, which no regex can reliably distinguish from two ordinary
  capitalized words. It is defense-in-depth, not a substitute for not
  logging PII in the first place. It errs toward *under*-redacting
  infrastructure (private/loopback addresses, operational field names —
  see that doc's "What pattern matching deliberately leaves alone"),
  because a filter that masks a service's own listen address costs
  debuggability for no privacy gain.
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
