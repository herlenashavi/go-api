package dbx

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// queryMetrics are the RED-style metrics dbx exposes for every query —
// count and duration, broken down by outcome — mirroring the shape
// telemetry.HTTPMetrics gives HTTP requests, so a slow or failing database
// shows up in the same metrics pipeline (and can back the same kind of
// alert) as a slow or failing handler.
type queryMetrics struct {
	queryCount metric.Int64Counter
	duration   metric.Float64Histogram
}

func newQueryMetrics(meter metric.Meter) (*queryMetrics, error) {
	queryCount, err := meter.Int64Counter(
		"db.client.query.count",
		metric.WithDescription("Count of database queries by outcome."),
	)
	if err != nil {
		return nil, fmt.Errorf("dbx: query counter: %w", err)
	}

	duration, err := meter.Float64Histogram(
		"db.client.query.duration",
		metric.WithDescription("Database query duration in seconds."),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, fmt.Errorf("dbx: query duration histogram: %w", err)
	}

	return &queryMetrics{queryCount: queryCount, duration: duration}, nil
}

// record is called once per query, from queryTracer.TraceQueryEnd. It
// deliberately does not label by statement/table: unlike HTTP's route
// pattern, there's no low-cardinality name for a query short of parsing
// SQL, and sqlc call sites already get per-statement detail from the
// trace span's db.statement attribute — this metric is for the
// dashboard/alert case ("is the database slow or erroring right now"),
// not per-query breakdown.
func (m *queryMetrics) record(ctx context.Context, d time.Duration, err error) {
	attrs := attribute.NewSet(
		attribute.String("db.system", "postgresql"),
		attribute.Bool("error", err != nil),
	)
	m.queryCount.Add(ctx, 1, metric.WithAttributeSet(attrs))
	m.duration.Record(ctx, d.Seconds(), metric.WithAttributeSet(attrs))
}

// registerPoolStats exposes pgxpool's own connection-pool accounting
// (idle/used/max connections) as observable gauges, read on demand via a
// callback rather than pushed on every change — a pool's connection
// counts only make sense as "right now" snapshots, never summed or rated
// the way a counter is.
func registerPoolStats(meter metric.Meter, pool *Pool) error {
	idle, err := meter.Int64ObservableGauge(
		"db.client.connections.idle",
		metric.WithDescription("Idle connections currently held by the pool."),
	)
	if err != nil {
		return fmt.Errorf("dbx: idle connections gauge: %w", err)
	}
	used, err := meter.Int64ObservableGauge(
		"db.client.connections.used",
		metric.WithDescription("Connections currently acquired/in-use."),
	)
	if err != nil {
		return fmt.Errorf("dbx: used connections gauge: %w", err)
	}
	maxGauge, err := meter.Int64ObservableGauge(
		"db.client.connections.max",
		metric.WithDescription("Maximum connections the pool will open."),
	)
	if err != nil {
		return fmt.Errorf("dbx: max connections gauge: %w", err)
	}

	_, err = meter.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		stat := pool.Stat()
		o.ObserveInt64(idle, int64(stat.IdleConns()))
		o.ObserveInt64(used, int64(stat.AcquiredConns()))
		o.ObserveInt64(maxGauge, int64(stat.MaxConns()))
		return nil
	}, idle, used, maxGauge)
	if err != nil {
		return fmt.Errorf("dbx: register connection pool callback: %w", err)
	}
	return nil
}
