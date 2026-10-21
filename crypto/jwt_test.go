package crypto_test

import (
	"crypto/rand"
	"crypto/rsa"
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

func generateRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	return key
}

func TestSignVerifyRS256(t *testing.T) {
	key := generateRSAKey(t)
	claims := crypto.Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "user-rs",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
		UserID: "user-rs",
		Roles:  []string{"viewer"},
	}

	token, err := crypto.SignRS256(claims, key)
	if err != nil {
		t.Fatalf("SignRS256: %v", err)
	}
	if token == "" {
		t.Fatal("expected non-empty token")
	}

	parsed, err := crypto.VerifyRS256(token, &key.PublicKey)
	if err != nil {
		t.Fatalf("VerifyRS256: %v", err)
	}
	if parsed.UserID != claims.UserID {
		t.Errorf("expected UserID %q, got %q", claims.UserID, parsed.UserID)
	}
}

func TestVerifyRS256WrongKey(t *testing.T) {
	key := generateRSAKey(t)
	wrongKey := generateRSAKey(t)

	claims := crypto.Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "user-rs",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	token, _ := crypto.SignRS256(claims, key)

	_, err := crypto.VerifyRS256(token, &wrongKey.PublicKey)
	if err == nil {
		t.Fatal("expected error verifying with wrong public key")
	}
}

func TestVerifyHS256WrongAlgorithm(t *testing.T) {
	key := generateRSAKey(t)
	claims := crypto.Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "alg-test",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	token, _ := crypto.SignRS256(claims, key)
	_, err := crypto.VerifyHS256(token, []byte("any-secret"))
	if err == nil {
		t.Fatal("expected error when verifying RS256 token as HS256")
	}
}

func TestVerifyRS256WrongAlgorithm(t *testing.T) {
	secret := []byte("some-secret")
	claims := crypto.Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "alg-test",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	token, _ := crypto.SignHS256(claims, secret)
	key := generateRSAKey(t)
	_, err := crypto.VerifyRS256(token, &key.PublicKey)
	if err == nil {
		t.Fatal("expected error when verifying HS256 token as RS256")
	}
}
