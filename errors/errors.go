// Package errors provides structured application errors and HTTP error writing.
package errors

import (
	"errors"
	"net/http"
)

// AppError is a structured error carrying an HTTP status code, a machine-readable
// code, a human-readable message, optional details, and an optional cause.
type AppError struct {
	Code       string      `json:"code"`
	Message    string      `json:"message"`
	Details    interface{} `json:"details,omitempty"`
	HTTPStatus int         `json:"-"`
	Err        error       `json:"-"`
}

// Error implements the error interface.
func (e *AppError) Error() string {
	if e.Err != nil {
		return e.Message + ": " + e.Err.Error()
	}
	return e.Message
}

// Unwrap returns the underlying cause.
func (e *AppError) Unwrap() error { return e.Err }

// New creates an AppError with the given HTTP status, code, and message.
func New(httpStatus int, code, message string) *AppError {
	return &AppError{Code: code, Message: message, HTTPStatus: httpStatus}
}

// BadRequest returns a 400 AppError.
func BadRequest(code, message string) *AppError {
	return New(http.StatusBadRequest, code, message)
}

// Unauthorized returns a 401 AppError.
func Unauthorized(code, message string) *AppError {
	return New(http.StatusUnauthorized, code, message)
}

// Forbidden returns a 403 AppError.
func Forbidden(code, message string) *AppError {
	return New(http.StatusForbidden, code, message)
}

// NotFound returns a 404 AppError.
func NotFound(code, message string) *AppError {
	return New(http.StatusNotFound, code, message)
}

// Conflict returns a 409 AppError.
func Conflict(code, message string) *AppError {
	return New(http.StatusConflict, code, message)
}

// UnprocessableEntity returns a 422 AppError.
func UnprocessableEntity(code, message string) *AppError {
	return New(http.StatusUnprocessableEntity, code, message)
}

// TooManyRequests returns a 429 AppError.
func TooManyRequests(code, message string) *AppError {
	return New(http.StatusTooManyRequests, code, message)
}

// Internal returns a 500 AppError.
func Internal(message string) *AppError {
	return New(http.StatusInternalServerError, "INTERNAL_ERROR", message)
}

// ServiceUnavailable returns a 503 AppError.
func ServiceUnavailable(message string) *AppError {
	return New(http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", message)
}

// WithDetails attaches detail payload to an AppError.
func WithDetails(err *AppError, details interface{}) *AppError {
	err.Details = details
	return err
}

// WithErr attaches a cause error to an AppError.
func WithErr(err *AppError, cause error) *AppError {
	err.Err = cause
	return err
}

// IsAppError tests whether err is an *AppError and returns it if so.
func IsAppError(err error) (*AppError, bool) {
	var ae *AppError
	if errors.As(err, &ae) {
		return ae, true
	}
	return nil, false
}
