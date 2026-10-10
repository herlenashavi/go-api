package grpcx

import (
	"context"
	"net"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	"github.com/herlenashavi/go-api/pkg/authn"
	"github.com/herlenashavi/go-api/pkg/reqctx"
)

// Dial builds a *grpc.ClientConn to target with go-api's default client
// behavior: OTel tracing/metrics via the same providers as the rest of the
// service (deps.TracerProvider/MeterProvider), the calling request's ID
// forwarded so logs correlate end-to-end across the service boundary,
// and — only if the calling context actually carries one via
// authn.WithRawToken — the original caller's bearer token forwarded too
// (see that function's doc comment on when that is and isn't
// appropriate).
//
// Connects without TLS by default (insecure.NewCredentials()), which is
// fine for same-cluster/same-host calls in an example or a service mesh
// that terminates TLS itself. If deps.MTLS is set (via LoadMTLS), it
// connects with mutual TLS instead: this service's own certificate is
// presented, and target's host is verified against the CA in
// TLSConfig.CAFile — see docs/grpc.md. Either way, pass
// grpc.WithTransportCredentials via extraOpts to override — DialOptions
// apply in order, so a later one wins.
//
// Per modern grpc-go, Dial does not block: the connection is established
// lazily on first use, so a reachability problem surfaces on the first
// RPC call, not here.
func Dial(target string, deps Dependencies, extraOpts ...grpc.DialOption) (*grpc.ClientConn, error) {
	statsOpts := []otelgrpc.Option{}
	if deps.TracerProvider != nil {
		statsOpts = append(statsOpts, otelgrpc.WithTracerProvider(deps.TracerProvider))
	}
	if deps.MeterProvider != nil {
		statsOpts = append(statsOpts, otelgrpc.WithMeterProvider(deps.MeterProvider))
	}

	transportCreds := insecure.NewCredentials()
	if deps.MTLS != nil {
		transportCreds = credentials.NewTLS(deps.MTLS.clientTLSConfig(targetHost(target)))
	}

	opts := append([]grpc.DialOption{
		grpc.WithTransportCredentials(transportCreds),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler(statsOpts...)),
		grpc.WithChainUnaryInterceptor(forwardingInterceptor()),
	}, extraOpts...)

	return grpc.NewClient(target, opts...)
}

// targetHost strips the port off a "host:port" dial target for use as
// the TLS ServerName (SNI + the name the peer's certificate is checked
// against). Falls back to the target as-is if it isn't in that shape —
// e.g. a custom grpc resolver scheme — rather than failing Dial itself;
// a genuinely wrong ServerName surfaces as a handshake failure on the
// first call, same as any other unreachable-target problem.
func targetHost(target string) string {
	host, _, err := net.SplitHostPort(target)
	if err != nil {
		return target
	}
	return host
}

// forwardingInterceptor carries the calling context's request ID and
// (optional) raw bearer token onto the outgoing RPC's metadata.
func forwardingInterceptor() grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		var kv []string
		if id := reqctx.RequestID(ctx); id != "" {
			kv = append(kv, requestIDMetadataKey, id)
		}
		if token, ok := authn.RawTokenFromContext(ctx); ok && token != "" {
			kv = append(kv, authorizationMetadataKey, "Bearer "+token)
		}
		if len(kv) > 0 {
			ctx = metadata.AppendToOutgoingContext(ctx, kv...)
		}
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}
