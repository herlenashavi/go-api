package middleware

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func jsonLogger(buf *bytes.Buffer, level slog.Level) *slog.Logger {
	return slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: level}))
}

func logLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var lines []map[string]any
	for _, raw := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if raw == "" {
			continue
		}
		var line map[string]any
		if err := json.Unmarshal([]byte(raw), &line); err != nil {
			t.Fatalf("log line is not valid JSON: %v\nline: %s", err, raw)
		}
		lines = append(lines, line)
	}
	return lines
}

// findLine returns the first log line whose msg matches, failing the test
// if there is none — more useful than indexing, since request-body logging
// adds a second line only in some configurations.
func findLine(t *testing.T, lines []map[string]any, msg string) map[string]any {
	t.Helper()
	for _, line := range lines {
		if line["msg"] == msg {
			return line
		}
	}
	t.Fatalf("no log line with msg = %q; got %+v", msg, lines)
	return nil
}

func TestAccessLog_LogsOneLinePerRequest(t *testing.T) {
	var buf bytes.Buffer
	h := AccessLog(AccessLogConfig{Logger: jsonLogger(&buf, slog.LevelInfo)})(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte("hello"))
		}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/heroes", nil))

	lines := logLines(t, &buf)
	if len(lines) != 1 {
		t.Fatalf("got %d log lines, want exactly 1: %+v", len(lines), lines)
	}
	line := lines[0]
	if line["msg"] != "request completed" {
		t.Fatalf("msg = %v", line["msg"])
	}
	if line["method"] != http.MethodPost {
		t.Fatalf("method = %v, want POST", line["method"])
	}
	if line["status"] != float64(http.StatusCreated) {
		t.Fatalf("status = %v, want 201", line["status"])
	}
	if line["bytes"] != float64(len("hello")) {
		t.Fatalf("bytes = %v, want %d", line["bytes"], len("hello"))
	}
	if _, ok := line["duration_ms"]; !ok {
		t.Fatalf("missing duration_ms: %+v", line)
	}
}

// TestAccessLog_LogsRoutePatternNotResolvedPath is the log-cardinality
// property: logging the resolved path would put one distinct value per
// hero ID into the logs (and into anything built on them), which is
// exactly what the route pattern avoids.
func TestAccessLog_LogsRoutePatternNotResolvedPath(t *testing.T) {
	var buf bytes.Buffer
	r := chi.NewRouter()
	r.Use(AccessLog(AccessLogConfig{Logger: jsonLogger(&buf, slog.LevelInfo)}))
	r.Get("/v1/heroes/{id}", func(w http.ResponseWriter, r *http.Request) {})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/heroes/abc-123", nil))

	line := findLine(t, logLines(t, &buf), "request completed")
	if line["route"] != "/v1/heroes/{id}" {
		t.Fatalf("route = %v, want the chi pattern %q", line["route"], "/v1/heroes/{id}")
	}
}

