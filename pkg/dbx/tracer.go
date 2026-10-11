package dbx

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// queryTracer implements pgx.QueryTracer, turning every query into a
// client span and, if metrics is non-nil, a count/duration observation.
// Statement text is attached to the span as-is (sqlc-generated queries
// are static, parameterized SQL — never string-built from request input —
// so this does not leak user data into span attributes the way logging a
// dynamically-built query might).
type queryTracer struct {
	tracer  trace.Tracer
	metrics *queryMetrics // nil disables query metrics; tracing still runs
}

// queryTraceState carries what TraceQueryEnd needs from TraceQueryStart —
// the span to close plus the start time for the duration metric, which the
// span itself doesn't expose.
type queryTraceState struct {
	span  trace.Span
	start time.Time
}

type spanContextKey struct{}

func (t *queryTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	ctx, span := t.tracer.Start(ctx, "db.query",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("db.system", "postgresql"),
			attribute.String("db.statement", data.SQL),
		),
	)
	return context.WithValue(ctx, spanContextKey{}, &queryTraceState{span: span, start: time.Now()})
}

func (t *queryTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	st, ok := ctx.Value(spanContextKey{}).(*queryTraceState)
	if !ok {
		return
	}
	defer st.span.End()
	if data.Err != nil {
		st.span.RecordError(data.Err)
		st.span.SetStatus(codes.Error, data.Err.Error())
	}
	if t.metrics != nil {
		t.metrics.record(ctx, time.Since(st.start), data.Err)
	}
}
