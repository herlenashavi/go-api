// Package problem implements RFC 9457 (application/problem+json) error
// responses: every error a go-api service returns has the same envelope,
// carries the request ID and trace ID for support correlation, and never
// leaks internal error text unless debug mode is explicitly enabled.
package problem

import (
	"encoding/json"
	"errors"
	"net/http"

	"go.opentelemetry.io/otel/trace"

	"github.com/herlenashavi/go-api/pkg/reqctx"
)

// FieldError describes a single field-level validation failure.
type FieldError struct {
	Field  string `json:"field"`
	Detail string `json:"detail"`
}

// Problem is the RFC 9457 response body. Type is a stable, dereferenceable
// identifier for the error category (not necessarily a resolvable URL);
// clients should branch on Type, never on Detail.
type Problem struct {
	Type      string       `json:"type"`
	Title     string       `json:"title"`
	Status    int          `json:"status"`
	Detail    string       `json:"detail,omitempty"`
	Instance  string       `json:"instance,omitempty"`
	RequestID string       `json:"requestId,omitempty"`
	TraceID   string       `json:"traceId,omitempty"`
	Errors    []FieldError `json:"errors,omitempty"`

	// cause is the original error, kept for logging but never serialized.
	cause error
}

func (p *Problem) Error() string {
	if p.Detail != "" {
		return p.Title + ": " + p.Detail
	}
	return p.Title
}

// Unwrap exposes the wrapped cause, if any, to errors.As/errors.Is.
func (p *Problem) Unwrap() error { return p.cause }

// WithCause attaches the original error for logging purposes without
// exposing it to clients. Returns p for chaining.
func (p *Problem) WithCause(err error) *Problem {
	p.cause = err
	return p
}

// Cause returns the error passed to WithCause, or nil.
func (p *Problem) Cause() error { return p.cause }

const typeBase = "https://go-api.dev/problems/"

func new(status int, typeSuffix, title, detail string) *Problem {
	return &Problem{Type: typeBase + typeSuffix, Title: title, Status: status, Detail: detail}
}

// New builds a Problem for a status/category not covered by the named
// constructors below (e.g. a 503 from a timeout middleware). typeSuffix
// becomes the last path segment of Type.
func New(status int, typeSuffix, title, detail string) *Problem {
	return new(status, typeSuffix, title, detail)
}

func BadRequest(detail string) *Problem {
	return new(http.StatusBadRequest, "bad-request", "Bad Request", detail)
}

func Validation(errs ...FieldError) *Problem {
	p := new(http.StatusUnprocessableEntity, "validation-error", "Validation Failed", "One or more fields failed validation.")
	p.Errors = errs
	return p
}

func Unauthorized(detail string) *Problem {
	if detail == "" {
		detail = "Authentication is required to access this resource."
	}
	return new(http.StatusUnauthorized, "unauthorized", "Unauthorized", detail)
}

func Forbidden(detail string) *Problem {
	if detail == "" {
		detail = "You do not have permission to perform this action."
	}
	return new(http.StatusForbidden, "forbidden", "Forbidden", detail)
}

func NotFound(resource string) *Problem {
	detail := "The requested resource could not be found."
	if resource != "" {
		detail = resource + " could not be found."
	}
	return new(http.StatusNotFound, "not-found", "Not Found", detail)
}

func Conflict(detail string) *Problem {
	return new(http.StatusConflict, "conflict", "Conflict", detail)
}

func TooManyRequests(detail string) *Problem {
	if detail == "" {
		detail = "Rate limit exceeded. Retry later."
	}
	return new(http.StatusTooManyRequests, "rate-limited", "Too Many Requests", detail)
}

// Internal builds a 500 problem. detail is only ever sent to the client when
// debug is true (pass your service's config.Base.Debug); otherwise it is
// replaced with a generic message that points at the request ID, and the
// original error is retained via WithCause for server-side logging.
func Internal(err error, debug bool) *Problem {
	detail := "An unexpected error occurred. See the server logs for request " + "details."
	if debug && err != nil {
		detail = err.Error()
	}
	return new(http.StatusInternalServerError, "internal", "Internal Server Error", detail).WithCause(err)
}

// From maps an arbitrary error to a *Problem: if err already is (or wraps) a
// *Problem it is returned as-is; otherwise it is treated as an unexpected
// internal error.
func From(err error, debug bool) *Problem {
	var p *Problem
	if errors.As(err, &p) {
		return p
	}
	return Internal(err, debug)
}

// Write serializes p as application/problem+json, filling Instance from the
// request path and RequestID/TraceID from context if not already set.
func Write(w http.ResponseWriter, r *http.Request, p *Problem) {
	if p.Instance == "" {
		p.Instance = r.URL.Path
	}
	if p.RequestID == "" {
		p.RequestID = reqctx.RequestID(r.Context())
	}
	if p.TraceID == "" {
		if sc := trace.SpanContextFromContext(r.Context()); sc.HasTraceID() {
			p.TraceID = sc.TraceID().String()
		}
	}

	w.Header().Set("Content-Type", "application/problem+json; charset=utf-8")
	w.WriteHeader(p.Status)
	_ = json.NewEncoder(w).Encode(p)
}
