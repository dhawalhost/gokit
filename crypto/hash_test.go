package crypto_test

import (
	"testing"

	"github.com/dhawalhost/gokit/crypto"
)

func TestPasswordHashing_TableDriven(t *testing.T) {
	tests := []struct {
		name     string
		password string
		verify   func(t *testing.T, password, hash string)
	}{
		{
			name:     "valid password hashes and verifies successfully",
			password: "secret123",
			verify: func(t *testing.T, password, hash string) {
				if len(hash) == 0 {
					t.Fatal("expected non-empty hash")
				}
				if err := crypto.CheckPassword(password, hash); err != nil {
					t.Errorf("CheckPassword failed on valid password: %v", err)
				}
				if err := crypto.CheckPassword("wrongpassword", hash); err == nil {
					t.Error("expected error for wrong password")
				}
			},
		},
		{
			name:     "complex password with special characters",
			password: "P@$$w0rd!#%^&*()_+~`",
			verify: func(t *testing.T, password, hash string) {
				if err := crypto.CheckPassword(password, hash); err != nil {
					t.Errorf("CheckPassword failed on complex password: %v", err)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			hash, err := crypto.HashPassword(tc.password)
			if err != nil {
				t.Fatalf("HashPassword: %v", err)
			}
			tc.verify(t, tc.password, hash)
		})
	}
}

func TestDigests_TableDriven(t *testing.T) {
	tests := []struct {
		name       string
		input      []byte
		wantSHA256 int
		wantSHA512 int
	}{
		{
			name:       "short string input",
			input:      []byte("hello"),
			wantSHA256: 64,
			wantSHA512: 128,
		},
		{
			name:       "empty input",
			input:      []byte(""),
			wantSHA256: 64,
			wantSHA512: 128,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h256_1 := crypto.SHA256(tc.input)
			h256_2 := crypto.SHA256(tc.input)
			if h256_1 != h256_2 {
				t.Error("SHA256 must be deterministic")
			}
			if len(h256_1) != tc.wantSHA256 {
				t.Errorf("expected SHA256 hex length %d, got %d", tc.wantSHA256, len(h256_1))
			}

			h512 := crypto.SHA512(tc.input)
			if len(h512) != tc.wantSHA512 {
				t.Errorf("expected SHA512 hex length %d, got %d", tc.wantSHA512, len(h512))
			}
		})
	}
}

func TestHMACSHA256_TableDriven(t *testing.T) {
	key := []byte("secret-key")
	tests := []struct {
		name       string
		data       []byte
		tamperData []byte
	}{
		{
			name:       "standard payload",
			data:       []byte("data"),
			tamperData: []byte("tampered-data"),
		},
		{
			name:       "binary payload",
			data:       []byte("\x00\x01\x02\x03"),
			tamperData: []byte("\x00\x01\x02\x04"),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mac1 := crypto.HMACSHA256(tc.data, key)
			mac2 := crypto.HMACSHA256(tc.data, key)
			if mac1 != mac2 {
				t.Error("HMAC must be deterministic")
			}
			if !crypto.VerifyHMACSHA256(tc.data, key, mac1) {
				t.Error("VerifyHMACSHA256 failed for valid MAC")
			}
			if crypto.VerifyHMACSHA256(tc.tamperData, key, mac1) {
				t.Error("VerifyHMACSHA256 unexpectedly succeeded for tampered data")
			}
			if crypto.VerifyHMACSHA256(tc.data, key, "invalid-hex") {
				t.Error("VerifyHMACSHA256 unexpectedly succeeded for invalid hex")
			}
		})
	}
}
