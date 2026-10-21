package validator_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/dhawalhost/gokit/validator"
	govalidator "github.com/go-playground/validator/v10"
)

type createUserReq struct {
	Email string `json:"email" validate:"required,email"`
	Name  string `json:"name"  validate:"required,min=2"`
	Age   int    `json:"age"   validate:"gte=0,lte=150"`
}

func makeRequest(t *testing.T, body interface{}) *http.Request {
	t.Helper()
	b, _ := json.Marshal(body)
	r, _ := http.NewRequest(http.MethodPost, "/", bytes.NewReader(b))
	r.Header.Set("Content-Type", "application/json")
	return r
}

func TestValidator_Bind_TableDriven(t *testing.T) {
	v := validator.New()

	tests := []struct {
		name     string
		makeReq  func(t *testing.T) *http.Request
		wantErr  bool
		checkReq func(t *testing.T, req createUserReq)
	}{
		{
			name: "valid JSON request binds and populates fields correctly",
			makeReq: func(t *testing.T) *http.Request {
				return makeRequest(t, createUserReq{Email: "user@example.com", Name: "Alice", Age: 30})
			},
			wantErr: false,
			checkReq: func(t *testing.T, req createUserReq) {
				if req.Email != "user@example.com" {
					t.Errorf("expected email 'user@example.com', got %q", req.Email)
				}
				if req.Name != "Alice" {
					t.Errorf("expected name 'Alice', got %q", req.Name)
				}
			},
		},
		{
			name: "invalid email format fails validation",
			makeReq: func(t *testing.T) *http.Request {
				return makeRequest(t, createUserReq{Email: "not-an-email", Name: "Alice", Age: 30})
			},
			wantErr: true,
		},
		{
			name: "missing required fields fails validation",
			makeReq: func(t *testing.T) *http.Request {
				return makeRequest(t, map[string]interface{}{"age": 25})
			},
			wantErr: true,
		},
		{
			name: "malformed JSON syntax returns unmarshal error",
			makeReq: func(_ *testing.T) *http.Request {
				r, _ := http.NewRequest(http.MethodPost, "/", strings.NewReader("not-valid-json{"))
				r.Header.Set("Content-Type", "application/json")
				return r
			},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := tc.makeReq(t)
			var req createUserReq
			err := v.Bind(r, &req)
			if (err != nil) != tc.wantErr {
				t.Fatalf("wantErr=%v, got=%v", tc.wantErr, err)
			}
			if !tc.wantErr && tc.checkReq != nil {
				tc.checkReq(t, req)
			}
		})
	}
}

func TestValidator_Validate_TableDriven(t *testing.T) {
	v := validator.New()

	tests := []struct {
		name    string
		model   interface{}
		wantErr bool
	}{
		{
			name:    "valid struct passes validation",
			model:   createUserReq{Email: "a@b.com", Name: "Al", Age: 25},
			wantErr: false,
		},
		{
			name:    "struct with invalid email, short name, and negative age fails",
			model:   createUserReq{Email: "bad", Name: "A", Age: -1},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := v.Validate(tc.model)
			if (err != nil) != tc.wantErr {
				t.Fatalf("wantErr=%v, got=%v", tc.wantErr, err)
			}
		})
	}

	// Singleton instance check
	if validator.Default == nil {
		t.Fatal("expected non-nil Default validator singleton")
	}
}

func TestValidator_RegisterCustom_TableDriven(t *testing.T) {
	tests := []struct {
		name    string
		tag     string
		fn      govalidator.Func
		wantErr bool
	}{
		{
			name: "valid custom tag registers without error",
			tag:  "notfoo",
			fn: func(_ govalidator.FieldLevel) bool {
				return true
			},
			wantErr: false,
		},
		{
			name: "empty tag returns error",
			tag:  "",
			fn: func(_ govalidator.FieldLevel) bool {
				return true
			},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v := validator.New()
			err := v.RegisterCustom(tc.tag, tc.fn)
			if (err != nil) != tc.wantErr {
				t.Fatalf("wantErr=%v, got=%v", tc.wantErr, err)
			}
		})
	}
}
