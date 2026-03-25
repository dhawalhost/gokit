package crypto

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

// GenerateCodeVerifier generates a random PKCE code verifier (43-128 chars, base64url).
func GenerateCodeVerifier() (string, error) {
	b, err := RandomBytes(32)
	if err != nil {
		return "", fmt.Errorf("crypto: pkce verifier: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// GenerateCodeChallenge derives the S256 code challenge from verifier.
func GenerateCodeChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// VerifyCodeChallenge checks that challenge == S256(verifier).
func VerifyCodeChallenge(verifier, challenge string) bool {
	return GenerateCodeChallenge(verifier) == challenge
}