// TestAccessLog_FallsBackToPathWithoutRoutingMetadata covers routePattern's
// non-chi path — a handler tested directly, or mounted on a plain
// http.ServeMux — where there is no pattern to report.
func TestAccessLog_FallsBackToPathWithoutRoutingMetadata(t *testing.T) {
	var buf bytes.Buffer
	h := AccessLog(AccessLogConfig{Logger: jsonLogger(&buf, slog.LevelInfo)})(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/no/chi/here", nil))

	line := findLine(t, logLines(t, &buf), "request completed")
	if line["route"] != "/no/chi/here" {
		t.Fatalf("route = %v, want the raw path as a fallback", line["route"])
	}
}

func TestAccessLog_IgnorePathsSkipsLoggingEntirely(t *testing.T) {
	var buf bytes.Buffer
	var served bool
	h := AccessLog(AccessLogConfig{
		Logger:      jsonLogger(&buf, slog.LevelDebug),
		IgnorePaths: []string{"/health"},
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { served = true }))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health/liveness", nil))

	if !served {
		t.Fatal("an ignored path should still reach the handler")
	}
	if got := strings.TrimSpace(buf.String()); got != "" {
		t.Fatalf("ignored path produced log output: %s", got)
	}
}

// TestAccessLog_IgnorePathsAppliesAtDebugLevelToo pins the behavior the
// config's doc comment promises: turning the log level up must not defeat
// the ignore list, or /health/liveness floods the logs again.
func TestAccessLog_IgnorePathsAppliesAtDebugLevelToo(t *testing.T) {
	var buf bytes.Buffer
	h := AccessLog(AccessLogConfig{
		Logger:         jsonLogger(&buf, slog.LevelDebug),
		IgnorePaths:    []string{"/health"},
		LogRequestBody: true,
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	req := httptest.NewRequest(http.MethodPost, "/health/readiness", strings.NewReader(`{"a":1}`))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := strings.TrimSpace(buf.String()); got != "" {
		t.Fatalf("ignored path produced log output at debug level: %s", got)
	}
}

// TestAccessLog_RequestBodyReachesTheHandlerIntact is the regression test
// for the subtlest failure mode in this package: logRequestBody consumes
// r.Body and has to put it back. If it doesn't, every logged request hands
// its handler an empty body — and only under the one log level and
// content type that trigger it, so it is easy to ship unnoticed.
func TestAccessLog_RequestBodyReachesTheHandlerIntact(t *testing.T) {
	const body = `{"name":"Clark Kent","password":"hunter2"}`

	var buf bytes.Buffer
	var seenByHandler string
	h := AccessLog(AccessLogConfig{
		Logger:         jsonLogger(&buf, slog.LevelDebug),
		LogRequestBody: true,
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		read, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("handler could not read the body: %v", err)
		}
		seenByHandler = string(read)
	}))

	req := httptest.NewRequest(http.MethodPost, "/v1/heroes", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if seenByHandler != body {
		t.Fatalf("handler saw body %q, want the complete original %q", seenByHandler, body)
	}
}

// TestAccessLog_RequestBodyIsRedacted checks the redaction pass
// logRequestBody applies before handing the parsed body to slog. The
// always-on handler in pkg/logging is a second, independent net; this
// asserts the first one, using a plain JSON handler so only this pass is
// in play.
func TestAccessLog_RequestBodyIsRedacted(t *testing.T) {
	var buf bytes.Buffer
	h := AccessLog(AccessLogConfig{
		Logger:         jsonLogger(&buf, slog.LevelDebug),
		LogRequestBody: true,
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	req := httptest.NewRequest(http.MethodPost, "/v1/heroes",
		strings.NewReader(`{"name":"Clark Kent","password":"hunter2","nested":{"token":"abc"}}`))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	line := findLine(t, logLines(t, &buf), "request body")
	logged, ok := line["body"].(map[string]any)
	if !ok {
		t.Fatalf("body did not decode as an object: %+v", line["body"])
	}
	if logged["password"] == "hunter2" {
		t.Fatal("password was logged unredacted")
	}
	if nested := logged["nested"].(map[string]any); nested["token"] == "abc" {
		t.Fatal("nested token was logged unredacted")
	}
	if logged["name"] != "Clark Kent" {
		t.Fatalf("name = %v, want it to survive redaction (not a sensitive key)", logged["name"])
	}
}

func TestAccessLog_RequestBodyOnlyLoggedForJSON(t *testing.T) {
	cases := []struct {
		contentType string
		wantLogged  bool
	}{
		{"application/json", true},
		{"application/json; charset=utf-8", true},
		{"text/plain", false},
		{"application/octet-stream", false},
		{"", false},
	}
	for _, tc := range cases {
		name := tc.contentType
		if name == "" {
			name = "absent"
		}
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			h := AccessLog(AccessLogConfig{
				Logger:         jsonLogger(&buf, slog.LevelDebug),
				LogRequestBody: true,
			})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

			req := httptest.NewRequest(http.MethodPost, "/v1/heroes", strings.NewReader(`{"a":1}`))
			if tc.contentType != "" {
				req.Header.Set("Content-Type", tc.contentType)
			}

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			logged := strings.Contains(buf.String(), "request body")
			if logged != tc.wantLogged {
				t.Fatalf("body logged = %v, want %v for Content-Type %q", logged, tc.wantLogged, tc.contentType)
			}
		})
	}
}

// TestAccessLog_RequestBodyNotLoggedBelowDebug confirms the level gate:
// LogRequestBody is wired from config.Base.Debug, but the level is a
// separate setting, and bodies must not appear at info.
func TestAccessLog_RequestBodyNotLoggedBelowDebug(t *testing.T) {
	var buf bytes.Buffer
	h := AccessLog(AccessLogConfig{
		Logger:         jsonLogger(&buf, slog.LevelInfo),
		LogRequestBody: true,
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	req := httptest.NewRequest(http.MethodPost, "/v1/heroes", strings.NewReader(`{"a":1}`))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if strings.Contains(buf.String(), "request body") {
		t.Fatalf("body logged at info level: %s", buf.String())
	}
}

// TestAccessLog_OversizedBodyStillReachesHandler covers the
// maxLoggedBodyBytes branch: a body too large to log must still be
// restored in full, since truncating what the handler receives would
// corrupt the request rather than just skip a log line.
func TestAccessLog_OversizedBodyStillReachesHandler(t *testing.T) {
	big := `{"blob":"` + strings.Repeat("x", maxLoggedBodyBytes) + `"}`

	var buf bytes.Buffer
	var gotLen int
	h := AccessLog(AccessLogConfig{
		Logger:         jsonLogger(&buf, slog.LevelDebug),
		LogRequestBody: true,
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		read, _ := io.ReadAll(r.Body)
		gotLen = len(read)
	}))

	req := httptest.NewRequest(http.MethodPost, "/v1/heroes", strings.NewReader(big))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if gotLen != len(big) {
		t.Fatalf("handler read %d bytes, want the full %d", gotLen, len(big))
	}
	if strings.Contains(buf.String(), "request body") {
		t.Fatal("a body over maxLoggedBodyBytes should not be logged")
	}
}

func TestAccessLog_MalformedJSONBodyIsNotLoggedButStillReadable(t *testing.T) {
	const body = `{"name": not valid json`

	var buf bytes.Buffer
	var seenByHandler string
	h := AccessLog(AccessLogConfig{
		Logger:         jsonLogger(&buf, slog.LevelDebug),
		LogRequestBody: true,
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		read, _ := io.ReadAll(r.Body)
		seenByHandler = string(read)
	}))

	req := httptest.NewRequest(http.MethodPost, "/v1/heroes", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if seenByHandler != body {
		t.Fatalf("handler saw %q, want %q — a malformed body must still be restored", seenByHandler, body)
	}
	if strings.Contains(buf.String(), "request body") {
		t.Fatal("unparseable JSON should not be logged as a body")
	}
}

func TestAccessLog_NilLoggerFallsBackToDefault(t *testing.T) {
	h := AccessLog(AccessLogConfig{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestShouldIgnore(t *testing.T) {
	cases := []struct {
		path    string
		ignores []string
		want    bool
	}{
		{"/health/liveness", []string{"/health"}, true},
		{"/v1/health-records", []string{"/health"}, true}, // substring match, documented
		{"/v1/heroes", []string{"/health"}, false},
		{"/v1/heroes", nil, false},
		{"/metrics", []string{"/health", "/metrics"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			if got := shouldIgnore(tc.path, tc.ignores); got != tc.want {
				t.Fatalf("shouldIgnore(%q, %v) = %v, want %v", tc.path, tc.ignores, got, tc.want)
			}
		})
	}
}
