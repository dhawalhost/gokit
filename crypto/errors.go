package crypto

import "errors"

// Sentinel errors returned by the crypto package.
var (
	// ErrInvalidKeySize is returned when the AES key is not exactly 32 bytes.
	ErrInvalidKeySize = errors.New("crypto: key must be exactly 32 bytes for AES-256")

	// ErrCiphertextTooShort is returned when the ciphertext is shorter than the GCM nonce size.
	ErrCiphertextTooShort = errors.New("crypto: ciphertext too short")

	// ErrInvalidTokenClaims is returned when a JWT token has unexpected claim types.
	ErrInvalidTokenClaims = errors.New("crypto: invalid token claims")

	// ErrUnexpectedSigningMethod is returned when a JWT uses an unexpected algorithm.
	ErrUnexpectedSigningMethod = errors.New("crypto: unexpected signing method")

	// ErrMaxLessThanMin is returned when RandomInt is called with max <= min.
	ErrMaxLessThanMin = errors.New("crypto: max must be greater than min")
)
