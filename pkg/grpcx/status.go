package grpcx

import (
	"errors"
	"net/http"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/herlenashavi/go-api/pkg/problem"
)

// Status maps a go-api error to the closest gRPC status, so the same
// validation/authz logic (pkg/httpx.Validate, pkg/authz.Authorizer,
// pkg/problem's constructors) produces the right error shape on both the
// HTTP and gRPC side of a service, instead of every RPC handler
// hand-rolling its own *problem.Problem -> codes.X mapping.
//
// An error that's already a gRPC status error (created via status.Error)
// passes through unchanged. Anything else that isn't a *problem.Problem
// becomes codes.Internal with a generic message — never its Error() text,
// which for a store or driver error can carry SQL, hostnames, or row data
// the caller has no business seeing. That mirrors problem.Internal(err,
// false) on the HTTP side. To surface detail in debug mode, wrap the error
// via problem.Internal(err, debug) before returning it; Status does not
// re-apply debug gating to a *problem.Problem, since its Detail already
// reflects whatever debug decision was made when it was built.
func Status(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := status.FromError(err); ok {
		return err
	}

	var p *problem.Problem
	if errors.As(err, &p) {
		return status.Error(httpStatusToCode(p.Status), p.Error())
	}

	return status.Error(codes.Internal, internalErrorMessage)
}

// internalErrorMessage is what callers see for an unexpected error. It
// matches recoveryInterceptor's non-debug panic message.
const internalErrorMessage = "internal error"

// Problem is Status's inverse: it maps an error returned by a gRPC call —
// typically one a downstream go-api service produced via Status — back
// into a *problem.Problem, so an HTTP handler that calls a gRPC service
// can surface that failure as the same application/problem+json shape it
// would have produced for a local error, via problem.Write(w, r,
// grpcx.Problem(err)).
//
// The round trip isn't perfectly lossless: Status collapses both
// problem.BadRequest (400) and problem.Validation (422) to
// codes.InvalidArgument, so Problem can only map that back to 400. That's
// an acceptable loss at a protocol boundary — the caller still gets a
// correct 4xx with the original message, just not field-level validation
// detail reconstructed from a gRPC status that was never designed to
// carry it.
func Problem(err error) *problem.Problem {
	if err == nil {
		return nil
	}
	st, ok := status.FromError(err)
	if !ok {
		return problem.Internal(err, false)
	}

	switch st.Code() {
	case codes.InvalidArgument:
		return problem.BadRequest(st.Message())
	case codes.Unauthenticated:
		return problem.Unauthorized(st.Message())
	case codes.PermissionDenied:
		return problem.Forbidden(st.Message())
	case codes.NotFound:
		return problem.NotFound(st.Message())
	case codes.AlreadyExists:
		return problem.Conflict(st.Message())
	case codes.ResourceExhausted:
		return problem.TooManyRequests(st.Message())
	case codes.Unavailable:
		return problem.New(http.StatusServiceUnavailable, "unavailable", "Service Unavailable", st.Message())
	case codes.DeadlineExceeded:
		return problem.New(http.StatusGatewayTimeout, "timeout", "Gateway Timeout", st.Message())
	default:
		return problem.Internal(errors.New(st.Message()), false)
	}
}

func httpStatusToCode(httpStatus int) codes.Code {
	switch httpStatus {
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return codes.InvalidArgument
	case http.StatusUnauthorized:
		return codes.Unauthenticated
	case http.StatusForbidden:
		return codes.PermissionDenied
	case http.StatusNotFound:
		return codes.NotFound
	case http.StatusConflict:
		return codes.AlreadyExists
	case http.StatusTooManyRequests:
		return codes.ResourceExhausted
	case http.StatusServiceUnavailable:
		return codes.Unavailable
	case http.StatusInternalServerError:
		return codes.Internal
	default:
		return codes.Unknown
	}
}
