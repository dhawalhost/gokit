package crypto_test

import (
	"testing"
	"time"

	"github.com/dhawalhost/gokit/crypto"
	"github.com/golang-jwt/jwt/v5"
)

func TestSignVerifyHS256(t *testing.T) {
	secret := []byte("super-secret-key")
	claims := crypto.Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "user-123",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
		UserID: "user-123",
		Roles:  []string{"admin"},
	}

	token, err := crypto.SignHS256(claims, secret)
	if err != nil {
		t.Fatalf("SignHS256: %v", err)
	}
	if token == "" {
		t.Fatal("expected non-empty token")
	}

	parsed, err := crypto.VerifyHS256(token, secret)
	if err != nil {
		t.Fatalf("VerifyHS256: %v", err)
	}
	if parsed.UserID != claims.UserID {
		t.Errorf("expected UserID %q, got %q", claims.UserID, parsed.UserID)
	}
}

func TestVerifyHS256WrongSecret(t *testing.T) {
	secret := []byte("correct-secret")
	claims := crypto.Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "user-123",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	token, _ := crypto.SignHS256(claims, secret)
	_, err := crypto.VerifyHS256(token, []byte("wrong-secret"))
	if err == nil {
		t.Fatal("expected error for wrong secret")
	}
}
