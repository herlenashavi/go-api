package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// tracingHarness builds a Tracing middleware over a real SDK tracer with
// an in-memory exporter, so assertions are against spans the OTel pipeline
// actually recorded rather than a hand-written fake.
func tracingHarness(t *testing.T) (func(http.Handler) http.Handler, *tracetest.SpanRecorder, propagation.TextMapPropagator) {
	t.Helper()
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() {
		// Not t.Context(): it is already canceled by the time cleanups run.
		if err := tp.Shutdown(context.Background()); err != nil {
			t.Errorf("tracer provider shutdown: %v", err)
		}
	})

	propagator := propagation.TraceContext{}
	return Tracing(tp.Tracer("test"), propagator), recorder, propagator
}

func onlySpan(t *testing.T, recorder *tracetest.SpanRecorder) sdktrace.ReadOnlySpan {
	t.Helper()
	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("got %d spans, want exactly 1", len(spans))
	}
	return spans[0]
}

func attrs(span sdktrace.ReadOnlySpan) map[attribute.Key]attribute.Value {
	out := make(map[attribute.Key]attribute.Value)
	for _, kv := range span.Attributes() {
		out[kv.Key] = kv.Value
	}
	return out
}

// TestTracing_SpanNamedByRoutePattern pins the rename described in
// Tracing's doc comment: the route isn't known until chi has routed, so
// the span starts as "HTTP GET" and must end up low-cardinality rather
// than one span name per hero ID.
func TestTracing_SpanNamedByRoutePattern(t *testing.T) {
	mw, recorder, _ := tracingHarness(t)

	r := chi.NewRouter()
	r.Use(mw)
	r.Get("/v1/heroes/{id}", func(w http.ResponseWriter, r *http.Request) {})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/heroes/abc-123", nil))

	span := onlySpan(t, recorder)
	if span.Name() != "GET /v1/heroes/{id}" {
		t.Fatalf("span name = %q, want %q", span.Name(), "GET /v1/heroes/{id}")
	}

	got := attrs(span)
	if route := got["http.route"].AsString(); route != "/v1/heroes/{id}" {
		t.Errorf("http.route = %q, want the pattern", route)
	}
	if path := got["url.path"].AsString(); path != "/v1/heroes/abc-123" {
		t.Errorf("url.path = %q, want the resolved path (the high-cardinality value belongs here, not in the name)", path)
	}
	if method := got["http.request.method"].AsString(); method != http.MethodGet {
		t.Errorf("http.request.method = %q, want GET", method)
	}
}

func TestTracing_SpanKindIsServer(t *testing.T) {
	mw, recorder, _ := tracingHarness(t)

	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	if kind := onlySpan(t, recorder).SpanKind(); kind != trace.SpanKindServer {
		t.Fatalf("span kind = %v, want server", kind)
	}
}

func TestTracing_RecordsStatusCode(t *testing.T) {
	cases := []struct {
		status    int
		wantError bool
	}{
		{http.StatusOK, false},
		{http.StatusNotFound, false}, // a 404 is the client's problem, not a server error
		{http.StatusUnprocessableEntity, false},
		{http.StatusInternalServerError, true},
		{http.StatusServiceUnavailable, true},
	}
	for _, tc := range cases {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			mw, recorder, _ := tracingHarness(t)

			h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
			}))
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

			span := onlySpan(t, recorder)
			if got := attrs(span)["http.response.status_code"].AsInt64(); got != int64(tc.status) {
				t.Errorf("http.response.status_code = %d, want %d", got, tc.status)
			}
			isError := span.Status().Code == codes.Error
			if isError != tc.wantError {
				t.Errorf("span status error = %v, want %v for a %d", isError, tc.wantError, tc.status)
			}
		})
	}
}

// TestTracing_JoinsUpstreamTrace is the distributed-tracing property: a
// request arriving with a W3C traceparent must continue that trace rather
// than starting a disconnected one, or a cross-service trace breaks at
// every hop.
func TestTracing_JoinsUpstreamTrace(t *testing.T) {
	mw, recorder, propagator := tracingHarness(t)

	// Build a valid upstream context and inject it the way a calling
	// service's propagator would.
	upstreamTraceID, err := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	if err != nil {
		t.Fatalf("TraceIDFromHex: %v", err)
	}
	upstreamSpanID, err := trace.SpanIDFromHex("00f067aa0ba902b7")
	if err != nil {
		t.Fatalf("SpanIDFromHex: %v", err)
	}
	upstream := trace.ContextWithSpanContext(t.Context(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    upstreamTraceID,
		SpanID:     upstreamSpanID,
		TraceFlags: trace.FlagsSampled,
		Remote:     true,
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	propagator.Inject(upstream, propagation.HeaderCarrier(req.Header))
	if req.Header.Get("traceparent") == "" {
		t.Fatal("test setup: no traceparent header was injected")
	}

	var seenTraceID trace.TraceID
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenTraceID = trace.SpanContextFromContext(r.Context()).TraceID()
	}))
	h.ServeHTTP(httptest.NewRecorder(), req)

	span := onlySpan(t, recorder)
	if span.SpanContext().TraceID() != upstreamTraceID {
		t.Errorf("span trace ID = %s, want the upstream trace %s", span.SpanContext().TraceID(), upstreamTraceID)
	}
	if span.Parent().SpanID() != upstreamSpanID {
		t.Errorf("span parent = %s, want the upstream span %s", span.Parent().SpanID(), upstreamSpanID)
	}
	if seenTraceID != upstreamTraceID {
		t.Errorf("handler saw trace ID %s, want %s — the span context must reach the handler for log correlation",
			seenTraceID, upstreamTraceID)
	}
}

// TestTracing_SpanContextReachesHandler covers the correlation path even
// without an upstream trace: telemetry.TraceLogFields reads the span
// context out of the request context to stamp trace_id on log lines.
func TestTracing_SpanContextReachesHandler(t *testing.T) {
	mw, _, _ := tracingHarness(t)

	var valid bool
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		valid = trace.SpanContextFromContext(r.Context()).IsValid()
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	if !valid {
		t.Fatal("no valid span context in the handler's request context")
	}
}
