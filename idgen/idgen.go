// Package idgen provides ID generation utilities using UUID, ULID, and NanoID.
package idgen

import (
	"fmt"

	"github.com/google/uuid"
	gonanoid "github.com/matoous/go-nanoid/v2"
	"github.com/oklog/ulid/v2"
)

// NewUUID generates a new random UUID v4 string.
func NewUUID() string {
	return uuid.New().String()
}

// NewUUIDv7 generates a new UUID v7 (time-ordered) string.
func NewUUIDv7() string {
	id, err := uuid.NewV7()
	if err != nil {
		// Fall back to v4 on error (should not occur with standard rand).
		return uuid.New().String()
	}
	return id.String()
}

// MustParseUUID parses s as a UUID and panics if it is invalid.
func MustParseUUID(s string) uuid.UUID {
	id, err := uuid.Parse(s)
	if err != nil {
		panic(fmt.Sprintf("idgen: invalid UUID %q: %v", s, err))
	}
	return id
}

// IsValidUUID reports whether s is a valid UUID.
func IsValidUUID(s string) bool {
	_, err := uuid.Parse(s)
	return err == nil
}

// NewULID generates a new monotonically-increasing ULID string.
func NewULID() string {
	return ulid.Make().String()
}

// NewNanoID generates a new NanoID of the default length (21 characters).
func NewNanoID() (string, error) {
	return gonanoid.New()
}

// MustNanoID generates a new NanoID and panics on error.
func MustNanoID() string {
	id, err := gonanoid.New()
	if err != nil {
		panic(fmt.Sprintf("idgen: nanoid: %v", err))
	}
	return id
}

// NewNanoIDSize generates a new NanoID of the given size.
func NewNanoIDSize(size int) (string, error) {
	id, err := gonanoid.New(size)
	if err != nil {
		return "", fmt.Errorf("idgen: nanoid size %d: %w", size, err)
	}
	return id, nil
}
