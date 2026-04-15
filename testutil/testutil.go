// Package testutil provides helpers for writing HTTP handler tests.
package testutil

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"
)

// NewTestLogger returns a no-op logger suitable for tests.
func NewTestLogger() *zap.Logger {
	return zap.NewNop()
}

// NewTestRecorder returns a new httptest.ResponseRecorder.
func NewTestRecorder() *httptest.ResponseRecorder {
	return httptest.NewRecorder()
}

// NewTestRequest creates an *http.Request for testing. If body is non-nil it is
// JSON-encoded and the Content-Type header is set to application/json.
func NewTestRequest(method, path string, body any) *http.Request {
	var req *http.Request
	if body != nil {
		b, _ := json.Marshal(body)
		req, _ = http.NewRequest(method, path, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req, _ = http.NewRequest(method, path, nil)
	}
	return req
}

// AssertJSONResponse asserts that rec has the given status code and, if dst is
// non-nil, decodes the body into dst.
func AssertJSONResponse(t *testing.T, rec *httptest.ResponseRecorder, status int, dst any) {
	t.Helper()
	if rec.Code != status {
		t.Errorf("expected status %d, got %d; body: %s", status, rec.Code, rec.Body.String())
	}
	if dst != nil {
		if err := json.NewDecoder(rec.Body).Decode(dst); err != nil {
			t.Errorf("failed to decode response body: %v", err)
		}
	}
}

// AssertErrorResponse asserts that rec has the given HTTP status and that the
// JSON body contains the expected error code.
func AssertErrorResponse(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rec.Code != status {
		t.Errorf("expected status %d, got %d; body: %s", status, rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Errorf("failed to decode error body: %v", err)
		return
	}
	if got, _ := payload["code"].(string); got != code {
		t.Errorf("expected error code %q, got %q", code, got)
	}
}
