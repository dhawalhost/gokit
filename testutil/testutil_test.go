package testutil_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dhawalhost/gokit/testutil"
)

func TestNewTestLogger(t *testing.T) {
	l := testutil.NewTestLogger()
	if l == nil {
		t.Fatal("expected non-nil logger")
	}
}

func TestNewTestRecorder(t *testing.T) {
	rec := testutil.NewTestRecorder()
	if rec == nil {
		t.Fatal("expected non-nil recorder")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("expected default code 200, got %d", rec.Code)
	}
}

func TestNewTestRequest_NoBody(t *testing.T) {
	r := testutil.NewTestRequest(http.MethodGet, "/test", nil)
	if r.Method != http.MethodGet {
		t.Errorf("expected GET, got %s", r.Method)
	}
	if r.URL.Path != "/test" {
		t.Errorf("expected /test, got %s", r.URL.Path)
	}
}

func TestNewTestRequest_WithBody(t *testing.T) {
	body := map[string]string{"key": "value"}
	r := testutil.NewTestRequest(http.MethodPost, "/test", body)
	if r.Method != http.MethodPost {
		t.Errorf("expected POST, got %s", r.Method)
	}
	if r.Header.Get("Content-Type") != "application/json" {
		t.Errorf("expected Content-Type application/json, got %s", r.Header.Get("Content-Type"))
	}
	var decoded map[string]string
	if err := json.NewDecoder(r.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if decoded["key"] != "value" {
		t.Errorf("expected key=value, got %q", decoded["key"])
	}
}

func TestAssertJSONResponse_Success(t *testing.T) {
	rec := httptest.NewRecorder()
	rec.WriteHeader(http.StatusOK)
	_, _ = rec.Write([]byte(`{"id":1}`))

	var dst map[string]int
	// Use a nested testing.T to capture failures without failing the outer test.
	inner := &testing.T{}
	testutil.AssertJSONResponse(inner, rec, http.StatusOK, &dst)
	if inner.Failed() {
		t.Error("AssertJSONResponse should not fail for matching status")
	}
	if dst["id"] != 1 {
		t.Errorf("expected id=1, got %d", dst["id"])
	}
}

func TestAssertJSONResponse_WrongStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	rec.WriteHeader(http.StatusCreated)
	_, _ = rec.Write([]byte(`{}`))

	inner := &testing.T{}
	testutil.AssertJSONResponse(inner, rec, http.StatusOK, nil)
	if !inner.Failed() {
		t.Error("AssertJSONResponse should fail for wrong status")
	}
}

func TestAssertErrorResponse_Success(t *testing.T) {
	rec := httptest.NewRecorder()
	rec.WriteHeader(http.StatusNotFound)
	_, _ = rec.Write([]byte(`{"code":"NOT_FOUND","message":"not found"}`))

	inner := &testing.T{}
	testutil.AssertErrorResponse(inner, rec, http.StatusNotFound, "NOT_FOUND")
	if inner.Failed() {
		t.Error("AssertErrorResponse should not fail for matching status and code")
	}
}

func TestAssertErrorResponse_WrongCode(t *testing.T) {
	rec := httptest.NewRecorder()
	rec.WriteHeader(http.StatusNotFound)
	_, _ = rec.Write([]byte(`{"code":"OTHER_CODE","message":"err"}`))

	inner := &testing.T{}
	testutil.AssertErrorResponse(inner, rec, http.StatusNotFound, "NOT_FOUND")
	if !inner.Failed() {
		t.Error("AssertErrorResponse should fail for wrong error code")
	}
}
