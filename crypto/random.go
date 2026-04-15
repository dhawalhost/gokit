package crypto

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math/big"
)

// RandomBytes returns n cryptographically random bytes.
func RandomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("crypto: random bytes: %w", err)
	}
	return b, nil
}

// RandomString returns a URL-safe base64 string of n random bytes.
func RandomString(n int) (string, error) {
	b, err := RandomBytes(n)
	if err != nil {
		return "", err
	}
	// Use hex to ensure URL-safe output without padding.
	return hex.EncodeToString(b)[:n], nil
}

// RandomHex returns a hex-encoded string of n random bytes (length 2*n).
func RandomHex(n int) (string, error) {
	b, err := RandomBytes(n)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// RandomInt returns a random int64 in [min, max].
func RandomInt(min, max int64) (int64, error) {
	if max <= min {
		return 0, ErrMaxLessThanMin
	}
	diff := big.NewInt(max - min + 1)
	n, err := rand.Int(rand.Reader, diff)
	if err != nil {
		return 0, fmt.Errorf("crypto: random int: %w", err)
	}
	return n.Int64() + min, nil
}
