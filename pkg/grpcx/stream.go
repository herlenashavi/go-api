package grpcx

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/herlenashavi/go-api/pkg/authn"
	"github.com/herlenashavi/go-api/pkg/reqctx"
)

// The stream interceptors below are the streaming-RPC counterparts of the
// unary chain in interceptors.go. NewServer installs both chains, so a
// streaming method registered on the server gets the same recovery,
// request ID, access log, peer identity, and authentication as a unary
// one — without them, a stream handler would run unauthenticated and an
// unrecovered panic in it would take down the process.

// wrappedServerStream overrides Context so a stream interceptor can hand
// the handler a context carrying values it added (request ID, Principal,
// ServiceIdentity) — grpc.ServerStream has no WithContext of its own.
type wrappedServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (w *wrappedServerStream) Context() context.Context { return w.ctx }

func withStreamContext(ss grpc.ServerStream, ctx context.Context) grpc.ServerStream {
	return &wrappedServerStream{ServerStream: ss, ctx: ctx}
}

// streamRecoveryInterceptor mirrors recoveryInterceptor.
func streamRecoveryInterceptor(logger *slog.Logger, debugMode bool) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) (err error) {
		defer func() {
			if rec := recover(); rec != nil {
				panicErr := fmt.Errorf("panic: %v", rec)
				logger.ErrorContext(ss.Context(), "unhandled panic in gRPC stream handler",
					"method", info.FullMethod, "error", panicErr, "stack", string(debug.Stack()))
				msg := internalErrorMessage
				if debugMode {
					msg = panicErr.Error()
				}
				err = status.Error(codes.Internal, msg)
			}
		}()
		return handler(srv, ss)
	}
}

// streamRequestIDInterceptor mirrors requestIDInterceptor.
func streamRequestIDInterceptor() grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		ctx := ss.Context()
		id := requestIDFromMetadata(ctx)
		if id == "" {
			id = uuid.NewString()
		}
		return handler(srv, withStreamContext(ss, reqctx.WithRequestID(ctx, id)))
	}
}

// streamAccessLogInterceptor mirrors accessLogInterceptor: one line per
// completed stream.
func streamAccessLogInterceptor(logger *slog.Logger) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		start := time.Now()
		err := handler(srv, ss)

		ctx := ss.Context()
		logger.LogAttrs(ctx, slog.LevelInfo, "rpc completed",
			slog.String("method", info.FullMethod),
			slog.String("code", status.Code(err).String()),
			slog.Int64("duration_ms", time.Since(start).Milliseconds()),
			slog.String("peer", peerAddr(ctx)),
		)
		return err
	}
}

// streamPeerIdentityInterceptor mirrors peerIdentityInterceptor.
func streamPeerIdentityInterceptor(serviceRoles map[string][]string) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		ctx := ss.Context()
		if identity := extractServiceIdentity(ctx, serviceRoles); identity != nil {
			ss = withStreamContext(ss, authn.WithServiceIdentity(ctx, identity))
		}
		return handler(srv, ss)
	}
}

// streamAuthnInterceptor mirrors authnInterceptor, including the health
// service exemption (Health/Watch is a streaming method).
func streamAuthnInterceptor(verifier authn.Verifier) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		ctx, err := authenticate(ss.Context(), verifier, info.FullMethod)
		if err != nil {
			return err
		}
		return handler(srv, withStreamContext(ss, ctx))
	}
}
