// Package secrets provides dynamic secret management, leak-proof memory scrubbing,
// provider abstractions (Env, File, Memory), in-memory TTL caching, and automatic rotation.
package secrets

import (
	"errors"
)

var (
	// ErrSecretNotFound is returned when a requested secret key does not exist.
	ErrSecretNotFound = errors.New("secrets: secret not found")
	// ErrProviderUnavailable is returned when the underlying secret store cannot be reached.
	ErrProviderUnavailable = errors.New("secrets: provider unavailable")
	// ErrInvalidSecret is returned when a secret payload is malformed or invalid.
	ErrInvalidSecret = errors.New("secrets: invalid secret")
	// ErrManagerClosed is returned when operations are attempted on a closed secret manager.
	ErrManagerClosed = errors.New("secrets: manager is closed")
)
