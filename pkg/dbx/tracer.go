package dbx

import (
	"context"

	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// queryTracer implements pgx.QueryTracer, turning every query into a
// client span. Statement text is attached as-is (sqlc-generated queries
// are static, parameterized SQL — never string-built from request input —
// so this does not leak user data into span attributes the way logging a
// dynamically-built query might).
type queryTracer struct {
	tracer trace.Tracer
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
	return context.WithValue(ctx, spanContextKey{}, span)
}

func (t *queryTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	span, ok := ctx.Value(spanContextKey{}).(trace.Span)
	if !ok {
		return
	}
	defer span.End()
	if data.Err != nil {
		span.RecordError(data.Err)
		span.SetStatus(codes.Error, data.Err.Error())
	}
}
