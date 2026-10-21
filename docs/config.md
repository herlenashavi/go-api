# Configuration (`pkg/config`)

Every go-api service loads its settings the same way: one env-driven
struct, parsed once at startup, that fails fast on a missing required
value rather than starting the service half-configured. This is
deliberately not "every package reads its own env vars" — a service's
entire configuration surface is one type you can read top to bottom.

## `config.Base`

```go
cfg, err := config.Load[config.Base]()
if err != nil { ... } // fails fast: e.g. SERVICE_NAME unset
```

`config.Base` holds every setting a go-api service generically needs:
the HTTP/gRPC listen addresses, log level, timeouts, CORS, OTel
exporter settings, the Cedar policy directory, JWT verifier settings,
the dev-only HMAC secret, and mTLS file paths (`config.Base.MTLS`, a
nested `MTLSConfig` — see [docs/grpc.md](grpc.md#mutual-tls-for-service-to-service-calls)).
Every field is a plain Go field with an `env` tag; see
`pkg/config/config.go` for the full list and each field's default.

`config.Base.Debug` is the one flag with broader effect than its name
suggests: it turns on debug-level logging (including the optional
request-body logger, [docs/logging.md](logging.md)), and in
`examples/heroes` specifically, gates the dev-token endpoint. It does
**not** change log format — that's always JSON, unconditionally, in
every mode.

The JWT verifier settings (`JWKS_URL`, `JWT_ISSUER`, `JWT_AUDIENCE`) have
no defaults because there is no safe one: `authn.NewJWTVerifier` refuses
to start without an issuer, and without an audience unless the caller
sets `JWTVerifierConfig.SkipAudienceCheck` explicitly — otherwise a token
your IdP minted for any *other* application on the same tenant would be
accepted. Every verified token (JWT or the dev HMAC one) must also carry
an `exp` claim.

### HTTP server timeouts

Three settings bound how long a connection can hold the server, each
covering a gap the others can't:

| Env var | Default | Bounds |
|---|---|---|
| `REQUEST_TIMEOUT_SECONDS` | 30 | Handler run time (the `Timeout` middleware → 503). |
| `HTTP_READ_TIMEOUT_SECONDS` | 30 | Reading the *whole* request, headers and body. This is what stops a client trickling a body in to pin a connection — a handler blocked in `Body.Read` doesn't observe the request timeout. `0` disables it. |
| `HTTP_IDLE_TIMEOUT_SECONDS` | 120 | An idle keep-alive connection between requests. `0` falls back to the read timeout. |

Raise `HTTP_READ_TIMEOUT_SECONDS` for a service that legitimately accepts
large or slow uploads.

## Extending it for your own service

A real service almost always needs at least one setting `Base` doesn't
have — a downstream service's address, a feature flag, a queue name.
The standardized way to add it is embedding, not a hand-rolled
`os.Getenv` call next to `main()`:

```go
type Settings struct {
    config.Base
    KafkaBrokers []string `env:"KAFKA_BROKERS" envSeparator:","`
    FeatureX     bool     `env:"FEATURE_X_ENABLED" envDefault:"false"`
}

cfg, err := config.Load[Settings]()
// cfg.ServiceName, cfg.HTTPPort, ... (from Base) alongside
// cfg.KafkaBrokers, cfg.FeatureX (your own fields) — one struct, one Load call.
```

`config.Load[T]`/`config.MustLoad[T]` are generic over any `T` that
embeds `Base` (directly or transitively) — [`caarlos0/env`](https://github.com/caarlos0/env)
recurses into embedded and named struct fields automatically, so this
needs no code in `pkg/config` itself to support. `examples/heroes/main.go`'s
own `Settings` (adding `VillainsAddr`, the address of the villains gRPC
service it calls) is the real-world version of the snippet above — see
it for the pattern actually in use, not just documented.

`app.Options.Config` still takes a plain `config.Base`, not your
extended `Settings` — pass `cfg.Base` (the embedded field) when you've
defined your own `Settings`:

```go
application, err := app.New(ctx, app.Options{Config: cfg.Base, ...})
```

## Why this, not per-package env reads

Before this existed, each example service read its own extra env vars
by hand: a `devJWTSecret()` function duplicated near-identically in
both `examples/heroes` and `examples/villains`, a `grpcPort()` with its
own `strconv.Atoi`, a `loadMTLS()` reading `MTLS_CERT_FILE`/`MTLS_KEY_FILE`/
`MTLS_CA_FILE` off `os.Getenv` directly. None of that was wrong exactly,
but it meant every service re-derived the same "is this configured or
not" logic, with no single place to see a service's full configuration
surface or its defaults.

The fix was not a new mechanism — `config.Base`'s embed-and-extend
pattern already existed and was already documented — it was *using* it
for everything that's an env concern, and only for that:

- `config.Base.GRPCHost`/`GRPCPort` replace `app.Options.GRPCHost`/`GRPCPort`:
  a gRPC server's listen address is exactly as deployment-tunable as
  `HTTPHost`/`HTTPPort`, so it belongs in the same typed, env-tagged
  struct — not a second, Options-level way to configure the same kind
  of thing. `app.Options.GRPCEnabled` stays on `Options`, deliberately:
  *whether* a service runs a gRPC server at all is a per-service code
  decision, not a per-deployment env one.
- `config.Base.DevJWTSecret` replaces the duplicated `devJWTSecret()`
  helpers.
- `config.Base.MTLS` (`MTLSConfig`) replaces each service's own
  `os.Getenv("MTLS_CERT_FILE")`-style reads; `grpcx.LoadMTLSFromConfig`
  turns it into a `*grpcx.MTLSStore` in one call, replacing the
  "all three empty means off" check every service used to implement
  itself.
- `grpcx.TLSConfig.ServiceRoles` deliberately stays **out** of
  `config.MTLSConfig` and out of env entirely: which roles a verified
  calling service gets is an authorization policy decision — the same
  category as the `authz.RoleRegistry` a service already defines in
  Go — not a value that should live in a deployment manifest next to a
  file path. `grpcx.LoadMTLSFromConfig` takes it as a separate
  argument for exactly this reason.
- A peer address like `VILLAINS_GRPC_ADDR` stays **out** of `config.Base`
  entirely, on purpose: *which* services a given service calls is
  inherently service-specific, not a generic framework concern. It's
  exactly the kind of field the embedding pattern above exists for.

See `pkg/config/config_test.go` for this exercised directly — defaults,
env overrides, the required-field failure, and the embedding pattern
itself parsing both the inherited `Base` fields and a caller's own
extension field from one `Load` call.
