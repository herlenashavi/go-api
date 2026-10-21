package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/herlenashavi/go-api/pkg/reqctx"
)

// captureRequestID runs h through RequestID and reports both the ID the
// handler saw in context and the one echoed on the response, which is the
// pair that has to agree for support correlation to work at all.
func captureRequestID(t *testing.T, req *http.Request) (fromContext, fromHeader string) {
	t.Helper()
	h := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fromContext = reqctx.RequestID(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return fromContext, rec.Header().Get(RequestIDHeader)
}

func TestRequestID_GeneratesWhenAbsent(t *testing.T) {
	ctxID, headerID := captureRequestID(t, httptest.NewRequest(http.MethodGet, "/", nil))

	if ctxID == "" {
		t.Fatal("no request ID was placed in the context")
	}
	if _, err := uuid.Parse(ctxID); err != nil {
		t.Fatalf("generated request ID %q is not a UUID: %v", ctxID, err)
	}
	if headerID != ctxID {
		t.Fatalf("echoed %s = %q, want it to match the context ID %q", RequestIDHeader, headerID, ctxID)
	}
}

// TestRequestID_ReusesInboundHeader is the end-to-end-correlation
// property: an ID assigned by a gateway or an upstream service has to
// survive into this service rather than being replaced, or a trace of one
// request spans two unrelated IDs.
func TestRequestID_ReusesInboundHeader(t *testing.T) {
	const upstream = "upstream-request-id-123"
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(RequestIDHeader, upstream)

	ctxID, headerID := captureRequestID(t, req)

	if ctxID != upstream {
		t.Fatalf("context ID = %q, want the inbound header value %q", ctxID, upstream)
	}
	if headerID != upstream {
		t.Fatalf("echoed %s = %q, want %q", RequestIDHeader, headerID, upstream)
	}
}

func TestRequestID_GeneratesAFreshIDPerRequest(t *testing.T) {
	first, _ := captureRequestID(t, httptest.NewRequest(http.MethodGet, "/", nil))
	second, _ := captureRequestID(t, httptest.NewRequest(http.MethodGet, "/", nil))

	if first == second {
		t.Fatalf("two requests got the same generated ID %q", first)
	}
}

// TestRequestIDLogFields_AttachesIDFromContext closes the loop between
// this middleware and pkg/logging: the ID is only useful if every log line
// written during the request carries it, which is this Fielder's job (it's
// wired into logging.New by app.New).
func TestRequestIDLogFields_AttachesIDFromContext(t *testing.T) {
	ctx := reqctx.WithRequestID(context.Background(), "req-abc-123")

	attrs := RequestIDLogFields(ctx)
	if len(attrs) != 1 {
		t.Fatalf("got %d attrs, want 1: %+v", len(attrs), attrs)
	}
	if attrs[0].Key != "request_id" {
		t.Errorf("key = %q, want request_id", attrs[0].Key)
	}
	if got := attrs[0].Value.String(); got != "req-abc-123" {
		t.Errorf("value = %q, want the context's request ID", got)
	}
}

// TestRequestIDLogFields_NilWithoutAnID covers the contract
// logging.Fielder sets out: a Fielder observing an absent value returns
// nil rather than an empty attribute, so log lines from outside a request
// (startup, shutdown) don't carry a blank request_id field.
func TestRequestIDLogFields_NilWithoutAnID(t *testing.T) {
	if attrs := RequestIDLogFields(context.Background()); attrs != nil {
		t.Fatalf("got %+v, want nil when no request ID is in context", attrs)
	}
}

// TestRequestID_ReplacesInvalidInboundHeader: the inbound ID is
// caller-controlled but echoed, logged on every line, and forwarded
// downstream, so anything oversized or outside reqctx.ValidRequestID's
// alphabet must be swapped for a generated one, not trusted.
func TestRequestID_ReplacesInvalidInboundHeader(t *testing.T) {
	for name, bad := range map[string]string{
		"oversized":     strings.Repeat("a", 129),
		"control chars": "abc\x01def",
		"space":         "abc def",
	} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set(RequestIDHeader, bad)

			ctxID, headerID := captureRequestID(t, req)

			if ctxID == bad || headerID == bad {
				t.Fatalf("invalid inbound ID %q was kept", bad)
			}
			if _, err := uuid.Parse(ctxID); err != nil {
				t.Fatalf("replacement ID %q is not a UUID: %v", ctxID, err)
			}
			if headerID != ctxID {
				t.Fatalf("echoed ID %q != context ID %q", headerID, ctxID)
			}
		})
	}
}
