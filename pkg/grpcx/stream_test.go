package grpcx_test

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/herlenashavi/go-api/pkg/authn"
	"github.com/herlenashavi/go-api/pkg/grpcx"
	"github.com/herlenashavi/go-api/pkg/reqctx"
)

// streamTestService is a hand-written server-streaming service (no codegen
// needed): /grpcx.test.Streamer/Stream sends one Empty and returns,
// /grpcx.test.Streamer/Panic panics. The handlers record the stream
// context they were given, so tests can assert what the interceptor chain
// put into it.
type streamTestService struct {
	gotSubject   string
	gotRequestID string
}

var streamTestDesc = grpc.ServiceDesc{
	ServiceName: "grpcx.test.Streamer",
	HandlerType: (*any)(nil),
	Streams: []grpc.StreamDesc{
		{
			StreamName:    "Stream",
			ServerStreams: true,
			Handler: func(srv any, stream grpc.ServerStream) error {
				s := srv.(*streamTestService)
				if p, ok := authn.FromContext(stream.Context()); ok {
					s.gotSubject = p.Subject
				}
				s.gotRequestID = reqctx.RequestID(stream.Context())
				return stream.SendMsg(&emptypb.Empty{})
			},
		},
		{
			StreamName:    "Panic",
			ServerStreams: true,
			Handler: func(any, grpc.ServerStream) error {
				panic("boom")
			},
		},
	},
}

func startStreamServer(t *testing.T, deps grpcx.Dependencies) (*streamTestService, *grpc.ClientConn) {
	t.Helper()
	svc := &streamTestService{}
	server := grpcx.NewServer(deps)
	server.RegisterService(&streamTestDesc, svc)

	lis := mustListen(t)
	go func() { _ = server.Serve(lis) }()
	t.Cleanup(server.Stop)

	conn, err := grpcx.Dial(lis.Addr().String(), grpcx.Dependencies{})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return svc, conn
}

// callStream opens method, half-closes, and drains it, returning the
// terminal error (nil on a clean end-of-stream).
func callStream(ctx context.Context, conn *grpc.ClientConn, method string) error {
	desc := &grpc.StreamDesc{ServerStreams: true}
	cs, err := conn.NewStream(ctx, desc, "/grpcx.test.Streamer/"+method)
	if err != nil {
		return err
	}
	if err := cs.SendMsg(&emptypb.Empty{}); err != nil {
		return err
	}
	if err := cs.CloseSend(); err != nil {
		return err
	}
	for {
		if err := cs.RecvMsg(&emptypb.Empty{}); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

// TestStreamAuthRequired is the streaming counterpart of TestAuthRequired:
// before stream interceptors were installed, a streaming method ran with
// no authentication at all.
func TestStreamAuthRequired(t *testing.T) {
	secret := []byte("test-secret")
	svc, conn := startStreamServer(t, grpcx.Dependencies{
		Verifier: authn.NewHMACVerifier(secret, authn.ClaimsConfig{}),
	})

	t.Run("no token", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := callStream(ctx, conn, "Stream"); status.Code(err) != codes.Unauthenticated {
			t.Fatalf("code = %v, want Unauthenticated (err: %v)", status.Code(err), err)
		}
	})

	t.Run("valid token", func(t *testing.T) {
		token, err := authn.SignHMAC(secret, authn.Principal{Subject: "alice"}, time.Minute)
		if err != nil {
			t.Fatalf("SignHMAC: %v", err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token, "x-request-id", "req-123")

		if err := callStream(ctx, conn, "Stream"); err != nil {
			t.Fatalf("Stream with valid token: %v", err)
		}
		if svc.gotSubject != "alice" {
			t.Fatalf("handler saw subject %q, want %q", svc.gotSubject, "alice")
		}
		if svc.gotRequestID != "req-123" {
			t.Fatalf("handler saw request ID %q, want %q", svc.gotRequestID, "req-123")
		}
	})
}

// TestStreamRecovery proves a panicking stream handler becomes
// codes.Internal and the server survives to serve another stream — an
// unrecovered panic there would crash this test process.
func TestStreamRecovery(t *testing.T) {
	_, conn := startStreamServer(t, grpcx.Dependencies{})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := callStream(ctx, conn, "Panic"); status.Code(err) != codes.Internal {
		t.Fatalf("code = %v, want Internal (err: %v)", status.Code(err), err)
	}
	if err := callStream(ctx, conn, "Stream"); err != nil {
		t.Fatalf("Stream after a panic on another stream: %v", err)
	}
}
