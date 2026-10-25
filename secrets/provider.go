package secrets

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Provider abstracts storage and retrieval of secrets across backend systems.
type Provider interface {
	Get(ctx context.Context, key string) (*Secret, error)
	Set(ctx context.Context, key string, secret *Secret) error
	Delete(ctx context.Context, key string) error
}

// MemoryProvider is a thread-safe in-memory Provider suitable for testing and local environments.
type MemoryProvider struct {
	mu      sync.RWMutex
	secrets map[string]*Secret
}

// NewMemoryProvider initializes an empty in-memory secret provider.
func NewMemoryProvider() *MemoryProvider {
	return &MemoryProvider{
		secrets: make(map[string]*Secret),
	}
}

// Get fetches a secret by key from memory.
func (m *MemoryProvider) Get(ctx context.Context, key string) (*Secret, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()

	sec, exists := m.secrets[key]
	if !exists {
		return nil, ErrSecretNotFound
	}
	return sec, nil
}

// Set stores a secret in memory.
func (m *MemoryProvider) Set(ctx context.Context, key string, secret *Secret) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if secret == nil {
		return ErrInvalidSecret
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.secrets[key] = secret
	return nil
}

// Delete removes a secret from memory.
func (m *MemoryProvider) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.secrets[key]; !exists {
		return ErrSecretNotFound
	}
	delete(m.secrets, key)
	return nil
}

// EnvProvider retrieves secrets from process environment variables.
type EnvProvider struct {
	prefix string
}

// NewEnvProvider creates a new EnvProvider with an optional prefix (e.g. "APP_").
func NewEnvProvider(prefix string) *EnvProvider {
	return &EnvProvider{
		prefix: prefix,
	}
}

// Get looks up a secret from environment variables.
// Key names with dots or hyphens are normalized to uppercase with underscores.
func (e *EnvProvider) Get(ctx context.Context, key string) (*Secret, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	envKey := e.normalizeKey(key)
	val, exists := os.LookupEnv(envKey)
	if !exists {
		return nil, ErrSecretNotFound
	}

	return NewSecret(val), nil
}

// Set sets an environment variable for the secret.
func (e *EnvProvider) Set(ctx context.Context, key string, secret *Secret) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if secret == nil {
		return ErrInvalidSecret
	}

	envKey := e.normalizeKey(key)
	return os.Setenv(envKey, secret.Value())
}

// Delete unsets the environment variable.
func (e *EnvProvider) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	envKey := e.normalizeKey(key)
	if _, exists := os.LookupEnv(envKey); !exists {
		return ErrSecretNotFound
	}
	return os.Unsetenv(envKey)
}

func (e *EnvProvider) normalizeKey(key string) string {
	normalized := strings.ToUpper(key)
	normalized = strings.ReplaceAll(normalized, ".", "_")
	normalized = strings.ReplaceAll(normalized, "-", "_")
	if e.prefix != "" && !strings.HasPrefix(normalized, e.prefix) {
		normalized = e.prefix + normalized
	}
	return normalized
}

// FileProvider reads secrets from individual files within a directory (Kubernetes secret volume mounts).
type FileProvider struct {
	dir string
}

// NewFileProvider creates a FileProvider pointing to a secrets directory.
func NewFileProvider(dir string) *FileProvider {
	return &FileProvider{
		dir: dir,
	}
}

// Get reads the content of a secret file.
func (f *FileProvider) Get(ctx context.Context, key string) (*Secret, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	cleanedKey := filepath.Clean(key)
	if strings.Contains(cleanedKey, "..") {
		return nil, ErrInvalidSecret
	}

	filePath := filepath.Join(f.dir, cleanedKey)
	data, err := os.ReadFile(filePath) // #nosec G304 -- path traversal validated above
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrSecretNotFound
		}
		return nil, err
	}

	return NewSecretBytes(data), nil
}

// Set writes a secret to a file.
func (f *FileProvider) Set(ctx context.Context, key string, secret *Secret) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if secret == nil {
		return ErrInvalidSecret
	}

	cleanedKey := filepath.Clean(key)
	if strings.Contains(cleanedKey, "..") {
		return ErrInvalidSecret
	}

	if err := os.MkdirAll(f.dir, 0700); err != nil {
		return err
	}

	filePath := filepath.Join(f.dir, cleanedKey)
	return os.WriteFile(filePath, secret.Bytes(), 0600)
}

// Delete removes the secret file.
func (f *FileProvider) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	cleanedKey := filepath.Clean(key)
	if strings.Contains(cleanedKey, "..") {
		return ErrInvalidSecret
	}

	filePath := filepath.Join(f.dir, cleanedKey)
	err := os.Remove(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return ErrSecretNotFound
		}
		return err
	}
	return nil
}

// ChainProvider evaluates multiple providers in order, falling back when ErrSecretNotFound occurs.
type ChainProvider struct {
	providers []Provider
}

// NewChainProvider creates a composite provider evaluating providers in prioritized order.
func NewChainProvider(providers ...Provider) *ChainProvider {
	return &ChainProvider{
		providers: providers,
	}
}

// Get checks each provider sequentially until a secret is found.
func (c *ChainProvider) Get(ctx context.Context, key string) (*Secret, error) {
	for _, p := range c.providers {
		sec, err := p.Get(ctx, key)
		if err == nil {
			return sec, nil
		}
		if !errors.Is(err, ErrSecretNotFound) {
			return nil, err
		}
	}
	return nil, ErrSecretNotFound
}

// Set writes the secret to the first provider in the chain.
func (c *ChainProvider) Set(ctx context.Context, key string, secret *Secret) error {
	if len(c.providers) == 0 {
		return ErrProviderUnavailable
	}
	return c.providers[0].Set(ctx, key, secret)
}

// Delete removes the secret from all providers in the chain that contain it.
func (c *ChainProvider) Delete(ctx context.Context, key string) error {
	deleted := false
	for _, p := range c.providers {
		if err := p.Delete(ctx, key); err == nil {
			deleted = true
		}
	}
	if !deleted {
		return ErrSecretNotFound
	}
	return nil
}
