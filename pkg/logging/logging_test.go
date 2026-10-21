package logging_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/herlenashavi/go-api/pkg/logging"
)

// decodeLines parses each line of buf as a separate JSON object — the
// shape slog.NewJSONHandler produces, one object per Handle call.
func decodeLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var lines []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("log line is not valid JSON: %v\nline: %s", err, line)
		}
		lines = append(lines, m)
	}
	return lines
}

func TestNew_ProducesJSON(t *testing.T) {
	var buf bytes.Buffer
	logger := logging.New(logging.Config{
		Level:       "info",
		ServiceName: "test-service",
		Env:         "test",
		Output:      &buf,
	})

	logger.Info("hello", "foo", "bar")

	lines := decodeLines(t, &buf)
	if len(lines) != 1 {
		t.Fatalf("got %d log lines, want 1", len(lines))
	}
	line := lines[0]
	if line["msg"] != "hello" {
		t.Fatalf("msg = %v, want %q", line["msg"], "hello")
	}
	if line["foo"] != "bar" {
		t.Fatalf("foo = %v, want %q", line["foo"], "bar")
	}
	if line["service"] != "test-service" || line["env"] != "test" {
		t.Fatalf("missing/wrong base attrs: %+v", line)
	}
}

func TestNew_FieldersEnrichEveryRecord(t *testing.T) {
	var buf bytes.Buffer
	fielder := func(ctx context.Context) []slog.Attr {
		return []slog.Attr{slog.String("trace_id", "abc123")}
	}
	logger := logging.New(logging.Config{Output: &buf}, fielder)

	logger.InfoContext(context.Background(), "hi")

	lines := decodeLines(t, &buf)
	if lines[0]["trace_id"] != "abc123" {
		t.Fatalf("got %+v, want trace_id=abc123", lines[0])
	}
}

// TestNew_CorrelationAttrsStayAtRootUnderWithGroup guards the attrs every
// log query filters on: a logger with an open group must still put
// service/env and fielder attrs at the top level, nesting only the
// caller's own attrs under the group.
func TestNew_CorrelationAttrsStayAtRootUnderWithGroup(t *testing.T) {
	var buf bytes.Buffer
	fielder := func(context.Context) []slog.Attr {
		return []slog.Attr{slog.String("request_id", "req-1")}
	}
	logger := logging.New(logging.Config{ServiceName: "svc", Env: "test", Output: &buf}, fielder)

	logger.WithGroup("db").InfoContext(context.Background(), "query", "q", "select 1")

	line := decodeLines(t, &buf)[0]
	if line["request_id"] != "req-1" || line["service"] != "svc" || line["env"] != "test" {
		t.Fatalf("correlation attrs not at root: %+v", line)
	}
	db, ok := line["db"].(map[string]any)
	if !ok {
		t.Fatalf("expected a db group, got %+v", line)
	}
	if db["q"] != "select 1" {
		t.Fatalf("db.q = %v, want the caller's attr nested under the group", db["q"])
	}
	for _, k := range []string{"request_id", "service", "env"} {
		if _, nested := db[k]; nested {
			t.Fatalf("%s leaked into the db group: %+v", k, db)
		}
	}
}

// TestNew_WithAndWithGroupNestInCallOrder checks attrs bound by With land
// at the nesting level that was open when With was called.
func TestNew_WithAndWithGroupNestInCallOrder(t *testing.T) {
	var buf bytes.Buffer
	logger := logging.New(logging.Config{ServiceName: "svc", Output: &buf})

	logger.With("a", "1").WithGroup("g").With("b", "2").WithGroup("h").Info("msg", "c", "3")

	line := decodeLines(t, &buf)[0]
	if line["a"] != "1" || line["service"] != "svc" {
		t.Fatalf("root attrs wrong: %+v", line)
	}
	g, ok := line["g"].(map[string]any)
	if !ok || g["b"] != "2" {
		t.Fatalf("g = %+v, want b=2", line["g"])
	}
	h, ok := g["h"].(map[string]any)
	if !ok || h["c"] != "3" {
		t.Fatalf("g.h = %+v, want c=3", g["h"])
	}
}

// TestNew_EmptyGroupIsOmitted matches slog's own handlers: a group with
// nothing in it doesn't produce an empty object.
func TestNew_EmptyGroupIsOmitted(t *testing.T) {
	var buf bytes.Buffer
	logger := logging.New(logging.Config{Output: &buf})

	logger.WithGroup("empty").Info("msg")

	line := decodeLines(t, &buf)[0]
	if _, ok := line["empty"]; ok {
		t.Fatalf("empty group should be omitted: %+v", line)
	}
}

func TestParseLevel_RespectsConfiguredLevel(t *testing.T) {
	var buf bytes.Buffer
	logger := logging.New(logging.Config{Level: "warn", Output: &buf})

	logger.Info("should be dropped")
	logger.Warn("should appear")

	lines := decodeLines(t, &buf)
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1 (info should be below the warn threshold)", len(lines))
	}
	if lines[0]["msg"] != "should appear" {
		t.Fatalf("got %+v", lines[0])
	}
}

func TestRedact_StripsKnownSecretKeys(t *testing.T) {
	body := map[string]any{
		"username": "alice",
		"password": "super-secret",
		"nested":   map[string]any{"token": "abc", "ok": "fine"},
		"list":     []any{map[string]any{"secret": "x"}, "plain"},
	}
	got := logging.Redact(body).(map[string]any)

	if got["username"] != "alice" {
		t.Fatalf("username should pass through unchanged, got %v", got["username"])
	}
	if got["password"] != "********" {
		t.Fatalf("password = %v, want masked", got["password"])
	}
	nested := got["nested"].(map[string]any)
	if nested["token"] != "********" || nested["ok"] != "fine" {
		t.Fatalf("nested = %+v", nested)
	}
	list := got["list"].([]any)
	if list[0].(map[string]any)["secret"] != "********" || list[1] != "plain" {
		t.Fatalf("list = %+v", list)
	}
}

func TestRedactKeys_ExtendsTheSet(t *testing.T) {
	logging.RedactKeys("internal_id")
	got := logging.Redact(map[string]any{"internal_id": "12345", "other": "x"}).(map[string]any)
	if got["internal_id"] != "********" {
		t.Fatalf("internal_id = %v, want masked after RedactKeys", got["internal_id"])
	}
	if got["other"] != "x" {
		t.Fatalf("other = %v, want unchanged", got["other"])
	}
}
