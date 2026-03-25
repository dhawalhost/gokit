package errors_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	apperrors "github.com/dhawalhost/gokit/errors"
)

func TestNewAppError(t *testing.T) {
	err := apperrors.New(http.StatusBadRequest, "TEST_CODE", "test message")
	if err.HTTPStatus != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", err.HTTPStatus)
	}
	if err.Code != "TEST_CODE" {
		t.Errorf("expected 'TEST_CODE', got %q", err.Code)
	}
	if err.Message != "test message" {
		t.Errorf("expected 'test message', got %q", err.Message)
	}
}

func TestAppErrorHelpers(t *testing.T) {
	tests := []struct {
		fn   func() *apperrors.AppError
		code int
	}{
		{func() *apperrors.AppError { return apperrors.BadRequest("C", "m") }, http.StatusBadRequest},
		{func() *apperrors.AppError { return apperrors.Unauthorized("C", "m") }, http.StatusUnauthorized},
		{func() *apperrors.AppError { return apperrors.Forbidden("C", "m") }, http.StatusForbidden},
		{func() *apperrors.AppError { return apperrors.NotFound("C", "m") }, http.StatusNotFound},
		{func() *apperrors.AppError { return apperrors.Conflict("C", "m") }, http.StatusConflict},
		{func() *apperrors.AppError { return apperrors.UnprocessableEntity("C", "m") }, http.StatusUnprocessableEntity},
		{func() *apperrors.AppError { return apperrors.TooManyRequests("C", "m") }, http.StatusTooManyRequests},
		{func() *apperrors.AppError { return apperrors.Internal("m") }, http.StatusInternalServerError},
		{func() *apperrors.AppError { return apperrors.ServiceUnavailable("m") }, http.StatusServiceUnavailable},
	}
	for _, tc := range tests {
		ae := tc.fn()
		if ae.HTTPStatus != tc.code {
			t.Errorf("expected %d, got %d", tc.code, ae.HTTPStatus)
		}
	}
}

func TestErrorString(t *testing.T) {
	err := apperrors.BadRequest("C", "bad request")
	if err.Error() != "bad request" {
		t.Errorf("expected 'bad request', got %q", err.Error())
	}
}

func TestErrorStringWithCause(t *testing.T) {
	cause := fmt.Errorf("underlying issue")
	err := apperrors.WithErr(apperrors.Internal("something failed"), cause)
	if err.Error() != "something failed: underlying issue" {
		t.Errorf("unexpected error string: %q", err.Error())
	}
}

func TestUnwrap(t *testing.T) {
	cause := fmt.Errorf("root cause")
	err := apperrors.WithErr(apperrors.Internal("msg"), cause)
	if err.Unwrap() != cause {
		t.Error("Unwrap should return the original cause")
	}
}

func TestWithDetails(t *testing.T) {
	err := apperrors.WithDetails(apperrors.BadRequest("C", "m"), map[string]string{"field": "email"})
	if err.Details == nil {
		t.Fatal("expected details to be set")
	}
}

func TestIsAppError(t *testing.T) {
	err := apperrors.NotFound("NOT_FOUND", "not found")
	ae, ok := apperrors.IsAppError(err)
	if !ok {
		t.Fatal("expected IsAppError to return true")
	}
	if ae.Code != "NOT_FOUND" {
		t.Errorf("expected 'NOT_FOUND', got %q", ae.Code)
	}
}

func TestIsAppErrorFalse(t *testing.T) {
	_, ok := apperrors.IsAppError(fmt.Errorf("plain error"))
	if ok {
		t.Fatal("expected IsAppError to return false for plain error")
	}
}

func TestWriteError(t *testing.T) {
	w := httptest.NewRecorder()
	r, _ := http.NewRequest(http.MethodGet, "/", nil)
	apperrors.WriteError(w, r, apperrors.NotFound("NOT_FOUND", "not found"))

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
	var body map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["code"] != "NOT_FOUND" {
		t.Errorf("expected code 'NOT_FOUND', got %v", body["code"])
	}
}

func TestWriteErrorPlain(t *testing.T) {
	w := httptest.NewRecorder()
	r, _ := http.NewRequest(http.MethodGet, "/", nil)
	apperrors.WriteError(w, r, fmt.Errorf("some internal error"))

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}
