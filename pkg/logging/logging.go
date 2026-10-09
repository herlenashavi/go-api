// Package logging configures structured, JSON-by-default logging on top of
// the standard library's log/slog.
//
// It deliberately avoids global mutable state: callers compose a logger from
// explicit "field functions" (see Fielder) that pull values — a trace ID, a
// request ID, an authenticated principal — out of a context.Context. Other
// go-api packages (telemetry, middleware, authn) each expose their own
// Fielder; app.New wires them together once at startup. logging itself knows
// nothing about traces, requests, or principals.
package logging

import (
	"context"
	"log/slog"
	"os"
	"strings"
)

// Fielder extracts zero or more structured attributes from a request-scoped
// context. It must be fast and must never panic; a Fielder observing an
// absent value (e.g. no principal on an unauthenticated route) returns nil.
type Fielder func(ctx context.Context) []slog.Attr

// Config controls the base logger's behaviour.
type Config struct {
	// Level is one of "debug", "info", "warn", "error". Defaults to "info".
	Level string
	// JSON selects slog.JSONHandler output. When false, a human-readable
	// text handler is used instead — intended for local development only.
	JSON bool
	// ServiceName and Env are attached to every log line.
	ServiceName string
	Env         string
	// Output defaults to os.Stdout.
	Output *os.File
}

// New builds the process-wide *slog.Logger. fielders are consulted, in
// order, on every log record to enrich it with request-scoped context (see
// Fielder); pass none for a context-agnostic logger (e.g. in tests).
func New(cfg Config, fielders ...Fielder) *slog.Logger {
	out := cfg.Output
	if out == nil {
		out = os.Stdout
	}

	opts := &slog.HandlerOptions{Level: parseLevel(cfg.Level)}

	var base slog.Handler
	if cfg.JSON {
		base = slog.NewJSONHandler(out, opts)
	} else {
		base = slog.NewTextHandler(out, opts)
	}

	h := &contextHandler{
		next:     base,
		fielders: fielders,
		baseAttrs: []slog.Attr{
			slog.String("service", cfg.ServiceName),
			slog.String("env", cfg.Env),
		},
	}
	return slog.New(h)
}

func parseLevel(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// contextHandler wraps a slog.Handler and injects fielder-derived attributes
// into every record before delegating.
type contextHandler struct {
	next      slog.Handler
	fielders  []Fielder
	baseAttrs []slog.Attr
}

func (h *contextHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *contextHandler) Handle(ctx context.Context, record slog.Record) error {
	record.AddAttrs(h.baseAttrs...)
	for _, f := range h.fielders {
		if attrs := f(ctx); len(attrs) > 0 {
			record.AddAttrs(attrs...)
		}
	}
	return h.next.Handle(ctx, record)
}

func (h *contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &contextHandler{next: h.next.WithAttrs(attrs), fielders: h.fielders, baseAttrs: h.baseAttrs}
}

func (h *contextHandler) WithGroup(name string) slog.Handler {
	return &contextHandler{next: h.next.WithGroup(name), fielders: h.fielders, baseAttrs: h.baseAttrs}
}

// secretKeys are redacted by Redact, matched case-insensitively against map
// keys. Extend by calling RedactKeys before Redact is used, or build your
// own redaction and pass logged bodies through it instead.
var secretKeys = map[string]struct{}{
	"password":      {},
	"secret":        {},
	"token":         {},
	"authorization": {},
	"apikey":        {},
	"api_key":       {},
	"access_token":  {},
	"refresh_token": {},
	"client_secret": {},
}

// RedactKeys adds additional key names (case-insensitive) to the set masked
// by Redact. Call it once at startup, not per-request.
func RedactKeys(keys ...string) {
	for _, k := range keys {
		secretKeys[strings.ToLower(k)] = struct{}{}
	}
}

// Redact returns a deep copy of v (expected to be the result of decoding a
// JSON body: map[string]any, []any, or scalars) with values under
// known-sensitive keys replaced by "********". It is intended for debug-level
// access logging of request bodies — never log raw, unredacted bodies.
func Redact(v any) any {
	switch val := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(val))
		for k, v := range val {
			if _, sensitive := secretKeys[strings.ToLower(k)]; sensitive {
				out[k] = "********"
				continue
			}
			out[k] = Redact(v)
		}
		return out
	case []any:
		out := make([]any, len(val))
		for i, v := range val {
			out[i] = Redact(v)
		}
		return out
	default:
		return val
	}
}
