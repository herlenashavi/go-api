package grpcx

import (
	"context"
	"time"

	"google.golang.org/grpc"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	"github.com/herlenashavi/go-api/pkg/health"
)

// healthService adapts pkg/health's Checker-based model to gRPC's standard
// health checking protocol, so both your HTTP /health/readiness and this
// gRPC service report identical results — they both call health.Run.
//
// Only Check is implemented; List and Watch fall back to the embedded
// UnimplementedHealthServer's Unimplemented status. That matches
// health.Router too: there's no per-service breakdown, only one overall
// readiness verdict for the whole process, deliberately — see
// health.Config's doc comment.
type healthService struct {
	healthpb.UnimplementedHealthServer
	checks  []health.Check
	timeout time.Duration
}

// RegisterHealth registers a gRPC health service on server, backed by the
// same checks you'd pass to health.Router's Config.Checks.
func RegisterHealth(server *grpc.Server, checks []health.Check, perCheckTimeout time.Duration) {
	healthpb.RegisterHealthServer(server, &healthService{checks: checks, timeout: perCheckTimeout})
}

func (h *healthService) Check(ctx context.Context, _ *healthpb.HealthCheckRequest) (*healthpb.HealthCheckResponse, error) {
	healthy, _ := health.Run(ctx, h.checks, h.timeout)
	status := healthpb.HealthCheckResponse_SERVING
	if !healthy {
		status = healthpb.HealthCheckResponse_NOT_SERVING
	}
	return &healthpb.HealthCheckResponse{Status: status}, nil
}
