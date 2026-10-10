package main

import (
	"context"
	"fmt"

	"google.golang.org/grpc"

	villainsv1 "github.com/herlenashavi/go-api/gen/villains/v1"
	"github.com/herlenashavi/go-api/pkg/grpcx"
)

// VillainsClient wraps the generated gRPC client for the villains
// service — heroes's one piece of interservice communication, built on
// pkg/grpcx.Dial so it gets the same OTel tracing/metrics as every other
// client/server pair in this framework, plus request-ID and (if the
// calling context carries one via authn.WithRawToken — which
// authn.Middleware already sets for every authenticated HTTP request)
// bearer-token forwarding for free.
type VillainsClient struct {
	conn   *grpc.ClientConn
	client villainsv1.VillainServiceClient
}

// NewVillainsClient dials target (host:port) once at startup. The
// connection is reused for the life of the process; grpc.ClientConn is
// safe for concurrent use by multiple goroutines, same as *http.Client.
func NewVillainsClient(target string, deps grpcx.Dependencies) (*VillainsClient, error) {
	conn, err := grpcx.Dial(target, deps)
	if err != nil {
		return nil, fmt.Errorf("dial villains service at %s: %w", target, err)
	}
	return &VillainsClient{conn: conn, client: villainsv1.NewVillainServiceClient(conn)}, nil
}

// Close releases the underlying connection. Call it once, at shutdown.
func (c *VillainsClient) Close() error {
	return c.conn.Close()
}

// GetVillain forwards ctx (and whatever request ID / bearer token it
// carries) to the villains service's GetVillain RPC. Errors come back
// wrapped by grpcx.Problem, so callers can pass them straight to
// problem.Write without knowing anything about gRPC status codes.
func (c *VillainsClient) GetVillain(ctx context.Context, id string) (*villainsv1.Villain, error) {
	resp, err := c.client.GetVillain(ctx, &villainsv1.GetVillainRequest{Id: id})
	if err != nil {
		return nil, grpcx.Problem(err)
	}
	return resp.Villain, nil
}
