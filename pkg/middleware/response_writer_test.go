package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStatusWriter_DefaultsTo200(t *testing.T) {
	sw := newStatusWriter(httptest.NewRecorder())

	// A handler that writes a body without calling WriteHeader gets an
	// implicit 200 from net/http; the wrapper has to report the same.
	if _, err := sw.Write([]byte("body")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if sw.status != http.StatusOK {
		t.Fatalf("status = %d, want 200 for an implicit header", sw.status)
	}
}

func TestStatusWriter_CapturesExplicitStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	sw := newStatusWriter(rec)

	sw.WriteHeader(http.StatusCreated)

	if sw.status != http.StatusCreated {
		t.Fatalf("captured status = %d, want 201", sw.status)
	}
	if rec.Code != http.StatusCreated {
		t.Fatalf("underlying writer got %d, want 201", rec.Code)
	}
}

// TestStatusWriter_FirstStatusWins matters because a second WriteHeader is
// what net/http would ignore (with a "superfluous WriteHeader" warning), so
// the status reported to logs and metrics must be the one actually sent.
func TestStatusWriter_FirstStatusWins(t *testing.T) {
	sw := newStatusWriter(httptest.NewRecorder())

	sw.WriteHeader(http.StatusNotFound)
	sw.WriteHeader(http.StatusInternalServerError)

	if sw.status != http.StatusNotFound {
		t.Fatalf("status = %d, want the first one written (404)", sw.status)
	}
}

func TestStatusWriter_CountsBytesAcrossWrites(t *testing.T) {
	rec := httptest.NewRecorder()
	sw := newStatusWriter(rec)

	for _, chunk := range []string{"hello", " ", "world"} {
		if _, err := sw.Write([]byte(chunk)); err != nil {
			t.Fatalf("Write(%q): %v", chunk, err)
		}
	}

	const want = len("hello world")
	if sw.bytes != want {
		t.Fatalf("bytes = %d, want %d", sw.bytes, want)
	}
	if rec.Body.String() != "hello world" {
		t.Fatalf("body = %q, want %q", rec.Body.String(), "hello world")
	}
}

func TestStatusWriter_WriteAfterExplicitHeaderKeepsStatus(t *testing.T) {
	sw := newStatusWriter(httptest.NewRecorder())

	sw.WriteHeader(http.StatusAccepted)
	_, _ = sw.Write([]byte("queued"))

	if sw.status != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 preserved across a later Write", sw.status)
	}
	if sw.bytes != len("queued") {
		t.Fatalf("bytes = %d, want %d", sw.bytes, len("queued"))
	}
}

// TestStatusWriter_UnwrapReachesTheRealWriter is why the wrapper is safe to
// put in the chain at all: http.ResponseController walks Unwrap to find
// Flusher/Hijacker, so without it a streaming or upgrading handler breaks
// behind the access-log and metrics middleware.
func TestStatusWriter_UnwrapReachesTheRealWriter(t *testing.T) {
	rec := httptest.NewRecorder()
	sw := newStatusWriter(rec)

	if sw.Unwrap() != http.ResponseWriter(rec) {
		t.Fatal("Unwrap did not return the underlying ResponseWriter")
	}

	// httptest.ResponseRecorder implements Flusher, so Flush must succeed
	// through the wrapper — the property ResponseController relies on.
	if err := http.NewResponseController(sw).Flush(); err != nil {
		t.Fatalf("Flush through the wrapper: %v", err)
	}
	if !rec.Flushed {
		t.Fatal("the underlying recorder was never flushed")
	}
}

func TestStatusWriter_HeaderPassesThrough(t *testing.T) {
	rec := httptest.NewRecorder()
	sw := newStatusWriter(rec)

	sw.Header().Set("X-Test", "value")

	if rec.Header().Get("X-Test") != "value" {
		t.Fatal("headers set through the wrapper did not reach the underlying writer")
	}
}
