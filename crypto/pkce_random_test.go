package crypto_test

import (
	"testing"

	"github.com/dhawalhost/gokit/crypto"
)

func TestPKCEVerifierChallenge(t *testing.T) {
	verifier, err := crypto.GenerateCodeVerifier()
	if err != nil {
		t.Fatalf("GenerateCodeVerifier: %v", err)
	}
	if len(verifier) == 0 {
		t.Fatal("expected non-empty verifier")
	}

	challenge := crypto.GenerateCodeChallenge(verifier)
	if len(challenge) == 0 {
		t.Fatal("expected non-empty challenge")
	}

	if !crypto.VerifyCodeChallenge(verifier, challenge) {
		t.Error("VerifyCodeChallenge should return true for matching pair")
	}
}

func TestPKCEVerifyFails(t *testing.T) {
	verifier, _ := crypto.GenerateCodeVerifier()
	if crypto.VerifyCodeChallenge(verifier, "wrong-challenge") {
		t.Error("VerifyCodeChallenge should return false for wrong challenge")
	}
}

func TestRandomBytes(t *testing.T) {
	b, err := crypto.RandomBytes(32)
	if err != nil {
		t.Fatalf("RandomBytes: %v", err)
	}
	if len(b) != 32 {
		t.Errorf("expected 32 bytes, got %d", len(b))
	}
	b2, _ := crypto.RandomBytes(32)
	same := true
	for i := range b {
		if b[i] != b2[i] {
			same = false
			break
		}
	}
	if same {
		t.Error("expected different random bytes")
	}
}

func TestRandomHex(t *testing.T) {
	h, err := crypto.RandomHex(16)
	if err != nil {
		t.Fatalf("RandomHex: %v", err)
	}
	if len(h) != 32 { // 16 bytes → 32 hex chars
		t.Errorf("expected 32 hex chars, got %d", len(h))
	}
}

func TestRandomInt(t *testing.T) {
	n, err := crypto.RandomInt(1, 100)
	if err != nil {
		t.Fatalf("RandomInt: %v", err)
	}
	if n < 1 || n > 100 {
		t.Errorf("expected value in [1,100], got %d", n)
	}
}

func TestRandomIntBadRange(t *testing.T) {
	_, err := crypto.RandomInt(100, 1)
	if err == nil {
		t.Fatal("expected error for invalid range")
	}
}
