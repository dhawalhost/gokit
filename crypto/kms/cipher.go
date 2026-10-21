package kms

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
)

// ErrUnsupportedAlgorithm is returned if an envelope uses an unknown encryption scheme.
var ErrUnsupportedAlgorithm = errors.New("kms: unsupported envelope algorithm")

// EnvelopeCipher handles encrypting and decrypting data using the envelope encryption pattern.
type EnvelopeCipher struct {
	provider KeyProvider
}

// NewEnvelopeCipher constructs an EnvelopeCipher with the given KeyProvider.
func NewEnvelopeCipher(provider KeyProvider) *EnvelopeCipher {
	return &EnvelopeCipher{provider: provider}
}

// zeroBytes overwrites a slice with zeroes to clear sensitive key material from memory.
func zeroBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// Encrypt encrypts plaintext under a unique, ephemeral data key, protected by keyID.
func (c *EnvelopeCipher) Encrypt(ctx context.Context, keyID string, plaintext []byte, additionalData ...[]byte) (*Envelope, error) {
	plainDEK, encryptedDEK, err := c.provider.GenerateDataKey(ctx, keyID)
	if err != nil {
		return nil, fmt.Errorf("kms: generate DEK: %w", err)
	}
	defer zeroBytes(plainDEK)

	block, err := aes.NewCipher(plainDEK)
	if err != nil {
		return nil, fmt.Errorf("kms: aes cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("kms: new gcm: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("kms: read nonce: %w", err)
	}

	var aad []byte
	if len(additionalData) > 0 {
		aad = additionalData[0]
	}

	ciphertext := gcm.Seal(nil, nonce, plaintext, aad)

	return &Envelope{
		KeyID:        keyID,
		EncryptedKey: encryptedDEK,
		Ciphertext:   ciphertext,
		Nonce:        nonce,
		Algorithm:    AlgorithmAES256GCM,
	}, nil
}

// Decrypt unpacks an Envelope, decrypts its DEK via the provider, and recovers the plaintext.
func (c *EnvelopeCipher) Decrypt(ctx context.Context, env *Envelope, additionalData ...[]byte) ([]byte, error) {
	if env == nil {
		return nil, ErrInvalidEnvelope
	}
	if env.Algorithm != AlgorithmAES256GCM {
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedAlgorithm, env.Algorithm)
	}

	plainDEK, err := c.provider.DecryptDataKey(ctx, env.KeyID, env.EncryptedKey)
	if err != nil {
		return nil, fmt.Errorf("kms: decrypt DEK: %w", err)
	}
	defer zeroBytes(plainDEK)

	block, err := aes.NewCipher(plainDEK)
	if err != nil {
		return nil, fmt.Errorf("kms: aes cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("kms: new gcm: %w", err)
	}

	var aad []byte
	if len(additionalData) > 0 {
		aad = additionalData[0]
	}

	plaintext, err := gcm.Open(nil, env.Nonce, env.Ciphertext, aad)
	if err != nil {
		return nil, fmt.Errorf("kms: gcm open: %w", err)
	}

	return plaintext, nil
}

// EncryptString encrypts a string and returns a base64-encoded serialized envelope.
func (c *EnvelopeCipher) EncryptString(ctx context.Context, keyID, text string, additionalData ...[]byte) (string, error) {
	env, err := c.Encrypt(ctx, keyID, []byte(text), additionalData...)
	if err != nil {
		return "", err
	}
	return env.MarshalBase64()
}

// DecryptString decrypts a base64-encoded serialized envelope back to a string.
func (c *EnvelopeCipher) DecryptString(ctx context.Context, base64Envelope string, additionalData ...[]byte) (string, error) {
	var env Envelope
	if err := env.UnmarshalBase64(base64Envelope); err != nil {
		return "", err
	}
	plaintext, err := c.Decrypt(ctx, &env, additionalData...)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}
