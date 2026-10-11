package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/herlenashavi/go-api/pkg/telemetry"
)

// metricsHarness wires the Metrics middleware to a real SDK meter with a
// manual reader, so the assertions below read back measurements the OTel
// pipeline actually recorded.
func metricsHarness(t *testing.T, ignorePaths []string) (func(http.Handler) http.Handler, *metric.ManualReader) {
	t.Helper()
	reader := metric.NewManualReader()
	mp := metric.NewMeterProvider(metric.WithReader(reader))
	t.Cleanup(func() {
		// Not t.Context(): it is already canceled by the time cleanups run.
		if err := mp.Shutdown(context.Background()); err != nil {
			t.Errorf("meter provider shutdown: %v", err)
		}
	})

	httpMetrics, err := telemetry.NewHTTPMetrics(mp.Meter("test"))
	if err != nil {
		t.Fatalf("NewHTTPMetrics: %v", err)
	}
	return Metrics(MetricsConfig{Metrics: httpMetrics, IgnorePaths: ignorePaths}), reader
}

// collect gathers the current metrics, indexed by instrument name.
func collect(t *testing.T, reader *metric.ManualReader) map[string]metricdata.Metrics {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}

	out := make(map[string]metricdata.Metrics)
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			out[m.Name] = m
		}
	}
	return out
}

func requireMetric(t *testing.T, reader *metric.ManualReader, name string) metricdata.Metrics {
	t.Helper()
	metrics := collect(t, reader)
	m, ok := metrics[name]
	if !ok {
		names := make([]string, 0, len(metrics))
		for n := range metrics {
			names = append(names, n)
		}
		t.Fatalf("metric %q was not recorded; got %v", name, names)
	}
	return m
}

func attrValue(t *testing.T, set attribute.Set, key string) attribute.Value {
	t.Helper()
	v, ok := set.Value(attribute.Key(key))
	if !ok {
		t.Fatalf("data point is missing the %q attribute; has %v", key, set.ToSlice())
	}
	return v
}

