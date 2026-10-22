package watcher

import (
	"context"
)

// Provider abstracts a configuration source capable of fetching data and
// notifying when underlying values change.
type Provider interface {
	// Name returns a human-readable identifier for the provider (e.g., "file", "polling", "memory").
	Name() string
	// Fetch retrieves the current configuration key-value map.
	Fetch(ctx context.Context) (map[string]any, error)
	// Watch starts a background listener that invokes onChange whenever values are updated.
	Watch(ctx context.Context, onChange func(values map[string]any, err error)) error
}
