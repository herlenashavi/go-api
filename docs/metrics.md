# Custom metrics

Every go-api service already gets `http.server.requests`/`duration`/
`active_requests` (`pkg/telemetry`) and, if you're on `dbx`,
`db.client.query.count`/`duration` and `db.client.connections.*`
(`pkg/dbx`) for free. This is how to add your own — a business counter
like "heroes created," a histogram for some domain-specific operation,
whatever your service needs that the framework can't know about.

## Where the Meter comes from

`app.New` builds one `metric.Meter` per service (via `pkg/telemetry`) and
exposes it as `application.Telemetry.Meter`. It is **the exact same
Meter** that backs the framework's own HTTP/DB instruments — there is no
separate registration step, no second exporter to configure. Any
instrument you create from it is:

- scraped by Prometheus at `/health/metrics` alongside everything else,
  always (Prometheus export doesn't depend on `MetricsEnabled`/
  `OTLPEndpoint`);
- shipped via OTLP too, if `Config.MetricsEnabled` and
  `Config.OTLPEndpoint` are set.

`application.Telemetry.Meter` only exists once `app.New` has returned —
if you need to instrument something constructed *before* that call (as
`examples/heroes`' DB pool is, so it can feed `app.Options.HealthChecks`),
wire the metric in afterward, the same way `examples/heroes/main.go` wraps
its `heroStore` with `newInstrumentedHeroStore` right after `app.New`
returns rather than where the store is first constructed.

## The three instrument kinds

OTel's metric API (`go.opentelemetry.io/otel/metric`) gives you:

- **Counter** (`meter.Int64Counter` / `Float64Counter`) — a value that
  only goes up: requests served, items created, errors seen. Use `.Add`.
- **Histogram** (`meter.Float64Histogram` / `Int64Histogram`) — a
  distribution you want percentiles over: duration, payload size. Use
  `.Record`.
- **UpDownCounter** / **ObservableGauge** — a value that can go up or
  down, or that you only know how to compute on demand: in-flight
  requests (`telemetry.HTTPMetrics.inFlight`, an `Int64UpDownCounter`),
  connection-pool occupancy (`dbx`'s pool-stats gauges, read via a
  callback). Use an observable gauge when the value is cheapest to
  compute lazily at scrape time rather than tracked incrementally.

`pkg/telemetry/http_metrics.go` and `pkg/dbx/metrics.go` are the two
reference implementations already in this codebase — copy their shape
(instruments built once, attribute sets built once per call, no
allocation surprises) rather than inventing a new one.

## Two rules that matter

1. **Create instruments once, at startup — never inside a request
   handler.** `meter.Int64Counter(name, ...)` returns the *same*
   instrument every time you call it with the same name, but calling it
   per-request is still wasted work and, more importantly, an easy way to
   typo a name differently across call sites and end up with two
   instruments instead of one. Build it once (in a constructor, package
   `init`-adjacent setup, or a decorator's constructor — see
   `examples/heroes/metrics.go`) and hold onto the handle.

2. **Keep attribute values low-cardinality.** The same rule that keeps
   `http.server.requests` labeled by route *pattern* and not resolved
   path, or keeps `dbx`'s query metrics unlabeled by statement/table (see
   `docs/observability.md`), applies to anything you add: never put a
   user ID, a free-text field, or anything else effectively unbounded
   into an attribute value. A status enum, a boolean, a small fixed set
   of categories — fine. A raw ID — not fine; it turns one metric into
   millions of time series.

## Naming

Lowercase, dot-separated, matching the style already used by the
framework's own instruments (`http.server.requests`, `db.client.query.count`)
and OTel's semantic conventions generally — e.g. `heroes.created`, not
`HeroesCreated` or `heroes_created_total`. The trailing `_total`/type
suffix you'll see in `/health/metrics` output is something the
Prometheus exporter appends automatically; don't bake it into the name
yourself.

## Worked example

`examples/heroes` defines two business counters —
`heroes.created`/`heroes.retired` — by wrapping its `heroStore` in a
decorator (`examples/heroes/metrics.go`), rather than reaching into
`pkg/crud`: `crud.Resource` has no Create/Update/Delete lifecycle hooks
(see its package doc), so a `Store` decorator is the idiomatic
composition point, the same place you'd add caching or audit logging in
front of an existing store.

```go
// metrics.go
type instrumentedHeroStore struct {
    heroStore
    created metric.Int64Counter
    retired metric.Int64Counter
}

func newInstrumentedHeroStore(meter metric.Meter, next heroStore) (heroStore, error) {
    created, err := meter.Int64Counter("heroes.created",
        metric.WithDescription("Count of heroes created."))
    if err != nil {
        return nil, fmt.Errorf("heroes.created counter: %w", err)
    }
    retired, err := meter.Int64Counter("heroes.retired",
        metric.WithDescription("Count of heroes retired."))
    if err != nil {
        return nil, fmt.Errorf("heroes.retired counter: %w", err)
    }
    return &instrumentedHeroStore{heroStore: next, created: created, retired: retired}, nil
}

func (s *instrumentedHeroStore) Create(ctx context.Context, in CreateHero) (Hero, error) {
    hero, err := s.heroStore.Create(ctx, in)
    if err == nil {
        s.created.Add(ctx, 1)
    }
    return hero, err
}
```

```go
// main.go, right after app.New returns
store, err = newInstrumentedHeroStore(application.Telemetry.Meter, store)
if err != nil {
    return err
}
```

Verify it end-to-end: create a hero, then `curl localhost:<port>/health/metrics`:

```
# HELP heroes_created_total Count of heroes created.
# TYPE heroes_created_total counter
heroes_created_total{otel_scope_name="heroes-example",...} 1
```

## If you're not using `pkg/crud`

The pattern is the same minus the decorator: build the instrument once
(wherever you already hold `application`, e.g. right after `app.New`),
close over it, and call `.Add`/`.Record` from inside whatever handler or
service-layer code needs it.
