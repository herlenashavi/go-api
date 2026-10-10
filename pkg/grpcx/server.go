package grpcx

import (
	"log/slog"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

// NewServer builds a *grpc.Server with go-api's default interceptor chain:
// panic recovery (outermost), request-ID propagation, access logging,
// then bearer-token authentication (innermost, only if deps.Verifier is
// set) — the same ordering rationale as pkg/app's HTTP middleware chain.
// OTel tracing/metrics are wired via a stats.Handler (otelgrpc), not an
// interceptor, per otelgrpc's current API.
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
// own options, for anything this package doesn't cover (TLS credentials,
// keepalive policy, message size limits).
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

	opts := append([]grpc.ServerOption{
		grpc.ChainUnaryInterceptor(interceptors...),
		grpc.StatsHandler(otelgrpc.NewServerHandler(statsOpts...)),
	}, extraOpts...)

	server := grpc.NewServer(opts...)
	if deps.Debug {
		reflection.Register(server)
	}
	return server
}
