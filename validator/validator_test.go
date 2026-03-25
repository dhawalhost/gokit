package validator_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	govalidator "github.com/go-playground/validator/v10"
	"github.com/dhawalhost/gokit/validator"
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

func TestBindValid(t *testing.T) {
	v := validator.New()
	r := makeRequest(t, createUserReq{Email: "user@example.com", Name: "Alice", Age: 30})
	var req createUserReq
	if err := v.Bind(r, &req); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if req.Email != "user@example.com" {
		t.Errorf("unexpected email: %q", req.Email)
	}
}

func TestBindInvalidEmail(t *testing.T) {
	v := validator.New()
	r := makeRequest(t, createUserReq{Email: "not-an-email", Name: "Alice", Age: 30})
	var req createUserReq
	if err := v.Bind(r, &req); err == nil {
		t.Fatal("expected validation error for invalid email")
	}
}

func TestBindMissingRequiredField(t *testing.T) {
	v := validator.New()
	r := makeRequest(t, map[string]interface{}{"age": 25}) // missing email and name
	var req createUserReq
	if err := v.Bind(r, &req); err == nil {
		t.Fatal("expected error for missing required fields")
	}
}

func TestBindInvalidJSON(t *testing.T) {
	v := validator.New()
	r, _ := http.NewRequest(http.MethodPost, "/", strings.NewReader("not-json"))
	r.Header.Set("Content-Type", "application/json")
	var req createUserReq
	if err := v.Bind(r, &req); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestValidate(t *testing.T) {
	v := validator.New()
	valid := createUserReq{Email: "a@b.com", Name: "Al", Age: 25}
	if err := v.Validate(valid); err != nil {
		t.Fatalf("expected no error for valid struct: %v", err)
	}
}

func TestValidateInvalid(t *testing.T) {
	v := validator.New()
	invalid := createUserReq{Email: "bad", Name: "A", Age: -1}
	if err := v.Validate(invalid); err == nil {
		t.Fatal("expected validation error for invalid struct")
	}
}

func TestDefault(t *testing.T) {
	if validator.Default == nil {
		t.Fatal("expected non-nil Default validator")
	}
}

func TestRegisterCustom(t *testing.T) {
	v := validator.New()
	err := v.RegisterCustom("notfoo", func(_ govalidator.FieldLevel) bool {
		return true
	})
	if err != nil {
		t.Fatalf("RegisterCustom: %v", err)
	}
}
