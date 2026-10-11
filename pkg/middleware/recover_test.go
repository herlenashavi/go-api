package middleware

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func panickingHandler(value any) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic(value)
	})
}

// decodeProblem parses a response body as an RFC 9457 problem document.
// Recover's whole purpose is that a panic produces one of these rather
// than net/http's bare, bodyless 500, so the assertions below check the
// envelope and not just the status code.
func decodeProblem(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/problem+json") {
		t.Fatalf("Content-Type = %q, want application/problem+json", ct)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response body is not valid JSON: %v\nbody: %s", err, rec.Body.String())
	}
	return body
}

func TestRecover_PanicBecomesProblemJSON(t *testing.T) {
	h := Recover(slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)), false)(panickingHandler("boom"))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/heroes", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	body := decodeProblem(t, rec)
	if body["status"] != float64(http.StatusInternalServerError) {
		t.Fatalf("problem.status = %v, want 500", body["status"])
	}
	if body["instance"] != "/v1/heroes" {
		t.Fatalf("problem.instance = %v, want the request path", body["instance"])
	}
}

// TestRecover_PanicValueHiddenUnlessDebug is the information-disclosure
// check: a panic message can contain anything (a DSN, a struct dump), so
// it must never reach the client outside debug mode.
func TestRecover_PanicValueHiddenUnlessDebug(t *testing.T) {
	const secret = "postgres://user:hunter2@db/app"

	t.Run("debug off", func(t *testing.T) {
		h := Recover(slog.New(slog.DiscardHandler), false)(panickingHandler(secret))

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

		if strings.Contains(rec.Body.String(), "hunter2") {
			t.Fatalf("panic value leaked to the client: %s", rec.Body.String())
		}
	})

	t.Run("debug on", func(t *testing.T) {
		h := Recover(slog.New(slog.DiscardHandler), true)(panickingHandler(secret))

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

		if !strings.Contains(rec.Body.String(), "hunter2") {
			t.Fatalf("debug mode should surface the panic detail, got: %s", rec.Body.String())
		}
	})
}

// TestRecover_LogsPanicWithStack pins the other half of recovery: the
// client gets an opaque 500, so the stack trace has to be in the logs or
// the panic is effectively invisible.
func TestRecover_LogsPanicWithStack(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	h := Recover(logger, false)(panickingHandler("boom"))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	var line map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &line); err != nil {
		t.Fatalf("log line is not valid JSON: %v\nline: %s", err, buf.String())
	}
	if line["level"] != "ERROR" {
		t.Fatalf("level = %v, want ERROR", line["level"])
	}
	stack, ok := line["stack"].(string)
	if !ok || !strings.Contains(stack, "panic") {
		t.Fatalf("log line is missing a stack trace: %+v", line)
	}
	if msg, _ := line["error"].(string); !strings.Contains(msg, "boom") {
		t.Fatalf("error = %v, want the panic value", line["error"])
	}
}

func TestRecover_NilLoggerFallsBackToDefault(t *testing.T) {
	// Passing a nil logger must not itself panic inside the recovery path,
	// which would turn a handled panic into a process-killing one.
	h := Recover(nil, false)(panickingHandler("boom"))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestRecover_PassesThroughWhenNoPanic(t *testing.T) {
	h := Recover(slog.New(slog.DiscardHandler), false)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("fine"))
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusTeapot || rec.Body.String() != "fine" {
		t.Fatalf("status = %d, body = %q; want 418/%q", rec.Code, rec.Body.String(), "fine")
	}
}
