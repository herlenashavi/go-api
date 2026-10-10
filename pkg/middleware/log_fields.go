package middleware

import (
	"context"
	"log/slog"

	"github.com/herlenashavi/go-api/pkg/reqctx"
)

// RequestIDLogFields is a logging.Fielder that attaches the request ID
// assigned by RequestID to every log line written during that request.
func RequestIDLogFields(ctx context.Context) []slog.Attr {
	id := reqctx.RequestID(ctx)
	if id == "" {
		return nil
	}
	return []slog.Attr{slog.String("request_id", id)}
}
