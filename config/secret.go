package config

import (
	"encoding/json"
	"log/slog"

	"github.com/dhawalhost/gokit/secrets"
)

// Secret wraps a sensitive string value and prevents it from being leaked
// in logs, error traces, or serialized output.
// It is specifically designed as an opt-in type for static configuration structs
// (e.g., credentials loaded from YAML, JSON, or environment flags).
//
// For enterprise dynamic secret management (e.g., loading from HashiCorp Vault,
// AWS Secrets Manager, automatic rotation, and secure memory zeroing via .Zero()),
// use the gokit/secrets package instead.
type Secret string

// Expose returns the underlying sensitive secret value.
// It is explicitly named to make secret access intentional and easily auditable.
func (s Secret) Expose() string {
	return string(s)
}

// Get is an alias for Expose, returning the raw string value.
func (s Secret) Get() string {
	return string(s)
}

// ToSecret converts the static config.Secret into a managed *secrets.Secret
// with memory zeroing capabilities.
func (s Secret) ToSecret(opts ...secrets.SecretOption) *secrets.Secret {
	return secrets.NewSecret(string(s), opts...)
}

// String implements fmt.Stringer to ensure that the secret is redacted
// when printed using %s or %v.
func (s Secret) String() string {
	return MaskedSecretPlaceholder
}

// GoString implements fmt.GoStringer to ensure that the secret is redacted
// when printed using %#v.
func (s Secret) GoString() string {
	return "config.Secret(\"" + MaskedSecretPlaceholder + "\")"
}

// MarshalJSON implements json.Marshaler to ensure that the secret is redacted
// when marshaled to JSON.
func (s Secret) MarshalJSON() ([]byte, error) {
	return json.Marshal(MaskedSecretPlaceholder)
}

// MarshalText implements encoding.TextMarshaler to ensure redaction in YAML, TOML, etc.
func (s Secret) MarshalText() ([]byte, error) {
	return []byte(MaskedSecretPlaceholder), nil
}

// LogValue implements slog.LogValuer to prevent leaks in standard structured logs.
func (s Secret) LogValue() slog.Value {
	return slog.StringValue(MaskedSecretPlaceholder)
}

// UnmarshalJSON implements json.Unmarshaler to allow loading the secret
// from JSON sources while preserving its sensitive nature.
func (s *Secret) UnmarshalJSON(b []byte) error {
	var val string
	if err := json.Unmarshal(b, &val); err != nil {
		return err
	}
	*s = Secret(val)
	return nil
}

// UnmarshalText implements encoding.TextUnmarshaler to allow loading the secret
// from YAML, TOML, and environment variable sources.
func (s *Secret) UnmarshalText(text []byte) error {
	*s = Secret(text)
	return nil
}
