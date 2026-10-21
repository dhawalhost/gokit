package errors_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	apperrors "github.com/dhawalhost/gokit/errors"
)

func TestAppError_Constructors_TableDriven(t *testing.T) {
	tests := []struct {
		name       string
		create     func() *apperrors.AppError
		wantStatus int
		wantCode   string
		wantMsg    string
	}{
		{
			name: "custom constructor New sets status, code, and message",
			create: func() *apperrors.AppError {
				return apperrors.New(http.StatusBadRequest, "TEST_CODE", "test message")
			},
			wantStatus: http.StatusBadRequest,
			wantCode:   "TEST_CODE",
			wantMsg:    "test message",
		},
		{
			name:       "BadRequest helper constructor",
			create:     func() *apperrors.AppError { return apperrors.BadRequest("BAD_REQ", "bad input") },
			wantStatus: http.StatusBadRequest,
			wantCode:   "BAD_REQ",
			wantMsg:    "bad input",
		},
		{
			name:       "Unauthorized helper constructor",
			create:     func() *apperrors.AppError { return apperrors.Unauthorized("UNAUTH", "missing auth") },
			wantStatus: http.StatusUnauthorized,
			wantCode:   "UNAUTH",
			wantMsg:    "missing auth",
		},
		{
			name:       "Forbidden helper constructor",
			create:     func() *apperrors.AppError { return apperrors.Forbidden("FORBIDDEN", "access denied") },
			wantStatus: http.StatusForbidden,
			wantCode:   "FORBIDDEN",
			wantMsg:    "access denied",
		},
		{
			name:       "NotFound helper constructor",
			create:     func() *apperrors.AppError { return apperrors.NotFound("NOT_FOUND", "not found") },
			wantStatus: http.StatusNotFound,
			wantCode:   "NOT_FOUND",
			wantMsg:    "not found",
		},
		{
			name:       "Conflict helper constructor",
			create:     func() *apperrors.AppError { return apperrors.Conflict("CONFLICT", "already exists") },
			wantStatus: http.StatusConflict,
			wantCode:   "CONFLICT",
			wantMsg:    "already exists",
		},
		{
			name: "UnprocessableEntity helper constructor",
			create: func() *apperrors.AppError {
				return apperrors.UnprocessableEntity("UNPROCESSABLE", "validation failed")
			},
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "UNPROCESSABLE",
			wantMsg:    "validation failed",
		},
		{
			name: "TooManyRequests helper constructor",
			create: func() *apperrors.AppError {
				return apperrors.TooManyRequests("RATE_LIMITED", "too many requests")
			},
			wantStatus: http.StatusTooManyRequests,
			wantCode:   "RATE_LIMITED",
			wantMsg:    "too many requests",
		},
		{
			name:       "Internal helper constructor",
			create:     func() *apperrors.AppError { return apperrors.Internal("something failed") },
			wantStatus: http.StatusInternalServerError,
			wantCode:   "INTERNAL_ERROR",
			wantMsg:    "something failed",
		},
		{
			name:       "ServiceUnavailable helper constructor",
			create:     func() *apperrors.AppError { return apperrors.ServiceUnavailable("down for maintenance") },
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "SERVICE_UNAVAILABLE",
			wantMsg:    "down for maintenance",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ae := tc.create()
			if ae.HTTPStatus != tc.wantStatus {
				t.Errorf("expected status %d, got %d", tc.wantStatus, ae.HTTPStatus)
			}
			if ae.Code != tc.wantCode {
				t.Errorf("expected code %q, got %q", tc.wantCode, ae.Code)
			}
			if ae.Message != tc.wantMsg {
				t.Errorf("expected message %q, got %q", tc.wantMsg, ae.Message)
			}
		})
	}
}

func TestAppError_WrappingAndUnwrap_TableDriven(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "error string formatting with and without underlying cause",
			run: func(t *testing.T) {
				base := apperrors.BadRequest("C", "bad request")
				if base.Error() != "bad request" {
					t.Errorf("expected 'bad request', got %q", base.Error())
				}

				cause := fmt.Errorf("underlying issue")
				withCause := apperrors.WithErr(apperrors.Internal("something failed"), cause)
				if withCause.Error() != "something failed: underlying issue" {
					t.Errorf("unexpected error string: %q", withCause.Error())
				}
				if withCause.Unwrap() != cause {
					t.Error("Unwrap should return the original cause")
				}
			},
		},
		{
			name: "WithDetails attaches arbitrary contextual metadata",
			run: func(t *testing.T) {
				err := apperrors.WithDetails(apperrors.BadRequest("C", "m"), map[string]string{"field": "email"})
				if err.Details == nil {
					t.Fatal("expected details to be set")
				}
			},
		},
		{
			name: "IsAppError correctly classifies AppError and standard error types",
			run: func(t *testing.T) {
				err := apperrors.NotFound("NOT_FOUND", "not found")
				ae, ok := apperrors.IsAppError(err)
				if !ok || ae.Code != "NOT_FOUND" {
					t.Fatalf("expected true with code NOT_FOUND, got ok=%v", ok)
				}

				if _, ok := apperrors.IsAppError(fmt.Errorf("plain error")); ok {
					t.Fatal("expected IsAppError to return false for standard errors")
				}
			},
		},
		{
			name: "Wrap protects sensitive error text from JSON serialization",
			run: func(t *testing.T) {
				underlying := fmt.Errorf("sensitive db connection failed: pass=123")
				wrapped := apperrors.Wrap(underlying, "Service temporarily unavailable")

				if wrapped == nil {
					t.Fatal("expected non-nil wrapped error")
				}
				if wrapped.HTTPStatus != http.StatusInternalServerError {
					t.Errorf("expected 500, got %d", wrapped.HTTPStatus)
				}
				if wrapped.Message != "Service temporarily unavailable" {
					t.Errorf("expected public message, got %s", wrapped.Message)
				}
				if wrapped.Unwrap() != underlying {
					t.Errorf("expected underlying cause to match")
				}

				b, err := json.Marshal(wrapped)
				if err != nil {
					t.Fatalf("Marshal: %v", err)
				}
				if string(b) != `{"code":"INTERNAL_ERROR","message":"Service temporarily unavailable"}` {
					t.Errorf("JSON leaked internal error: %s", b)
				}

				if apperrors.Wrap(nil, "msg") != nil {
					t.Error("Wrap(nil) should return nil")
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, tc.run)
	}
}

func TestWriteError_TableDriven(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{
			name:       "writes structured AppError with custom HTTP status",
			err:        apperrors.NotFound("NOT_FOUND", "user not found"),
			wantStatus: http.StatusNotFound,
			wantCode:   "NOT_FOUND",
		},
		{
			name:       "plain error writes 500 Internal Server Error fallback",
			err:        fmt.Errorf("unhandled database failure"),
			wantStatus: http.StatusInternalServerError,
			wantCode:   "INTERNAL_ERROR",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r, _ := http.NewRequest(http.MethodGet, "/", nil)
			apperrors.WriteError(w, r, tc.err)

			if w.Code != tc.wantStatus {
				t.Fatalf("expected status %d, got %d", tc.wantStatus, w.Code)
			}
			var body map[string]interface{}
			if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			if body["code"] != tc.wantCode {
				t.Errorf("expected code %q, got %v", tc.wantCode, body["code"])
			}
		})
	}
}
