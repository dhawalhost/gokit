package kms_test

import (
	"context"
	"crypto/rand"
	"io"
	"testing"

	"github.com/dhawalhost/gokit/crypto/kms"
)

func generateTestKey() []byte {
	k := make([]byte, 32)
	_, _ = io.ReadFull(rand.Reader, k)
	return k
}

func TestEnvelopeSerialization(t *testing.T) {
	env := &kms.Envelope{
		KeyID:        "kek-primary",
		EncryptedKey: []byte("encrypted-dek-bytes"),
		Ciphertext:   []byte("secret-ciphertext-payload"),
		Nonce:        []byte("12-byte-nonce"),
		Algorithm:    kms.AlgorithmAES256GCM,
	}

	// 1. JSON Roundtrip
	data, err := env.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded kms.Envelope
	if err := decoded.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.KeyID != env.KeyID || string(decoded.Ciphertext) != string(env.Ciphertext) {
		t.Errorf("decoded does not match original: %+v", decoded)
	}

	// 2. Base64 Roundtrip
	b64, err := env.MarshalBase64()
	if err != nil {
		t.Fatalf("MarshalBase64: %v", err)
	}

	var decodedB64 kms.Envelope
	if err := decodedB64.UnmarshalBase64(b64); err != nil {
		t.Fatalf("UnmarshalBase64: %v", err)
	}
	if decodedB64.KeyID != env.KeyID {
		t.Errorf("decodedB64 mismatch: %+v", decodedB64)
	}

	// 3. Invalid unmarshal
	var bad kms.Envelope
	if err := bad.Unmarshal([]byte(`{"key_id":"test"}`)); err != kms.ErrInvalidEnvelope {
		t.Errorf("expected ErrInvalidEnvelope on missing fields, got %v", err)
	}
}

func TestLocalKeyProvider_Validation(t *testing.T) {
	// Empty keys
	_, err := kms.NewLocalKeyProvider(nil, "")
	if err == nil {
		t.Error("expected error with empty master keys")
	}

	// Short key
	badKeys := map[string][]byte{"k1": []byte("too-short")}
	_, err = kms.NewLocalKeyProvider(badKeys, "k1")
	if err == nil {
		t.Error("expected error with < 32 byte key")
	}

	// Default key missing
	validKeys := map[string][]byte{"k1": generateTestKey()}
	_, err = kms.NewLocalKeyProvider(validKeys, "nonexistent")
	if err == nil {
		t.Error("expected error when default key is not in map")
	}
}

func TestEnvelopeCipher_EncryptDecrypt(t *testing.T) {
	k1 := generateTestKey()
	k2 := generateTestKey()

	provider, err := kms.NewLocalKeyProvider(map[string][]byte{
		"v1": k1,
		"v2": k2,
	}, "v1")
	if err != nil {
		t.Fatalf("NewLocalKeyProvider: %v", err)
	}

	cipher := kms.NewEnvelopeCipher(provider)
	ctx := context.Background()

	plaintext := []byte("confidential credit card data: 4111-2222-3333-4444")
	aad := []byte("tenant_id:acme_corp")

	// 1. Encrypt with v1
	env, err := cipher.Encrypt(ctx, "v1", plaintext, aad)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if env.KeyID != "v1" {
		t.Errorf("expected KeyID v1, got %s", env.KeyID)
	}
	if string(env.Ciphertext) == string(plaintext) {
		t.Error("ciphertext must not equal plaintext")
	}

	// 2. Decrypt with correct AAD
	decrypted, err := cipher.Decrypt(ctx, env, aad)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if string(decrypted) != string(plaintext) {
		t.Errorf("decrypted=%s, want=%s", string(decrypted), string(plaintext))
	}

	// 3. Decrypt with tampered AAD should fail
	_, err = cipher.Decrypt(ctx, env, []byte("tenant_id:evil_corp"))
	if err == nil {
		t.Error("expected decryption error with tampered AAD")
	}

	// 4. Decrypt with tampered ciphertext should fail
	tamperedEnv := *env
	tamperedEnv.Ciphertext = make([]byte, len(env.Ciphertext))
	copy(tamperedEnv.Ciphertext, env.Ciphertext)
	tamperedEnv.Ciphertext[0] ^= 0xFF
	_, err = cipher.Decrypt(ctx, &tamperedEnv, aad)
	if err == nil {
		t.Error("expected decryption error with tampered ciphertext")
	}
}

func TestEnvelopeCipher_StringHelpers(t *testing.T) {
	provider, _ := kms.NewLocalKeyProvider(map[string][]byte{
		"master": generateTestKey(),
	}, "master")
	cipher := kms.NewEnvelopeCipher(provider)
	ctx := context.Background()

	original := "sensitive-user-ssn-999-00-1111"
	b64Env, err := cipher.EncryptString(ctx, "master", original)
	if err != nil {
		t.Fatalf("EncryptString: %v", err)
	}

	recovered, err := cipher.DecryptString(ctx, b64Env)
	if err != nil {
		t.Fatalf("DecryptString: %v", err)
	}
	if recovered != original {
		t.Errorf("recovered=%q, want=%q", recovered, original)
	}
}

func TestEnvelopeCipher_KeyRotation(t *testing.T) {
	k1 := generateTestKey()
	k2 := generateTestKey()

	provider, _ := kms.NewLocalKeyProvider(map[string][]byte{
		"2025-key": k1,
		"2026-key": k2,
	}, "2026-key")
	cipher := kms.NewEnvelopeCipher(provider)
	ctx := context.Background()

	// Data encrypted under older 2025 key
	data := "historical-contract-record"
	envOld, err := cipher.Encrypt(ctx, "2025-key", []byte(data))
	if err != nil {
		t.Fatalf("Encrypt old: %v", err)
	}

	// Decrypt works seamlessly because envelope retains KeyID="2025-key"
	decrypted, err := cipher.Decrypt(ctx, envOld)
	if err != nil {
		t.Fatalf("Decrypt old: %v", err)
	}
	if string(decrypted) != data {
		t.Errorf("decrypted=%q, want=%q", string(decrypted), data)
	}
}
