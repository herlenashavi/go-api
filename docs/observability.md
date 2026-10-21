# Observability

How go-api instruments logging, tracing, metrics, and health across HTTP,
gRPC, and the database — and what isn't instrumented yet.

## Structured logging

**`pkg/logging`** (see also [Logging & PII Redaction](logging.md)) is
JSON-only via `log/slog` — there is no dev-mode text formatter to diverge
from production. Every record, regardless of call site, passes through a
built-in PII/secret redactor (key-based and pattern-based) before
encoding. Request-scoped context (request ID, trace/span ID, auth
principal) is injected via `Fielder`s without `logging` knowing those
concepts exist.

## Tracing and metrics pipeline

**`pkg/telemetry`** sets up the OTel SDK: OTLP gRPC exporters for traces
and metrics, W3C `traceparent` + baggage propagation, parent-based ratio
sampling. Prometheus exposition (`/health/metrics`) is always on,
independent of OTLP. HTTP RED metrics
(`http.server.requests`/`duration`/`active_requests`) are labeled by
route *pattern*, not the resolved path, to keep cardinality bounded.

Custom business metrics use the same `Meter` — see
[Custom Metrics](metrics.md) for the pattern and its cardinality rules.

## HTTP middleware

`Tracing` (server span per request), `Metrics` (RED), `AccessLog`
(structured one-line-per-request log, optional redacted debug body
logging), `Recover` (panic → 500 problem+json + logged stack trace).

## gRPC

**`pkg/grpcx`** mirrors the HTTP stack: request-ID propagation via
metadata, access-log and panic-recovery interceptors, and an `otelgrpc`
stats handler sharing the same `TracerProvider`/`MeterProvider` as HTTP,
so HTTP and gRPC telemetry land in one pipeline. The gRPC health service
is backed by the same `health.Checker`s as the HTTP readiness endpoint.

## Database

**`pkg/dbx`**'s `pgx.QueryTracer` turns every query into a `db.query`
client span. `dbx.Config.Meter`, if set, adds RED-style query metrics
(`db.client.query.count`/`duration`, labeled by outcome) and
connection-pool saturation gauges
(`db.client.connections.idle`/`used`/`max`) — the same shape HTTP gets
from `Tracing` *and* `Metrics` together.

Neither `examples/heroes` nor `examples/villains` currently wires
`Tracer`/`Meter` into `dbx.NewPool`: both build the pool *before* calling
`app.New` (the pool's health check is itself an `app.Options` input), and
`pkg/app.New` builds its `telemetry.Provider` internally with no option
to accept a pre-built one or hand it back out early. Wiring this would
need either a `pkg/app` API change (constructing/exposing the
`telemetry.Provider` before the rest of `App`) or restructuring
`health.Router` to accept checks added after construction — neither
exists today.

## Health

**`pkg/health`** provides liveness/readiness with per-check timeouts
that can't be hung by a misbehaving `Checker`, plus the Prometheus scrape
endpoint. `/health/readiness` is unauthenticated, so its body names which
checks failed but never *why*: a failing check's error (which for a
database ping names hosts, users, and databases) is logged at warn level
as `readiness check failed`, not returned to the prober.

## Known gaps

**No error-tracking/exception-aggregation integration.** Panics are
logged with a stack trace (`middleware.Recover`, `grpcx`'s
`recoveryInterceptor`) and the trace span gets `codes.Error`, but nothing
aggregates "same panic, N times, across M pods" the way Sentry/Rollbar
do — that has to be reconstructed by hand from logs. Panics also aren't
counted as a metric distinct from ordinary 5xx responses: a spike in
`http.server.requests` by status code doesn't distinguish an intentional
500 from an actual crash.

**No continuous profiling / pprof endpoint.** Nothing exposes
`net/http/pprof` or wires up a continuous profiler (e.g.
Pyroscope/Google Cloud Profiler). CPU/memory regressions are invisible
until they show up as latency or OOMs. A `pprof` endpoint would need
gating behind `cfg.Debug` or a separate internal-only listener — the
same posture `/health/metrics` already has, mounted outside auth but
still not something to expose publicly in production.

**No SLO/error-budget metrics.** RED metrics exist, but nothing computes
or exposes a rolling error budget or SLO burn rate — that's left to
whatever queries the Prometheus/OTLP backend downstream.

**No log sampling/rate limiting.** `AccessLog` logs every request
unconditionally. A noisy endpoint (health-check-adjacent polling, a
retry storm) has no built-in way to cap log volume short of adding it to
`IgnorePaths` entirely, which loses the signal rather than thinning it.

**No outbound-call instrumentation for non-DB dependencies.** `dbx`
traces Postgres, and gRPC-to-gRPC calls are covered (`grpcx.Dial`), but
there's no wrapped `http.Client` pattern for calling another service's
plain HTTP API — that call is un-instrumented unless the caller manually
injects trace context via `telemetry.Provider.Inject`.

Of these, a panic-rate metric is the cheapest to add (one counter in
`middleware.Recover`/`grpcx.recoveryInterceptor`, parallel to what they
already log); a `pprof` endpoint is the next most self-contained. Error-
tracking integration is more an organizational decision (which vendor,
if any) than a framework one.
