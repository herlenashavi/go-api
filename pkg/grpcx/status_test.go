package grpcx_test

import (
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/herlenashavi/go-api/pkg/grpcx"
	"github.com/herlenashavi/go-api/pkg/problem"
)

func TestStatus_MapsProblemsToCodes(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want codes.Code
	}{
		{"not found", problem.NotFound("Villain"), codes.NotFound},
		{"forbidden", problem.Forbidden(""), codes.PermissionDenied},
		{"unauthorized", problem.Unauthorized(""), codes.Unauthenticated},
		{"validation", problem.Validation(problem.FieldError{Field: "name", Detail: "is required"}), codes.InvalidArgument},
		{"bad request", problem.BadRequest("nope"), codes.InvalidArgument},
		{"conflict", problem.Conflict("already exists"), codes.AlreadyExists},
		{"internal", problem.Internal(errors.New("boom"), false), codes.Internal},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := grpcx.Status(tc.err)
			if code := grpcstatus.Code(got); code != tc.want {
				t.Fatalf("code = %v, want %v (err: %v)", code, tc.want, got)
			}
		})
	}
}

func TestStatus_PassesThroughExistingGRPCStatus(t *testing.T) {
	original := grpcstatus.Error(codes.ResourceExhausted, "slow down")
	if got := grpcx.Status(original); got != original {
		t.Fatalf("expected the exact same error to pass through unchanged, got %v", got)
	}
}

func TestStatus_PlainErrorBecomesInternal(t *testing.T) {
	got := grpcx.Status(errors.New("unexpected"))
	if code := grpcstatus.Code(got); code != codes.Internal {
		t.Fatalf("code = %v, want Internal", code)
	}
}

func TestStatus_Nil(t *testing.T) {
	if got := grpcx.Status(nil); got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
}

func TestProblem_RoundTripsThroughStatus(t *testing.T) {
	cases := []struct {
		name       string
		original   *problem.Problem
		wantStatus int
	}{
		{"not found", problem.NotFound("Villain"), 404},
		{"forbidden", problem.Forbidden("nope"), 403},
		{"unauthorized", problem.Unauthorized("nope"), 401},
		{"conflict", problem.Conflict("exists"), 409},
		{"bad request", problem.BadRequest("nope"), 400},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			roundTripped := grpcx.Problem(grpcx.Status(tc.original))
			if roundTripped.Status != tc.wantStatus {
				t.Fatalf("status = %d, want %d", roundTripped.Status, tc.wantStatus)
			}
		})
	}
}

func TestProblem_Nil(t *testing.T) {
	if got := grpcx.Problem(nil); got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
}
