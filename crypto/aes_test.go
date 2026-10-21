package crypto_test

import (
	"bytes"
	"testing"

	"github.com/dhawalhost/gokit/crypto"
)

var testKey = []byte("12345678901234567890123456789012") // 32 bytes

func TestAES_EncryptDecrypt_TableDriven(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "bytes encrypt and decrypt roundtrip matches plaintext",
			run: func(t *testing.T) {
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
			},
		},
		{
			name: "encrypt generates unique ciphertexts due to randomized nonce",
			run: func(t *testing.T) {
				plain := []byte("same input")
				ct1, _ := crypto.Encrypt(plain, testKey)
				ct2, _ := crypto.Encrypt(plain, testKey)
				if bytes.Equal(ct1, ct2) {
					t.Error("expected different ciphertexts due to random nonce")
				}
			},
		},
		{
			name: "string encrypt and decrypt roundtrip with hex encoding",
			run: func(t *testing.T) {
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
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, tc.run)
	}
}

func TestAES_Errors_TableDriven(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "decrypt with incorrect key fails authentication",
			run: func(t *testing.T) {
				ct, _ := crypto.Encrypt([]byte("secret"), testKey)
				wrongKey := []byte("00000000000000000000000000000000")
				_, err := crypto.Decrypt(ct, wrongKey)
				if err == nil {
					t.Fatal("expected error when decrypting with wrong key")
				}
			},
		},
		{
			name: "decrypt fails on ciphertext shorter than nonce prefix",
			run: func(t *testing.T) {
				_, err := crypto.Decrypt([]byte("short"), testKey)
				if err == nil {
					t.Fatal("expected error for too-short ciphertext")
				}
			},
		},
		{
			name: "encrypt fails on invalid key length",
			run: func(t *testing.T) {
				_, err := crypto.Encrypt([]byte("plain"), []byte("short"))
				if err == nil {
					t.Fatal("expected error for bad key length")
				}
			},
		},
		{
			name: "encrypt string fails on invalid key length",
			run: func(t *testing.T) {
				_, err := crypto.EncryptString("plaintext", []byte("short"))
				if err == nil {
					t.Fatal("expected error for bad key length")
				}
			},
		},
		{
			name: "decrypt string fails on invalid hex string",
			run: func(t *testing.T) {
				_, err := crypto.DecryptString("not-valid-hex!!", testKey)
				if err == nil {
					t.Fatal("expected error for invalid hex string")
				}
			},
		},
		{
			name: "decrypt string fails on incorrect key",
			run: func(t *testing.T) {
				ct, err := crypto.EncryptString("plaintext", testKey)
				if err != nil {
					t.Fatalf("EncryptString: %v", err)
				}
				wrongKey := []byte("00000000000000000000000000000000")
				_, err = crypto.DecryptString(ct, wrongKey)
				if err == nil {
					t.Fatal("expected error when decrypting with wrong key")
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, tc.run)
	}
}
