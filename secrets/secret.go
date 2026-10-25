package secrets

import (
	"encoding/json"
	"time"
)

// Secret wraps sensitive values, preventing accidental exposure in logs, JSON, or stack traces,
// while providing explicit retrieval and secure memory zeroing.
type Secret struct {
	data      []byte
	version   string
	metadata  map[string]string
	updatedAt time.Time
}

// SecretOption configures optional metadata on a Secret.
type SecretOption func(*Secret)

// WithVersion sets the revision or version identifier for the secret.
func WithVersion(version string) SecretOption {
	return func(s *Secret) {
		s.version = version
	}
}

// WithMetadata attaches key-value metadata tags to the secret.
func WithMetadata(meta map[string]string) SecretOption {
	return func(s *Secret) {
		s.metadata = meta
	}
}

// NewSecret creates a Secret from a plaintext string.
func NewSecret(val string, opts ...SecretOption) *Secret {
	return NewSecretBytes([]byte(val), opts...)
}

// NewSecretBytes creates a Secret from a byte slice.
func NewSecretBytes(val []byte, opts ...SecretOption) *Secret {
	buf := make([]byte, len(val))
	copy(buf, val)

	s := &Secret{
		data:      buf,
		updatedAt: time.Now().UTC(),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Value returns the unmasked string value of the secret.
func (s *Secret) Value() string {
	if s == nil || len(s.data) == 0 {
		return ""
	}
	return string(s.data)
}

// Bytes returns a defensive copy of the raw secret bytes.
func (s *Secret) Bytes() []byte {
	if s == nil || len(s.data) == 0 {
		return nil
	}
	buf := make([]byte, len(s.data))
	copy(buf, s.data)
	return buf
}

// Version returns the secret version, if configured.
func (s *Secret) Version() string {
	if s == nil {
		return ""
	}
	return s.version
}

// Metadata returns a defensive copy of the secret metadata tags.
func (s *Secret) Metadata() map[string]string {
	if s == nil || s.metadata == nil {
		return nil
	}
	copied := make(map[string]string, len(s.metadata))
	for k, v := range s.metadata {
		copied[k] = v
	}
	return copied
}

// UpdatedAt returns the timestamp when the secret was created or loaded.
func (s *Secret) UpdatedAt() time.Time {
	if s == nil {
		return time.Time{}
	}
	return s.updatedAt
}

// String masks the secret value to prevent accidental leakage in fmt.Println, logs, or prints.
func (s *Secret) String() string {
	return "[REDACTED]"
}

// GoString masks the secret value when formatted with %#v.
func (s *Secret) GoString() string {
	return "[REDACTED]"
}

// MarshalJSON overrides JSON serialization to prevent leaking secret contents.
func (s *Secret) MarshalJSON() ([]byte, error) {
	return json.Marshal("[REDACTED]")
}

// MarshalText overrides text serialization to prevent leaking secret contents.
func (s *Secret) MarshalText() ([]byte, error) {
	return []byte("[REDACTED]"), nil
}

// Zero securely scrubs the secret payload from memory.
func (s *Secret) Zero() {
	if s == nil || len(s.data) == 0 {
		return
	}
	for i := range s.data {
		s.data[i] = 0
	}
	s.data = nil
}
