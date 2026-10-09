package middleware

import (
	"net/http"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// Tracing starts a server span for every request, extracting an inbound
// W3C traceparent (if any) from the request headers via propagator so the
// span joins an upstream caller's trace. The route pattern isn't known
// until chi finishes routing, so the span starts as "HTTP {method}" and is
// renamed to "{method} {route}" once routing completes — tracing backends
// handle a renamed span normally; this just keeps the final name
// low-cardinality instead of "HTTP GET" for every route.
func Tracing(tracer trace.Tracer, propagator propagation.TextMapPropagator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := propagator.Extract(r.Context(), propagation.HeaderCarrier(r.Header))

			ctx, span := tracer.Start(ctx, "HTTP "+r.Method,
				trace.WithSpanKind(trace.SpanKindServer),
				trace.WithAttributes(
					attribute.String("http.request.method", r.Method),
					attribute.String("url.path", r.URL.Path),
				),
			)
			defer span.End()

			sw := newStatusWriter(w)
			next.ServeHTTP(sw, r.WithContext(ctx))

			route := routePattern(r)
			span.SetName(r.Method + " " + route)
			span.SetAttributes(
				attribute.String("http.route", route),
				attribute.Int("http.response.status_code", sw.status),
			)
			if sw.status >= 500 {
				span.SetStatus(codes.Error, http.StatusText(sw.status))
			}
		})
	}
}
