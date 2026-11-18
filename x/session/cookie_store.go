package session

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"time"
)

// CookieStore provides a stateless session store that encrypts session data using AES-256-GCM.
type CookieStore struct {
	mu        sync.RWMutex
	aead      cipher.AEAD
	cache     map[string]memoryEntry // in-memory transit cache for the Store interface
	keyLength int
}

// NewCookieStore constructs a CookieStore using a 32-byte AES key.
func NewCookieStore(key []byte) (*CookieStore, error) {
	if len(key) != 32 {
		return nil, errors.New("session: cookie store key must be exactly 32 bytes for AES-256")
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	return &CookieStore{
		aead:      gcm,
		cache:     make(map[string]memoryEntry),
		keyLength: len(key),
	}, nil
}

// Encrypt serializes and encrypts session Data into a base64-URL string.
func (c *CookieStore) Encrypt(data *Data) (string, error) {
	if data == nil {
		return "", ErrInvalidSession
	}

	raw, err := json.Marshal(data)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}

	ciphertext := c.aead.Seal(nonce, nonce, raw, nil)
	return base64.RawURLEncoding.EncodeToString(ciphertext), nil
}

// Decrypt parses and decrypts a base64-URL ciphertext string into session Data.
func (c *CookieStore) Decrypt(encoded string) (*Data, error) {
	if encoded == "" {
		return nil, ErrSessionNotFound
	}

	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, ErrInvalidSession
	}

	nonceSize := c.aead.NonceSize()
	if len(raw) < nonceSize {
		return nil, ErrInvalidSession
	}

	nonce, ciphertext := raw[:nonceSize], raw[nonceSize:]
	plaintext, err := c.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, ErrInvalidSession
	}

	var data Data
	if err := json.Unmarshal(plaintext, &data); err != nil {
		return nil, ErrInvalidSession
	}

	return &data, nil
}

// Load retrieves session data from the store.
func (c *CookieStore) Load(ctx context.Context, id string) (*Data, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if id == "" {
		return nil, ErrSessionNotFound
	}

	// First try decrypting if id is ciphertext
	if data, err := c.Decrypt(id); err == nil {
		return data, nil
	}

	c.mu.RLock()
	entry, exists := c.cache[id]
	c.mu.RUnlock()

	if !exists {
		return nil, ErrSessionNotFound
	}

	if time.Now().After(entry.expiresAt) {
		c.mu.Lock()
		delete(c.cache, id)
		c.mu.Unlock()
		return nil, ErrSessionNotFound
	}

	var data Data
	if err := json.Unmarshal(entry.data, &data); err != nil {
		return nil, ErrInvalidSession
	}

	return &data, nil
}

// Save stores session data in transit cache.
func (c *CookieStore) Save(ctx context.Context, id string, data *Data, ttl time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if id == "" || data == nil {
		return ErrInvalidSession
	}

	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.cache[id] = memoryEntry{
		data:      raw,
		expiresAt: time.Now().Add(ttl),
	}
	return nil
}

// Destroy deletes session data from transit cache.
func (c *CookieStore) Destroy(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.cache, id)
	return nil
}

// Touch extends session expiration in transit cache.
func (c *CookieStore) Touch(ctx context.Context, id string, ttl time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, exists := c.cache[id]
	if !exists || time.Now().After(entry.expiresAt) {
		delete(c.cache, id)
		return ErrSessionNotFound
	}

	entry.expiresAt = time.Now().Add(ttl)
	c.cache[id] = entry
	return nil
}
