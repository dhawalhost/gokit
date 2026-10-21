package testutil_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dhawalhost/gokit/testutil"
)

func TestTestHelpers_Creation_TableDriven(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "NewTestLogger returns non-nil logger",
			run: func(t *testing.T) {
				l := testutil.NewTestLogger()
				if l == nil {
					t.Fatal("expected non-nil logger")
				}
			},
		},
		{
			name: "NewTestRecorder returns recorder with default status 200",
			run: func(t *testing.T) {
				rec := testutil.NewTestRecorder()
				if rec == nil {
					t.Fatal("expected non-nil recorder")
				}
				if rec.Code != http.StatusOK {
					t.Errorf("expected default code 200, got %d", rec.Code)
				}
			},
		},
		{
			name: "NewTestRequest without body sets method and path",
			run: func(t *testing.T) {
				r := testutil.NewTestRequest(http.MethodGet, "/test", nil)
				if r.Method != http.MethodGet {
					t.Errorf("expected GET, got %s", r.Method)
				}
				if r.URL.Path != "/test" {
					t.Errorf("expected /test, got %s", r.URL.Path)
				}
			},
		},
		{
			name: "NewTestRequest with body serializes JSON and sets Content-Type",
			run: func(t *testing.T) {
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
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, tc.run)
	}
}

func TestAssertJSONResponse_TableDriven(t *testing.T) {
	tests := []struct {
		name        string
		code        int
		body        string
		expectCode  int
		expectFail  bool
		verifyModel func(t *testing.T, dst map[string]int)
	}{
		{
			name:       "valid JSON and matching status code passes assertion",
			code:       http.StatusOK,
			body:       `{"id":1}`,
			expectCode: http.StatusOK,
			expectFail: false,
			verifyModel: func(t *testing.T, dst map[string]int) {
				if dst["id"] != 1 {
					t.Errorf("expected id=1, got %d", dst["id"])
				}
			},
		},
		{
			name:       "mismatched status code triggers failure",
			code:       http.StatusCreated,
			body:       `{}`,
			expectCode: http.StatusOK,
			expectFail: true,
		},
		{
			name:       "invalid JSON body triggers failure",
			code:       http.StatusOK,
			body:       `not-json`,
			expectCode: http.StatusOK,
			expectFail: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			rec.WriteHeader(tc.code)
			_, _ = rec.Write([]byte(tc.body))

			inner := &testing.T{}
			var dst map[string]int
			testutil.AssertJSONResponse(inner, rec, tc.expectCode, &dst)

			if inner.Failed() != tc.expectFail {
				t.Fatalf("expectFail=%v, got inner.Failed()=%v", tc.expectFail, inner.Failed())
			}
			if !tc.expectFail && tc.verifyModel != nil {
				tc.verifyModel(t, dst)
			}
		})
	}
}

func TestAssertErrorResponse_TableDriven(t *testing.T) {
	tests := []struct {
		name       string
		code       int
		body       string
		expectCode int
		errorCode  string
		expectFail bool
	}{
		{
			name:       "matching status and error code passes assertion",
			code:       http.StatusNotFound,
			body:       `{"code":"NOT_FOUND","message":"not found"}`,
			expectCode: http.StatusNotFound,
			errorCode:  "NOT_FOUND",
			expectFail: false,
		},
		{
			name:       "mismatched error code triggers failure",
			code:       http.StatusNotFound,
			body:       `{"code":"OTHER_CODE","message":"err"}`,
			expectCode: http.StatusNotFound,
			errorCode:  "NOT_FOUND",
			expectFail: true,
		},
		{
			name:       "invalid JSON in error response triggers failure",
			code:       http.StatusBadRequest,
			body:       `not-json`,
			expectCode: http.StatusBadRequest,
			errorCode:  "ANY_CODE",
			expectFail: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			rec.WriteHeader(tc.code)
			_, _ = rec.Write([]byte(tc.body))

			inner := &testing.T{}
			testutil.AssertErrorResponse(inner, rec, tc.expectCode, tc.errorCode)

			if inner.Failed() != tc.expectFail {
				t.Fatalf("expectFail=%v, got inner.Failed()=%v", tc.expectFail, inner.Failed())
			}
		})
	}
}
