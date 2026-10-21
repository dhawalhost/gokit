package crypto_test

import (
	"bytes"
	"testing"

	"github.com/dhawalhost/gokit/crypto"
)

var testKey = []byte("12345678901234567890123456789012") // 32 bytes

func TestEncryptDecrypt(t *testing.T) {
	plain := []byte("hello world")
	ct, err := crypto.Encrypt(plain, testKey)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	pt, err := crypto.Decrypt(ct, testKey)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(pt, plain) {
		t.Errorf("expected %q, got %q", plain, pt)
	}
}

func TestEncryptProducesUniqueOutputs(t *testing.T) {
	plain := []byte("same input")
	ct1, _ := crypto.Encrypt(plain, testKey)
	ct2, _ := crypto.Encrypt(plain, testKey)
	if bytes.Equal(ct1, ct2) {
		t.Error("expected different ciphertexts due to random nonce")
	}
}

func TestDecryptWrongKey(t *testing.T) {
	ct, _ := crypto.Encrypt([]byte("secret"), testKey)
	wrongKey := []byte("00000000000000000000000000000000")
	_, err := crypto.Decrypt(ct, wrongKey)
	if err == nil {
		t.Fatal("expected error when decrypting with wrong key")
	}
}

func TestDecryptTooShort(t *testing.T) {
	_, err := crypto.Decrypt([]byte("short"), testKey)
	if err == nil {
		t.Fatal("expected error for too-short ciphertext")
	}
}

func TestEncryptDecryptString(t *testing.T) {
	plain := "hello, crypto!"
	ct, err := crypto.EncryptString(plain, testKey)
	if err != nil {
		t.Fatalf("EncryptString: %v", err)
	}
	pt, err := crypto.DecryptString(ct, testKey)
	if err != nil {
		t.Fatalf("DecryptString: %v", err)
	}
	if pt != plain {
		t.Errorf("expected %q, got %q", plain, pt)
	}
}

func TestEncryptBadKey(t *testing.T) {
	_, err := crypto.Encrypt([]byte("plain"), []byte("short"))
	if err == nil {
		t.Fatal("expected error for bad key length")
	}
}

func TestEncryptStringBadKey(t *testing.T) {
	_, err := crypto.EncryptString("plaintext", []byte("short"))
	if err == nil {
		t.Fatal("expected error for bad key length")
	}
}

func TestDecryptStringInvalidHex(t *testing.T) {
	_, err := crypto.DecryptString("not-valid-hex!!", testKey)
	if err == nil {
		t.Fatal("expected error for invalid hex string")
	}
}

func TestDecryptStringBadKey(t *testing.T) {
	ct, err := crypto.EncryptString("plaintext", testKey)
	if err != nil {
		t.Fatalf("EncryptString: %v", err)
	}
	wrongKey := []byte("00000000000000000000000000000000")
	_, err = crypto.DecryptString(ct, wrongKey)
	if err == nil {
		t.Fatal("expected error when decrypting with wrong key")
	}
}
