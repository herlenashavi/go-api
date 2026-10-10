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