// TestMetrics_RecordsRequestLabeledByRoutePattern is the cardinality
// property RED metrics depend on: labeling by resolved path would create a
// new time series per hero ID and eventually take down the metrics
// backend, which is exactly what the route pattern prevents.
func TestMetrics_RecordsRequestLabeledByRoutePattern(t *testing.T) {
	mw, reader := metricsHarness(t, nil)

	r := chi.NewRouter()
	r.Use(mw)
	r.Get("/v1/heroes/{id}", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Two requests to the same route with different IDs must land on one
	// series, not two.
	for _, id := range []string{"abc-123", "def-456"} {
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/v1/heroes/"+id, nil))
	}

	sum, ok := requireMetric(t, reader, "http.server.requests").Data.(metricdata.Sum[int64])
	if !ok {
		t.Fatal("http.server.requests is not an int64 sum")
	}
	if len(sum.DataPoints) != 1 {
		t.Fatalf("got %d data points, want 1 — two IDs on one route must share a series", len(sum.DataPoints))
	}

	dp := sum.DataPoints[0]
	if dp.Value != 2 {
		t.Errorf("count = %d, want 2", dp.Value)
	}
	if got := attrValue(t, dp.Attributes, "http.route").AsString(); got != "/v1/heroes/{id}" {
		t.Errorf("http.route = %q, want the pattern", got)
	}
	if got := attrValue(t, dp.Attributes, "http.request.method").AsString(); got != http.MethodGet {
		t.Errorf("http.request.method = %q, want GET", got)
	}
	if got := attrValue(t, dp.Attributes, "http.response.status_code").AsInt64(); got != http.StatusOK {
		t.Errorf("http.response.status_code = %d, want 200", got)
	}
}

// TestMetrics_SeparatesSeriesByStatus is the "Errors" half of RED: a 500
// and a 200 on the same route have to be distinguishable, or no error-rate
// alert can be built on this.
func TestMetrics_SeparatesSeriesByStatus(t *testing.T) {
	mw, reader := metricsHarness(t, nil)

	r := chi.NewRouter()
	r.Use(mw)
	r.Get("/v1/heroes", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("fail") != "" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/v1/heroes", nil))
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/v1/heroes?fail=1", nil))

	sum := requireMetric(t, reader, "http.server.requests").Data.(metricdata.Sum[int64])
	byStatus := make(map[int64]int64)
	for _, dp := range sum.DataPoints {
		byStatus[attrValue(t, dp.Attributes, "http.response.status_code").AsInt64()] += dp.Value
	}

	if byStatus[http.StatusOK] != 1 || byStatus[http.StatusInternalServerError] != 1 {
		t.Fatalf("counts by status = %v, want one 200 and one 500", byStatus)
	}
}

func TestMetrics_RecordsDuration(t *testing.T) {
	mw, reader := metricsHarness(t, nil)

	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	hist, ok := requireMetric(t, reader, "http.server.duration").Data.(metricdata.Histogram[float64])
	if !ok {
		t.Fatal("http.server.duration is not a float64 histogram")
	}
	if len(hist.DataPoints) != 1 {
		t.Fatalf("got %d data points, want 1", len(hist.DataPoints))
	}
	if hist.DataPoints[0].Count != 1 {
		t.Fatalf("histogram count = %d, want 1", hist.DataPoints[0].Count)
	}
}

// TestMetrics_InFlightReturnsToZero is the leak check on
// HTTPMetrics.Start's finish func: an up/down counter that is incremented
// and not decremented drifts upward forever, so the gauge would read as a
// permanently-saturated server.
func TestMetrics_InFlightReturnsToZero(t *testing.T) {
	mw, reader := metricsHarness(t, nil)

	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	for range 3 {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	}

	sum, ok := requireMetric(t, reader, "http.server.active_requests").Data.(metricdata.Sum[int64])
	if !ok {
		t.Fatal("http.server.active_requests is not an int64 sum")
	}
	var total int64
	for _, dp := range sum.DataPoints {
		total += dp.Value
	}
	if total != 0 {
		t.Fatalf("in-flight = %d after all requests completed, want 0", total)
	}
}

// TestMetrics_InFlightCountsDuringTheRequest confirms the gauge actually
// moves — without this, TestMetrics_InFlightReturnsToZero would also pass
// against a no-op.
func TestMetrics_InFlightCountsDuringTheRequest(t *testing.T) {
	mw, reader := metricsHarness(t, nil)

	var during int64
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sum := requireMetric(t, reader, "http.server.active_requests").Data.(metricdata.Sum[int64])
		for _, dp := range sum.DataPoints {
			during += dp.Value
		}
		w.WriteHeader(http.StatusOK)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	if during != 1 {
		t.Fatalf("in-flight during the request = %d, want 1", during)
	}
}

// TestMetrics_IgnorePathsRecordsNothing covers the documented reason the
// list exists: a Prometheus scrape of /health/metrics would otherwise
// generate metrics about scraping metrics, on every scrape interval.
func TestMetrics_IgnorePathsRecordsNothing(t *testing.T) {
	mw, reader := metricsHarness(t, []string{"/health"})

	var served bool
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		served = true
		w.WriteHeader(http.StatusOK)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/health/metrics", nil))

	if !served {
		t.Fatal("an ignored path should still reach the handler")
	}
	if metrics := collect(t, reader); len(metrics) != 0 {
		t.Fatalf("an ignored path recorded metrics: %v", metrics)
	}
}

func TestMetrics_IgnorePathsDoesNotAffectOtherRoutes(t *testing.T) {
	mw, reader := metricsHarness(t, []string{"/health"})

	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/v1/heroes", nil))

	sum := requireMetric(t, reader, "http.server.requests").Data.(metricdata.Sum[int64])
	if len(sum.DataPoints) != 1 || sum.DataPoints[0].Value != 1 {
		t.Fatalf("a non-ignored route was not recorded: %+v", sum.DataPoints)
	}
}
