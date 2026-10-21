// Package logging configures structured JSON logging on top of the
// standard library's log/slog — every service built on go-api logs JSON,
// unconditionally; there is no "pretty text for local dev" mode to
// accidentally ship, because production logging pipelines (and this
// package's own PII filter — see pii.go) are built against structured
// fields, not a human-formatted line.
//
// It deliberately avoids global mutable state: callers compose a logger
// from explicit "field functions" (see Fielder) that pull values — a
// trace ID, a request ID, an authenticated principal — out of a
// context.Context. Other go-api packages (telemetry, middleware, authn)
// each expose their own Fielder; app.New wires them together once at
// startup. logging itself knows nothing about traces, requests, or
// principals.
//
// Every record passing through a logger built by New — regardless of
// level, regardless of which package or handler logged it — is run
// through the PII-redacting handler in pii.go before it reaches the
// JSON encoder. That is the whole point of building it into New rather
// than leaving it as an opt-in helper call sites have to remember to
// use: a log line can't skip it by omission.
package logging

import (
	"context"
	"io"
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
	// ServiceName and Env are attached to every log line.
	ServiceName string
	Env         string
	// Output defaults to os.Stdout. Any io.Writer works — a bytes.Buffer
	// in a test, for instance — not just a real file.
	Output io.Writer
}

// New builds the process-wide *slog.Logger: JSON output, every record
// passed through the PII/secret-redacting handler (pii.go) before
// encoding, and enriched with whatever fielders supply from context.
// fielders are consulted, in order, on every log record; pass none for a
// context-agnostic logger (e.g. in tests).
func New(cfg Config, fielders ...Fielder) *slog.Logger {
	out := cfg.Output
	if out == nil {
		out = os.Stdout
	}

	opts := &slog.HandlerOptions{Level: parseLevel(cfg.Level)}
	base := slog.NewJSONHandler(out, opts)
	redacting := &piiHandler{next: base}

	h := &contextHandler{
		next:     redacting,
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

// contextHandler wraps a slog.Handler and injects fielder-derived
// attributes into every record before delegating to it — in New's chain,
// that delegate is piiHandler, so fielder-supplied attributes get
// redaction-scanned exactly like everything else. It does not redact
// anything itself; that's piiHandler's job, kept separate so this type
// stays about one thing (context enrichment).
//
// It tracks the logger's With/WithGroup chain itself (in scopes) rather
// than delegating it to next. Delegating would open the groups on next,
// so service/env and fielder attrs (request_id, trace_id, actor) added at
// Handle time would land *inside* whatever group was open —
// logger.WithGroup("db") would bury request_id under "db" and break every
// log query that filters on it. Instead Handle nests the caller's attrs
// under their groups itself and adds the correlation attrs at the root.
type contextHandler struct {
	next      slog.Handler
	fielders  []Fielder
	baseAttrs []slog.Attr
	scopes    []scope
}

// scope is one With or WithGroup call, in the order they were made: a
// group name opens a group, attrs are bound at the current nesting level.
type scope struct {
	group string
	attrs []slog.Attr
}

func (h *contextHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *contextHandler) Handle(ctx context.Context, record slog.Record) error {
	attrs := make([]slog.Attr, 0, record.NumAttrs())
	record.Attrs(func(a slog.Attr) bool {
		attrs = append(attrs, a)
		return true
	})
	// Fold the scopes innermost-first: attrs bound by With prepend at their
	// level, a group wraps everything nested beneath it. An empty group is
	// dropped, matching slog's own handlers.
	for i := len(h.scopes) - 1; i >= 0; i-- {
		s := h.scopes[i]
		if s.group == "" {
			attrs = append(append([]slog.Attr{}, s.attrs...), attrs...)
			continue
		}
		if len(attrs) == 0 {
			continue
		}
		attrs = []slog.Attr{{Key: s.group, Value: slog.GroupValue(attrs...)}}
	}

	out := slog.NewRecord(record.Time, record.Level, record.Message, record.PC)
	out.AddAttrs(attrs...)
	out.AddAttrs(h.baseAttrs...)
	for _, f := range h.fielders {
		if fa := f(ctx); len(fa) > 0 {
			out.AddAttrs(fa...)
		}
	}
	return h.next.Handle(ctx, out)
}

func (h *contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	return h.withScope(scope{attrs: attrs})
}

func (h *contextHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	return h.withScope(scope{group: name})
}

func (h *contextHandler) withScope(s scope) *contextHandler {
	scopes := make([]scope, len(h.scopes), len(h.scopes)+1)
	copy(scopes, h.scopes)
	return &contextHandler{
		next:      h.next,
		fielders:  h.fielders,
		baseAttrs: h.baseAttrs,
		scopes:    append(scopes, s),
	}
}

// secretKeys are redacted by Redact, matched case-insensitively against map
// keys. Extend by calling RedactKeys before Redact is used, or build your
// own redaction and pass logged bodies through it instead.
//
// This is a narrower, older mechanism than pii.go's always-on handler —
// kept because middleware.AccessLog's optional request-body logging
// needs to redact a *parsed JSON body* (map[string]any/[]any) before
// handing it to slog.Any, not a string slog is about to encode; pii.go's
// handler still gets a second, independent pass at the result (its own
// sensitive-key set is deliberately a superset of this one), so a field
// name missing from this list but present in pii.go's is still caught.
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
