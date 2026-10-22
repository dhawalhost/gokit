package grpc

import (
	"context"
	"errors"
	"net/http"

	gokiterrors "github.com/dhawalhost/gokit/errors"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ToStatus converts an error into a gRPC *status.Status.
// If the error is already a gRPC status error, it is returned directly.
// If it is a gokit *errors.AppError, the HTTPStatus is translated to the corresponding gRPC code.
// If it is a context cancellation or timeout, it maps to codes.Canceled or codes.DeadlineExceeded.
// Otherwise, it defaults to codes.Internal.
func ToStatus(err error) *status.Status {
	if err == nil {
		return status.New(codes.OK, "")
	}

	// If it is already a gRPC status error, return it
	if st, ok := status.FromError(err); ok {
		return st
	}

	// Context errors
	if errors.Is(err, context.Canceled) {
		return status.New(codes.Canceled, err.Error())
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return status.New(codes.DeadlineExceeded, err.Error())
	}

	// Check if it's a gokit AppError
	if ae, ok := gokiterrors.IsAppError(err); ok {
		code := httpStatusToGRPCCode(ae.HTTPStatus)
		msg := ae.Message
		if ae.Code != "" && ae.Code != "INTERNAL_ERROR" {
			msg = ae.Code + ": " + ae.Message
		}
		return status.New(code, msg)
	}

	return status.New(codes.Internal, err.Error())
}

// ToGRPCError converts an error into a gRPC error suitable for returning from RPC handlers.
func ToGRPCError(err error) error {
	if err == nil {
		return nil
	}
	return ToStatus(err).Err()
}

// FromStatus converts a gRPC *status.Status into a gokit *errors.AppError.
// If st is nil or codes.OK, it returns nil.
func FromStatus(st *status.Status) *gokiterrors.AppError {
	if st == nil || st.Code() == codes.OK {
		return nil
	}

	httpStatus, appCode := grpcCodeToHTTPStatus(st.Code())
	return gokiterrors.New(httpStatus, appCode, st.Message())
}

// FromGRPCError converts a gRPC client/server error into a gokit application error.
// If err is nil, it returns nil. If err is not a gRPC status error, it is wrapped in an internal error.
func FromGRPCError(err error) error {
	if err == nil {
		return nil
	}

	st, ok := status.FromError(err)
	if !ok {
		return gokiterrors.Internal(err.Error())
	}

	return FromStatus(st)
}

func httpStatusToGRPCCode(httpStatus int) codes.Code {
	switch httpStatus {
	case http.StatusBadRequest:
		return codes.InvalidArgument
	case http.StatusUnauthorized:
		return codes.Unauthenticated
	case http.StatusForbidden:
		return codes.PermissionDenied
	case http.StatusNotFound:
		return codes.NotFound
	case http.StatusConflict:
		return codes.AlreadyExists
	case http.StatusUnprocessableEntity:
		return codes.InvalidArgument
	case http.StatusTooManyRequests:
		return codes.ResourceExhausted
	case http.StatusGatewayTimeout:
		return codes.DeadlineExceeded
	case http.StatusServiceUnavailable:
		return codes.Unavailable
	case http.StatusNotImplemented:
		return codes.Unimplemented
	default:
		return codes.Internal
	}
}

func grpcCodeToHTTPStatus(code codes.Code) (int, string) {
	switch code {
	case codes.InvalidArgument:
		return http.StatusBadRequest, "INVALID_ARGUMENT"
	case codes.Unauthenticated:
		return http.StatusUnauthorized, "UNAUTHENTICATED"
	case codes.PermissionDenied:
		return http.StatusForbidden, "PERMISSION_DENIED"
	case codes.NotFound:
		return http.StatusNotFound, "NOT_FOUND"
	case codes.AlreadyExists:
		return http.StatusConflict, "ALREADY_EXISTS"
	case codes.ResourceExhausted:
		return http.StatusTooManyRequests, "RESOURCE_EXHAUSTED"
	case codes.FailedPrecondition:
		return http.StatusBadRequest, "FAILED_PRECONDITION"
	case codes.Aborted:
		return http.StatusConflict, "ABORTED"
	case codes.OutOfRange:
		return http.StatusBadRequest, "OUT_OF_RANGE"
	case codes.Unimplemented:
		return http.StatusNotImplemented, "UNIMPLEMENTED"
	case codes.Internal:
		return http.StatusInternalServerError, "INTERNAL_ERROR"
	case codes.Unavailable:
		return http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE"
	case codes.DataLoss:
		return http.StatusInternalServerError, "DATA_LOSS"
	case codes.DeadlineExceeded:
		return http.StatusGatewayTimeout, "DEADLINE_EXCEEDED"
	case codes.Canceled:
		return 499, "CLIENT_CLOSED_REQUEST"
	default:
		return http.StatusInternalServerError, "INTERNAL_ERROR"
	}
}
