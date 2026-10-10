package grpcx

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	"github.com/google/uuid"

	"github.com/herlenashavi/go-api/pkg/authn"
	"github.com/herlenashavi/go-api/pkg/reqctx"
)

// recoveryInterceptor mirrors middleware.Recover: a panic in a handler
// becomes a codes.Internal status instead of killing the server's
// connection (an unrecovered panic in a gRPC handler goroutine would take
// down the whole process, the same risk middleware.Timeout's doc comment
// describes for HTTP).
func recoveryInterceptor(logger *slog.Logger, debugMode bool) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		defer func() {
			if rec := recover(); rec != nil {
				panicErr := fmt.Errorf("panic: %v", rec)
				logger.ErrorContext(ctx, "unhandled panic in gRPC handler",
					"method", info.FullMethod, "error", panicErr, "stack", string(debug.Stack()))
				msg := "internal error"
				if debugMode {
					msg = panicErr.Error()
				}
				err = status.Error(codes.Internal, msg)
			}
		}()
		return handler(ctx, req)
	}
}

// requestIDInterceptor reuses an inbound x-request-id metadata value (set
// by an HTTP gateway's middleware.RequestID, or by a calling service's
// client interceptor below) or mints one, and stores it via reqctx so
// logging.Fielder-based correlation works identically to the HTTP path.
func requestIDInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		id := requestIDFromMetadata(ctx)
		if id == "" {
			id = uuid.NewString()
		}
		return handler(reqctx.WithRequestID(ctx, id), req)
	}
}

func requestIDFromMetadata(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	values := md.Get(requestIDMetadataKey)
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

// accessLogInterceptor logs one line per completed RPC: full method,
// gRPC status code, duration — the gRPC equivalent of
// middleware.AccessLog. The otel stats handler (see NewServer) covers
// tracing/metrics; this covers the same structured-log trail the HTTP
// side has.
func accessLogInterceptor(logger *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		start := time.Now()
		resp, err := handler(ctx, req)

		logger.LogAttrs(ctx, slog.LevelInfo, "rpc completed",
			slog.String("method", info.FullMethod),
			slog.String("code", status.Code(err).String()),
			slog.Int64("duration_ms", time.Since(start).Milliseconds()),
			slog.String("peer", peerAddr(ctx)),
		)
		return resp, err
	}
}

func peerAddr(ctx context.Context) string {
	p, ok := peer.FromContext(ctx)
	if !ok || p.Addr == nil {
		return ""
	}
	return p.Addr.String()
}

// healthServiceMethodPrefix exempts the standard gRPC health checking
// protocol (registered via RegisterHealth) from authentication, the same
// way health.Router's HTTP endpoints are mounted outside any auth
// middleware in pkg/app — a load balancer or orchestrator probing health
// should never need a token. This is the one hardcoded exception;
// anything else that should be reachable without authentication belongs
// on a server built with no Verifier at all, not on an allowlist, for the
// same reason authz has no generic method->action mapping (see package
// doc) — "which RPCs skip auth" is meant to be an obvious, server-wide
// fact, not a per-method setting easy to misconfigure.
const healthServiceMethodPrefix = "/grpc.health.v1.Health/"

// authnInterceptor is the gRPC equivalent of authn.Middleware: it reads a
// "Bearer <token>" value from the "authorization" metadata key, verifies
// it, and places the resulting Principal (and the raw token, for further
// forwarding — see authn.WithRawToken) into context.
func authnInterceptor(verifier authn.Verifier) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if strings.HasPrefix(info.FullMethod, healthServiceMethodPrefix) {
			return handler(ctx, req)
		}

		token, err := bearerTokenFromMetadata(ctx)
		if err != nil {
			return nil, status.Error(codes.Unauthenticated, err.Error())
		}

		principal, err := verifier.Verify(ctx, token)
		if err != nil {
			return nil, status.Error(codes.Unauthenticated, "the provided credentials are invalid or expired")
		}

		ctx = authn.WithPrincipal(ctx, principal)
		ctx = authn.WithRawToken(ctx, token)
		return handler(ctx, req)
	}
}

func bearerTokenFromMetadata(ctx context.Context) (string, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", fmt.Errorf("a bearer token is required in the %q metadata key", authorizationMetadataKey)
	}
	values := md.Get(authorizationMetadataKey)
	if len(values) == 0 {
		return "", fmt.Errorf("a bearer token is required in the %q metadata key", authorizationMetadataKey)
	}
	const prefix = "Bearer "
	if !strings.HasPrefix(values[0], prefix) {
		return "", fmt.Errorf("the %q metadata value must be in the form %q", authorizationMetadataKey, prefix+"<token>")
	}
	token := strings.TrimSpace(strings.TrimPrefix(values[0], prefix))
	if token == "" {
		return "", fmt.Errorf("the %q metadata value must be in the form %q", authorizationMetadataKey, prefix+"<token>")
	}
	return token, nil
}
