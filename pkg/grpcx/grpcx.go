// Package grpcx provides go-api's gRPC interservice-communication
// patterns: a server with the same observability/auth posture as
// pkg/app's HTTP stack (request ID, panic recovery, access logging, OTel
// tracing/metrics, bearer-token authentication), a client dial helper that
// propagates trace context, request ID, and (optionally) the caller's
// token downstream, and a gRPC health service backed by the same
// pkg/health.Checker your HTTP readiness endpoint already uses.
//
// Authorization is deliberately not a generic interceptor here: unlike
// chi routes, a gRPC service has no uniform place to hang a per-method
// (action, resource-type) mapping the way authz.Route does for HTTP. Call
// authz.Authorizer.Check/Authorize directly inside each RPC handler — the
// same escape hatch pkg/crud's hand-written routes use — using
// authn.FromContext(ctx) to get the Principal the server interceptor
// already placed there. See examples/villains for the pattern in practice.
package grpcx

import (
	"log/slog"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/herlenashavi/go-api/pkg/authn"
)

// requestIDMetadataKey is the gRPC metadata key equivalent of
// middleware.RequestIDHeader, so a request ID set upstream (by an HTTP
// gateway, or by a calling service) survives into this one.
const requestIDMetadataKey = "x-request-id"

// authorizationMetadataKey mirrors the HTTP Authorization header.
const authorizationMetadataKey = "authorization"

// Dependencies bundles what both NewServer and Dial need from the rest of
// the framework, so a service only has to assemble this once.
type Dependencies struct {
	Logger *slog.Logger
	// Tracer/Meter come from telemetry.Provider — pass the same instances
	// the HTTP side uses so gRPC and HTTP spans/metrics land in one
	// pipeline, not two.
	Tracer trace.Tracer
	Meter  metric.Meter
	// TracerProvider/MeterProvider are required separately from
	// Tracer/Meter because otelgrpc's stats handler takes a provider, not
	// an already-named Tracer/Meter — pass telemetry.Provider.TracerProvider
	// and .MeterProvider.
	TracerProvider trace.TracerProvider
	MeterProvider  metric.MeterProvider
	// Verifier authenticates incoming requests (server) if set; Dial
	// forwards a token (client) only if one is present in the outgoing
	// context (see authn.WithRawToken) regardless of this field.
	Verifier authn.Verifier
	// Debug controls whether panic values are included in the Internal
	// status message returned to callers (see Recovery).
	Debug bool
}
