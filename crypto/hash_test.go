package crypto_test

import (
	"testing"

	"github.com/dhawalhost/gokit/crypto"
)

func TestHashPassword(t *testing.T) {
	hash, err := crypto.HashPassword("secret123")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if len(hash) == 0 {
		t.Fatal("expected non-empty hash")
	}
}

func TestCheckPasswordValid(t *testing.T) {
	hash, _ := crypto.HashPassword("secret123")
	if err := crypto.CheckPassword("secret123", hash); err != nil {
		t.Errorf("CheckPassword valid: %v", err)
	}
}

func TestCheckPasswordInvalid(t *testing.T) {
	hash, _ := crypto.HashPassword("secret123")
	if err := crypto.CheckPassword("wrongpassword", hash); err == nil {
		t.Error("expected error for wrong password")
	}
}

func TestSHA256(t *testing.T) {
	h1 := crypto.SHA256([]byte("hello"))
	h2 := crypto.SHA256([]byte("hello"))
	if h1 != h2 {
		t.Error("SHA256 should be deterministic")
	}
	if h1 == crypto.SHA256([]byte("world")) {
		t.Error("SHA256 should differ for different inputs")
	}
	if len(h1) != 64 {
		t.Errorf("SHA256 hex should be 64 chars, got %d", len(h1))
	}
}

func TestSHA512(t *testing.T) {
	h := crypto.SHA512([]byte("hello"))
	if len(h) != 128 {
		t.Errorf("SHA512 hex should be 128 chars, got %d", len(h))
	}
}

func TestHMACSHA256(t *testing.T) {
	key := []byte("secret-key")
	mac1 := crypto.HMACSHA256([]byte("data"), key)
	mac2 := crypto.HMACSHA256([]byte("data"), key)
	if mac1 != mac2 {
		t.Error("HMAC should be deterministic")
	}
	mac3 := crypto.HMACSHA256([]byte("other"), key)
	if mac1 == mac3 {
		t.Error("HMAC should differ for different data")
	}
}
