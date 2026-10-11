# Observability

This is a map of what go-api instruments today, how the pieces fit
together, and where the gaps are — written after a review of
`pkg/logging`, `pkg/telemetry`, `pkg/middleware`, `pkg/grpcx`, `pkg/dbx`,
and `pkg/health`.

## What's already there

**Structured logging** (`pkg/logging`, see also `docs/logging.md`)
JSON-only via `log/slog`, no dev-mode text formatter to diverge from
production. Every record — regardless of call site — passes through a
built-in PII/secret redactor (key-based and pattern-based) before
encoding. Request-scoped context (request ID, trace/span ID, auth
principal) is injected via `Fielder`s without `logging` knowing those
concepts exist.

**Tracing + metrics pipeline** (`pkg/telemetry`)
OTel SDK setup: OTLP gRPC exporters for traces and metrics, W3C
`traceparent`+baggage propagation, parent-based ratio sampling. Prometheus
exposition is always on independent of OTLP (`/health/metrics`). HTTP RED
metrics (`http.server.requests`/`duration`/`active_requests`) use the route
*pattern* as a label, not the resolved path, to keep cardinality bounded.

**HTTP middleware** (`pkg/middleware`)
`Tracing` (server span per request), `Metrics` (RED), `AccessLog`
(structured one-line-per-request log, optional redacted debug body
logging), `Recover` (panic → 500 problem+json + logged stack trace).

**gRPC** (`pkg/grpcx`)
Mirrors the HTTP stack: request-ID propagation via metadata, access-log
and panic-recovery interceptors, and an `otelgrpc` stats handler sharing
the same `TracerProvider`/`MeterProvider` as HTTP, so HTTP and gRPC
telemetry land in one pipeline. gRPC health service backed by the same
`health.Checker`s as the HTTP readiness endpoint.

**Database** (`pkg/dbx`)
`pgx.QueryTracer` turns every query into a `db.query` client span.
**As of this review, also:** RED-style query metrics
(`db.client.query.count`/`duration`, labeled by outcome) and
connection-pool saturation gauges (`db.client.connections.idle`/`used`/`max`)
via `dbx.Config.Meter` — see "Addressed" below.

**Health** (`pkg/health`)
Liveness/readiness with per-check timeouts that can't be hung by a
misbehaving `Checker`, plus the Prometheus scrape endpoint.

## Addressed in this pass

**DB query metrics and pool-saturation gauges** (`pkg/dbx`). Previously
`dbx` only traced queries — no counter, no duration histogram, no
visibility into connection-pool exhaustion. HTTP has both `Tracing` *and*
`Metrics`; DB only had the tracing half, which means a dashboard or alert
("error rate," "p99 latency," "pool exhausted") had no DB-side signal to
read, only individual trace spans. Fixed by adding:

- `dbx.Config.Meter metric.Meter` (parallel to the existing `Tracer`
  field) — set either, both, or neither.
- `db.client.query.count` / `db.client.query.duration`, labeled by
  `db.system` and `error` (bool). Deliberately *not* labeled by
  statement/table — there's no low-cardinality query name short of
  parsing SQL, and the trace span already carries full `db.statement`
  detail for the per-query case.
- `db.client.connections.{idle,used,max}` as observable gauges read from
  `pgxpool.Pool.Stat()` on each collection — the thing that actually pages
  someone when a service is saturating its pool under load, which was
  previously invisible until requests started timing out.

**Known limitation, not fixed here:** neither `examples/heroes` nor
`examples/villains` actually wires `Tracer`/`Meter` into `dbx.NewPool` —
not even before this change. Both examples build `dbx.Pool` *before*
calling `app.New` (the pool's health check is itself an `app.Options`
input), and `pkg/app.New` builds its `telemetry.Provider` internally with
no option to accept a pre-built one or hand it back out early. Wiring
this properly means either giving `pkg/app` a way to construct/expose its
`telemetry.Provider` before the rest of `App`, or restructuring
`health.Router` to accept checks added after construction. Either is a
real (if narrow) `pkg/app` API change, out of scope for a metrics
addition — flagged here so it doesn't get lost.

## Remaining gaps

**No error-tracking/exception-aggregation integration.** Panics are
logged with a stack trace (`middleware.Recover`, `grpcx`'s
`recoveryInterceptor`) and the trace span gets `codes.Error`, but nothing
aggregates "same panic, N times, across M pods" the way Sentry/Rollbar do
— that's reconstructed by hand from logs today. Also, panics aren't
counted as a metric: a spike in 500s shows up in `http.server.requests`
by status code, but there's no direct "panic rate" signal separate from
"5xx rate," so a non-panic 500 (e.g. intentionally returned) and an actual
crash look the same at the metrics level.

**No continuous profiling / pprof endpoint.** Nothing exposes
`net/http/pprof` or wires up a continuous profiler (e.g. Pyroscope/Google
Cloud Profiler). CPU/memory regressions are invisible until they show up
as latency or OOMs. Worth gating behind `cfg.Debug` or a separate
internal-only listener, mirroring how `/health/metrics` is mounted
outside auth but is still something you wouldn't want publicly
reachable in prod.

**No SLO/error-budget metrics.** RED metrics exist, but nothing computes
or exposes a rolling error budget / SLO burn rate — that's left to
whatever queries the Prometheus/OTLP backend downstream. Reasonable to
leave as a backend/dashboard concern rather than framework code, but
worth naming so it's not mistaken for "already covered."

**No log sampling/rate limiting.** `AccessLog` logs every request
unconditionally. A noisy endpoint (health-check-adjacent polling, a
retry storm) has no built-in way to cap log volume short of adding it to
`IgnorePaths` entirely (losing the signal rather than just thinning it).

**Custom/business metrics — addressed, see `docs/metrics.md`.**
`application.Telemetry.Meter` was already a real, fully-wired
`metric.Meter` (same Prometheus/OTLP pipeline as the framework's own
instruments), just undocumented and unproven by example. `docs/metrics.md`
now documents the pattern — instrument-naming, cardinality rules, the
"create once at startup" rule — and `examples/heroes` demonstrates it with
two business counters (`heroes.created`/`heroes.retired`) via a `Store`
decorator (`examples/heroes/metrics.go`), composed right after `app.New`
returns. `pkg/crud` still has no Create/Update/Delete lifecycle hooks of
its own — the Store-decorator pattern is the intended way to layer this
on, not a gap.

**No synthetic/outbound-call instrumentation for non-DB dependencies.**
`dbx` traces Postgres; there's no equivalent wrapped `http.Client` or
gRPC client interceptor pattern documented for calling *other* services'
HTTP APIs (gRPC-to-gRPC is covered — see `grpcx.Dial`). If a service calls
out over plain HTTP to a third party, that call is currently
un-instrumented unless the caller remembers to inject
`telemetry.Provider.Inject` manually.

## Suggested priority

1. DB query metrics — done, see above.
2. Panic-rate metric (cheap: one counter in `middleware.Recover` /
   `grpcx.recoveryInterceptor`, parallel to how they already log).
3. pprof endpoint behind `cfg.Debug`.
4. Error-tracking integration, if there's an existing account/budget for
   one (Sentry et al. are usually an organizational decision, not a
   framework one).
