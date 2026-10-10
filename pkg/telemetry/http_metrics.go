package telemetry

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// HTTPMetrics are the RED (Rate, Errors, Duration) metrics every go-api
// service exposes for every route. The path label is always the route
// *pattern* ("/v1/heroes/{id}"), never the resolved path — so numeric and
// slug path parameters can't blow up metric cardinality the way raw
// paths do.
type HTTPMetrics struct {
	requestCount metric.Int64Counter
	duration     metric.Float64Histogram
	inFlight     metric.Int64UpDownCounter
}

// NewHTTPMetrics creates the instruments on meter. Call it once at startup.
func NewHTTPMetrics(meter metric.Meter) (*HTTPMetrics, error) {
	requestCount, err := meter.Int64Counter(
		"http.server.requests",
		metric.WithDescription("Count of HTTP requests by method, route, and status."),
	)
	if err != nil {
		return nil, fmt.Errorf("telemetry: requests counter: %w", err)
	}

	duration, err := meter.Float64Histogram(
		"http.server.duration",
		metric.WithDescription("HTTP request duration in seconds."),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, fmt.Errorf("telemetry: duration histogram: %w", err)
	}

	inFlight, err := meter.Int64UpDownCounter(
		"http.server.active_requests",
		metric.WithDescription("Number of in-flight HTTP requests."),
	)
	if err != nil {
		return nil, fmt.Errorf("telemetry: in-flight gauge: %w", err)
	}

	return &HTTPMetrics{requestCount: requestCount, duration: duration, inFlight: inFlight}, nil
}

// Start marks the beginning of a request. The route pattern (e.g.
// "/v1/heroes/{id}") usually isn't known yet at this point — chi resolves
// it while routing — so Start only takes method; the returned finish func
// takes the route once it's known, plus the final status code. Call finish
// exactly once, whatever happens (including on panic recovery).
func (m *HTTPMetrics) Start(ctx context.Context, method string) func(route string, status int) {
	inFlightAttrs := attribute.NewSet(attribute.String("http.request.method", method))
	m.inFlight.Add(ctx, 1, metric.WithAttributeSet(inFlightAttrs))
	start := time.Now()

	return func(route string, status int) {
		m.inFlight.Add(ctx, -1, metric.WithAttributeSet(inFlightAttrs))
		withStatus := attribute.NewSet(
			attribute.String("http.request.method", method),
			attribute.String("http.route", route),
			attribute.Int("http.response.status_code", status),
		)
		m.requestCount.Add(ctx, 1, metric.WithAttributeSet(withStatus))
		m.duration.Record(ctx, time.Since(start).Seconds(), metric.WithAttributeSet(withStatus))
	}
}
