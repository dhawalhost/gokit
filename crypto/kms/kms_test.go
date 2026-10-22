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

func TestEnvelopeSerialization_TableDriven(t *testing.T) {
	validEnv := &kms.Envelope{
		KeyID:        "kek-primary",
		EncryptedKey: []byte("encrypted-dek-bytes"),
		Ciphertext:   []byte("secret-ciphertext-payload"),
		Nonce:        []byte("12-byte-nonce"),
		Algorithm:    kms.AlgorithmAES256GCM,
	}

	validJSON, _ := validEnv.Marshal()
	validB64, _ := validEnv.MarshalBase64()

	tests := []struct {
		name      string
		format    string // "json" or "base64"
		input     string
		wantErr   bool
		checkProp func(t *testing.T, env *kms.Envelope)
	}{
		{
			name:    "valid JSON unmarshals all fields correctly",
			format:  "json",
			input:   string(validJSON),
			wantErr: false,
			checkProp: func(t *testing.T, env *kms.Envelope) {
				if env.KeyID != "kek-primary" || string(env.Ciphertext) != "secret-ciphertext-payload" {
					t.Errorf("unexpected unmarshaled envelope: %+v", env)
				}
			},
		},
		{
			name:    "valid Base64 unmarshals correctly",
			format:  "base64",
			input:   validB64,
			wantErr: false,
			checkProp: func(t *testing.T, env *kms.Envelope) {
				if env.KeyID != "kek-primary" {
					t.Errorf("unexpected key ID: %s", env.KeyID)
				}
			},
		},
		{
			name:    "JSON missing required encrypted_key returns error",
			format:  "json",
			input:   `{"key_id":"k1","ciphertext":"dGVzdA==","nonce":"bm9uY2U=","algorithm":"AES-256-GCM"}`,
			wantErr: true,
		},
		{
			name:    "JSON missing required ciphertext returns error",
			format:  "json",
			input:   `{"key_id":"k1","encrypted_key":"ZGVr","nonce":"bm9uY2U=","algorithm":"AES-256-GCM"}`,
			wantErr: true,
		},
		{
			name:    "malformed JSON returns error",
			format:  "json",
			input:   `{not-valid-json`,
			wantErr: true,
		},
		{
			name:    "invalid Base64 returns error",
			format:  "base64",
			input:   `!!!not-base-64!!!`,
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var env kms.Envelope
			var err error
			if tc.format == "json" {
				err = env.Unmarshal([]byte(tc.input))
			} else {
				err = env.UnmarshalBase64(tc.input)
			}

			if (err != nil) != tc.wantErr {
				t.Fatalf("wantErr=%v, got err=%v", tc.wantErr, err)
			}
			if !tc.wantErr && tc.checkProp != nil {
				tc.checkProp(t, &env)
			}
		})
	}
}

