// Package validator provides request binding and validation utilities.
package validator

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-playground/validator/v10"
)

// Default is the package-level default Validator instance.
var Default = New()

// Validator wraps go-playground/validator.
type Validator struct {
	v *validator.Validate
}

// New creates a new Validator.
func New() *Validator {
	return &Validator{v: validator.New()}
}

// Bind decodes the JSON body of r into dst and then validates it.
func (v *Validator) Bind(r *http.Request, dst interface{}) error {
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		return fmt.Errorf("validator: decode: %w", err)
	}
	return v.Validate(dst)
}

// Validate runs struct validation on dst.
func (v *Validator) Validate(dst interface{}) error {
	if err := v.v.Struct(dst); err != nil {
		return fmt.Errorf("validator: validate: %w", err)
	}
	return nil
}

// RegisterCustom registers a custom validation function for the given tag.
func (v *Validator) RegisterCustom(tag string, fn validator.Func) error {
	if err := v.v.RegisterValidation(tag, fn); err != nil {
		return fmt.Errorf("validator: register %q: %w", tag, err)
	}
	return nil
}
