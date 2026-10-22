package watcher

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// FetchFunc retrieves configuration values from a remote source (e.g. Vault, AWS SSM, HTTP API).
type FetchFunc func(ctx context.Context) (map[string]any, error)

// PollingProvider periodically polls a FetchFunc and emits updates only when the hash changes.
type PollingProvider struct {
	name     string
	fetchFn  FetchFunc
	interval time.Duration

	mu       sync.Mutex
	lastHash string
}

// NewPollingProvider constructs a PollingProvider with an interval and fetcher.
func NewPollingProvider(name string, interval time.Duration, fetchFn FetchFunc) *PollingProvider {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	return &PollingProvider{
		name:     name,
		fetchFn:  fetchFn,
		interval: interval,
	}
}

// Name returns the provider identifier.
func (p *PollingProvider) Name() string {
	return p.name
}

// Fetch executes a single fetch call.
func (p *PollingProvider) Fetch(ctx context.Context) (map[string]any, error) {
	return p.fetchFn(ctx)
}

// Watch starts the background polling loop and invokes onChange when data changes.
func (p *PollingProvider) Watch(ctx context.Context, onChange func(values map[string]any, err error)) error {
	go func() {
		ticker := time.NewTicker(p.interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				data, err := p.fetchFn(ctx)
				if err != nil {
					onChange(nil, err)
					continue
				}

				hash := computeHash(data)
				p.mu.Lock()
				if hash != p.lastHash {
					p.lastHash = hash
					p.mu.Unlock()
					onChange(data, nil)
				} else {
					p.mu.Unlock()
				}

			case <-ctx.Done():
				return
			}
		}
	}()

	return nil
}

func computeHash(data map[string]any) string {
	b, err := json.Marshal(data)
	if err != nil {
		return fmt.Sprintf("%v", data)
	}
	h := sha256.Sum256(b)
	return fmt.Sprintf("%x", h)
}