func TestLocalKeyProvider_TableDriven(t *testing.T) {
	valid32 := generateTestKey()

	tests := []struct {
		name       string
		masterKeys map[string][]byte
		defaultKey string
		wantErr    bool
	}{
		{
			name:       "nil master keys map returns error",
			masterKeys: nil,
			defaultKey: "",
			wantErr:    true,
		},
		{
			name:       "empty master keys map returns error",
			masterKeys: map[string][]byte{},
			defaultKey: "",
			wantErr:    true,
		},
		{
			name:       "key length less than 32 bytes returns error",
			masterKeys: map[string][]byte{"k1": []byte("too-short-16-byt")},
			defaultKey: "k1",
			wantErr:    true,
		},
		{
			name:       "key length greater than 32 bytes returns error",
			masterKeys: map[string][]byte{"k1": append(valid32, 0x01)},
			defaultKey: "k1",
			wantErr:    true,
		},
		{
			name:       "default key not in keys map returns error",
			masterKeys: map[string][]byte{"k1": valid32},
			defaultKey: "nonexistent",
			wantErr:    true,
		},
		{
			name:       "valid master key with explicit default passes",
			masterKeys: map[string][]byte{"k1": valid32},
			defaultKey: "k1",
			wantErr:    false,
		},
		{
			name:       "valid master key with empty default auto-selects key",
			masterKeys: map[string][]byte{"k1": valid32},
			defaultKey: "",
			wantErr:    false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := kms.NewLocalKeyProvider(tc.masterKeys, tc.defaultKey)
			if (err != nil) != tc.wantErr {
				t.Errorf("NewLocalKeyProvider() wantErr=%v, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestEnvelopeCipher_EncryptDecrypt_TableDriven(t *testing.T) {
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

	tests := []struct {
		name       string
		keyID      string
		plaintext  []byte
		aad        []byte
		tamperFunc func(env *kms.Envelope, aad *[]byte)
		wantErr    bool
	}{
		{
			name:       "successful encryption and decryption with AAD",
			keyID:      "v1",
			plaintext:  []byte("secret credit card: 4111-2222-3333-4444"),
			aad:        []byte("tenant:acme_corp"),
			tamperFunc: nil,
			wantErr:    false,
		},
		{
			name:       "successful encryption and decryption without AAD",
			keyID:      "v2",
			plaintext:  []byte("plain payload without auth data"),
			aad:        nil,
			tamperFunc: nil,
			wantErr:    false,
		},
		{
			name:      "tampered AAD fails authentication",
			keyID:     "v1",
			plaintext: []byte("sensitive document"),
			aad:       []byte("tenant:original"),
			tamperFunc: func(_ *kms.Envelope, aad *[]byte) {
				*aad = []byte("tenant:attacker")
			},
			wantErr: true,
		},
		{
			name:      "tampered ciphertext fails decryption",
			keyID:     "v1",
			plaintext: []byte("sensitive document"),
			aad:       []byte("tenant:acme"),
			tamperFunc: func(env *kms.Envelope, _ *[]byte) {
				env.Ciphertext[0] ^= 0xFF
			},
			wantErr: true,
		},
		{
			name:      "tampered encrypted DEK fails decryption",
			keyID:     "v1",
			plaintext: []byte("sensitive document"),
			aad:       nil,
			tamperFunc: func(env *kms.Envelope, _ *[]byte) {
				env.EncryptedKey[0] ^= 0xFF
			},
			wantErr: true,
		},
		{
			name:      "unsupported algorithm fails decryption",
			keyID:     "v1",
			plaintext: []byte("sensitive document"),
			aad:       nil,
			tamperFunc: func(env *kms.Envelope, _ *[]byte) {
				env.Algorithm = "UNSUPPORTED-CIPHER"
			},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env, err := cipher.Encrypt(ctx, tc.keyID, tc.plaintext, tc.aad)
			if err != nil {
				t.Fatalf("Encrypt error: %v", err)
			}

			decryptAAD := tc.aad
			if tc.tamperFunc != nil {
				tc.tamperFunc(env, &decryptAAD)
			}

			decrypted, err := cipher.Decrypt(ctx, env, decryptAAD)
			if (err != nil) != tc.wantErr {
				t.Fatalf("Decrypt() wantErr=%v, got err=%v", tc.wantErr, err)
			}
			if !tc.wantErr && string(decrypted) != string(tc.plaintext) {
				t.Errorf("decrypted=%q, want=%q", string(decrypted), string(tc.plaintext))
			}
		})
	}
}

func TestEnvelopeCipher_StringHelpers(t *testing.T) {
	provider, _ := kms.NewLocalKeyProvider(map[string][]byte{
		"master": generateTestKey(),
	}, "master")
	cipher := kms.NewEnvelopeCipher(provider)
	ctx := context.Background()

	tests := []struct {
		name      string
		plaintext string
		aad       []byte
	}{
		{"standard ssn string", "999-00-1111", []byte("user:101")},
		{"empty string", "", nil},
		{"long JSON payload", `{"account":"123456789","routing":"987654321","pin":"4321"}`, []byte("tenant:bank")},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b64Env, err := cipher.EncryptString(ctx, "master", tc.plaintext, tc.aad)
			if err != nil {
				t.Fatalf("EncryptString: %v", err)
			}

			recovered, err := cipher.DecryptString(ctx, b64Env, tc.aad)
			if err != nil {
				t.Fatalf("DecryptString: %v", err)
			}
			if recovered != tc.plaintext {
				t.Errorf("recovered=%q, want=%q", recovered, tc.plaintext)
			}
		})
	}
}

func TestEnvelope_EdgeCases_TableDriven(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "nil Envelope Marshal returns ErrInvalidEnvelope",
			run: func(t *testing.T) {
				var env *kms.Envelope
				_, err := env.Marshal()
				if err != kms.ErrInvalidEnvelope {
					t.Fatalf("expected ErrInvalidEnvelope, got %v", err)
				}
				_, err = env.MarshalBase64()
				if err != kms.ErrInvalidEnvelope {
					t.Fatalf("expected ErrInvalidEnvelope from MarshalBase64, got %v", err)
				}
			},
		},
		{
			name: "Unmarshal invalid or empty fields returns error",
			run: func(t *testing.T) {
				env := &kms.Envelope{}
				err := env.Unmarshal([]byte(`{"key_id":"k1"}`))
				if err != kms.ErrInvalidEnvelope {
					t.Fatalf("expected ErrInvalidEnvelope for missing fields, got %v", err)
				}
				err = env.Unmarshal([]byte(`invalid-json`))
				if err == nil {
					t.Fatal("expected error on invalid JSON")
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, tc.run)
	}
}

func TestKeyProvider_AndCipher_EdgeCases_TableDriven(t *testing.T) {
	key := generateTestKey()
	provider, err := kms.NewLocalKeyProvider(map[string][]byte{"k1": key}, "k1")
	if err != nil {
		t.Fatalf("NewLocalKeyProvider error: %v", err)
	}
	cipher := kms.NewEnvelopeCipher(provider)
	ctx := context.Background()

	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "GenerateDataKey returns ErrKeyNotFound for missing key",
			run: func(t *testing.T) {
				_, _, err := provider.GenerateDataKey(ctx, "nonexistent-key")
				if err == nil {
					t.Error("expected error for nonexistent key in GenerateDataKey")
				}
			},
		},
		{
			name: "DecryptDataKey returns ErrKeyNotFound for missing key",
			run: func(t *testing.T) {
				_, err := provider.DecryptDataKey(ctx, "nonexistent-key", []byte("encrypted"))
				if err == nil {
					t.Error("expected error for nonexistent key in DecryptDataKey")
				}
			},
		},
		{
			name: "DecryptDataKey returns error for invalid encrypted DEK",
			run: func(t *testing.T) {
				_, err := provider.DecryptDataKey(ctx, "k1", []byte("too-short"))
				if err == nil {
					t.Error("expected error for malformed ciphertext in DecryptDataKey")
				}
			},
		},
		{
			name: "EncryptString and DecryptString error handling",
			run: func(t *testing.T) {
				// Encrypt with unknown key
				if _, err := cipher.EncryptString(ctx, "unknown-key", "secret"); err == nil {
					t.Error("expected error when encrypting with unknown key")
				}

				// Decrypt with invalid base64
				if _, err := cipher.DecryptString(ctx, "not-valid-base64!!!"); err == nil {
					t.Error("expected error when decrypting invalid base64")
				}

				// Decrypt with base64 that is not a valid envelope
				if _, err := cipher.DecryptString(ctx, "aW52YWxpZCBqc29u"); err == nil { // "invalid json" in base64
					t.Error("expected error when decrypting non-envelope base64")
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, tc.run)
	}
}
