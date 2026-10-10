package grpcx

import (
	"log/slog"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/reflection"
)

// NewServer builds a *grpc.Server with go-api's default interceptor chain:
// panic recovery (outermost), request-ID propagation, access logging,
// mTLS peer-identity extraction (only meaningful if deps.MTLS is set —
// see below; a no-op otherwise), then bearer-token authentication
// (innermost, only if deps.Verifier is set) — the same ordering
// rationale as pkg/app's HTTP middleware chain. OTel tracing/metrics are
// wired via a stats.Handler (otelgrpc), not an interceptor, per
// otelgrpc's current API.
//
// When deps.MTLS is set (via LoadMTLS), the server's transport itself
// becomes TLS, requiring or accepting a client certificate per
// TLSConfig.RequireClientCert — this is a ServerOption the package sets
// directly, not something passed via extraOpts, since getting it right
// (hot-reloadable via GetConfigForClient) isn't something to leave to
// every caller. See docs/grpc.md's mTLS section.
//
// When deps.Debug is true, server reflection is also registered — the
// gRPC equivalent of go-api only serving OpenAPI/docs in debug mode: it
// lets grpcurl and similar tools call your service without a copy of its
// .proto file, which is exactly the kind of discoverability you want
// locally and don't want to assume is safe to expose in production.
//
// Register your service implementations on the returned server, plus
// RegisterHealth if you want the same health.Checker-driven readiness
// story your HTTP side already has. extraOpts are appended after go-api's
// own options, for anything this package doesn't cover (keepalive
// policy, message size limits, or transport credentials if you need
// something other than plaintext/mTLS — e.g. server-only TLS).
func NewServer(deps Dependencies, extraOpts ...grpc.ServerOption) *grpc.Server {
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}

	interceptors := []grpc.UnaryServerInterceptor{
		recoveryInterceptor(logger, deps.Debug),
		requestIDInterceptor(),
		accessLogInterceptor(logger),
	}
	var serviceRoles map[string][]string
	if deps.MTLS != nil {
		serviceRoles = deps.MTLS.cfg.ServiceRoles
	}
	interceptors = append(interceptors, peerIdentityInterceptor(serviceRoles))
	if deps.Verifier != nil {
		interceptors = append(interceptors, authnInterceptor(deps.Verifier))
	}

	statsOpts := []otelgrpc.Option{}
	if deps.TracerProvider != nil {
		statsOpts = append(statsOpts, otelgrpc.WithTracerProvider(deps.TracerProvider))
	}
	if deps.MeterProvider != nil {
		statsOpts = append(statsOpts, otelgrpc.WithMeterProvider(deps.MeterProvider))
	}

	opts := []grpc.ServerOption{
		grpc.ChainUnaryInterceptor(interceptors...),
		grpc.StatsHandler(otelgrpc.NewServerHandler(statsOpts...)),
	}
	if deps.MTLS != nil {
		opts = append(opts, grpc.Creds(credentials.NewTLS(deps.MTLS.serverTLSConfig())))
	}
	opts = append(opts, extraOpts...)

	server := grpc.NewServer(opts...)
	if deps.Debug {
		reflection.Register(server)
	}
	return server
}
