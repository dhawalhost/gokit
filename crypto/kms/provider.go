package kms

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/dhawalhost/gokit/crypto"
)

var (
	// ErrKeyNotFound indicates the requested Master Key ID does not exist.
	ErrKeyNotFound = errors.New("kms: master key not found")
	// ErrInvalidMasterKey indicates the master key is not 32 bytes.
	ErrInvalidMasterKey = errors.New("kms: master key must be exactly 32 bytes for AES-256")
)

// KeyProvider handles generating and decrypting Data Encryption Keys (DEKs) using a Master Key (KEK).
type KeyProvider interface {
	GenerateDataKey(ctx context.Context, keyID string) (plainDEK, encryptedDEK []byte, err error)
	DecryptDataKey(ctx context.Context, keyID string, encryptedDEK []byte) (plainDEK []byte, err error)
}

// LocalKeyProvider implements KeyProvider using in-memory / local AES-256 master keys.
type LocalKeyProvider struct {
	mu         sync.RWMutex
	masterKeys map[string][]byte
	defaultKey string
}

// NewLocalKeyProvider creates a LocalKeyProvider from a map of 32-byte master keys.
func NewLocalKeyProvider(masterKeys map[string][]byte, defaultKeyID string) (*LocalKeyProvider, error) {
	if len(masterKeys) == 0 {
		return nil, errors.New("kms: at least one master key is required")
	}

	keysCopy := make(map[string][]byte, len(masterKeys))
	for k, v := range masterKeys {
		if len(v) != 32 {
			return nil, fmt.Errorf("%w: key %q has %d bytes", ErrInvalidMasterKey, k, len(v))
		}
		keyBytes := make([]byte, 32)
		copy(keyBytes, v)
		keysCopy[k] = keyBytes
	}

	if defaultKeyID == "" {
		for k := range keysCopy {
			defaultKeyID = k
			break
		}
	} else if _, ok := keysCopy[defaultKeyID]; !ok {
		return nil, fmt.Errorf("%w: default key %q not in master keys", ErrKeyNotFound, defaultKeyID)
	}

	return &LocalKeyProvider{
		masterKeys: keysCopy,
		defaultKey: defaultKeyID,
	}, nil
}

// GenerateDataKey creates a new 32-byte random DEK and encrypts it with the master key.
func (p *LocalKeyProvider) GenerateDataKey(_ context.Context, keyID string) ([]byte, []byte, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if keyID == "" {
		keyID = p.defaultKey
	}
	kek, ok := p.masterKeys[keyID]
	if !ok {
		return nil, nil, fmt.Errorf("%w: %q", ErrKeyNotFound, keyID)
	}

	// Generate 32-byte cryptographically secure random plaintext DEK
	plainDEK := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, plainDEK); err != nil {
		return nil, nil, fmt.Errorf("kms: generate random DEK: %w", err)
	}

	// Encrypt DEK with KEK using AES-256-GCM
	encryptedDEK, err := crypto.Encrypt(plainDEK, kek)
	if err != nil {
		return nil, nil, fmt.Errorf("kms: encrypt DEK: %w", err)
	}

	return plainDEK, encryptedDEK, nil
}

// DecryptDataKey decrypts an encrypted DEK using the specified master key.
func (p *LocalKeyProvider) DecryptDataKey(_ context.Context, keyID string, encryptedDEK []byte) ([]byte, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if keyID == "" {
		keyID = p.defaultKey
	}
	kek, ok := p.masterKeys[keyID]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrKeyNotFound, keyID)
	}

	plainDEK, err := crypto.Decrypt(encryptedDEK, kek)
	if err != nil {
		return nil, fmt.Errorf("kms: decrypt DEK: %w", err)
	}

	return plainDEK, nil
}
