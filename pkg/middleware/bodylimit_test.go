package middleware

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// readBodyThrough runs a request with the given body through BodyLimit and
// reports what the handler managed to read and the error it got, which is
// the contract httpx.Decode depends on to produce a 413.
func readBodyThrough(t *testing.T, maxBytes int64, body string) (int, error) {
	t.Helper()
	var (
		n      int
		readEr error
	)
	h := BodyLimit(maxBytes)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var read []byte
		read, readEr = io.ReadAll(r.Body)
		n = len(read)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)))
	return n, readEr
}

func TestBodyLimit_UnderLimitReadsFully(t *testing.T) {
	body := strings.Repeat("a", 100)

	n, err := readBodyThrough(t, 1024, body)
	if err != nil {
		t.Fatalf("unexpected read error under the limit: %v", err)
	}
	if n != len(body) {
		t.Fatalf("read %d bytes, want %d", n, len(body))
	}
}

// TestBodyLimit_OverLimitYieldsMaxBytesError pins the specific error type
// rather than just "an error": pkg/httpx matches *http.MaxBytesError via
// errors.As to turn an oversized body into a 413 problem+json, so a
// different error type here would silently degrade that to a generic 400.
func TestBodyLimit_OverLimitYieldsMaxBytesError(t *testing.T) {
	const limit = 64

	_, err := readBodyThrough(t, limit, strings.Repeat("a", limit*2))
	if err == nil {
		t.Fatal("reading past the limit should fail")
	}

	var maxErr *http.MaxBytesError
	if !errors.As(err, &maxErr) {
		t.Fatalf("error = %v (%T), want an *http.MaxBytesError so httpx can map it to 413", err, err)
	}
	if maxErr.Limit != limit {
		t.Fatalf("MaxBytesError.Limit = %d, want %d — httpx reports this value to the client", maxErr.Limit, limit)
	}
}

func TestBodyLimit_ExactlyAtLimitIsAllowed(t *testing.T) {
	const limit = 64

	n, err := readBodyThrough(t, limit, strings.Repeat("a", limit))
	if err != nil {
		t.Fatalf("a body exactly at the limit should be readable, got: %v", err)
	}
	if n != limit {
		t.Fatalf("read %d bytes, want %d", n, limit)
	}
}

// TestBodyLimit_NonPositiveDisablesTheLimit covers the documented escape
// hatch, including that it returns next unwrapped rather than installing a
// pass-through reader.
func TestBodyLimit_NonPositiveDisablesTheLimit(t *testing.T) {
	for _, limit := range []int64{0, -1} {
		body := strings.Repeat("a", 4096)

		n, err := readBodyThrough(t, limit, body)
		if err != nil {
			t.Errorf("limit %d: unexpected error with the limit disabled: %v", limit, err)
		}
		if n != len(body) {
			t.Errorf("limit %d: read %d bytes, want the full %d", limit, n, len(body))
		}
	}
}
