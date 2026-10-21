package outbox

import (
	"context"
	"math/rand"
	"sync"
	"time"
)

// RelayConfig configures the background outbox polling and dispatch relay.
type RelayConfig struct {
	BatchSize     int           // Max messages fetched per poll cycle (default: 50)
	PollInterval  time.Duration // Frequency of polling pending messages (default: 500ms)
	MaxRetries    int           // Max delivery retry attempts before marking Failed (default: 5)
	BaseBackoff   time.Duration // Base exponential backoff duration (default: 1s)
	MaxBackoff    time.Duration // Cap on exponential backoff duration (default: 1m)
	Retention     time.Duration // Age after which published messages are cleaned up (default: 7 * 24h, 0 to disable)
	CleanInterval time.Duration // Interval to run cleanup of old published messages (default: 1h)
}

// Relay handles polling outbox messages from the Store and dispatching them through the Publisher.
type Relay struct {
	store     Store
	publisher Publisher
	cfg       RelayConfig

	stopCh chan struct{}
	wg     sync.WaitGroup
	mu     sync.Mutex
	active bool
}

// NewRelay constructs a new outbox Relay.
func NewRelay(store Store, publisher Publisher, cfg RelayConfig) *Relay {
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 50
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 500 * time.Millisecond
	}
	if cfg.MaxRetries <= 0 {
		cfg.MaxRetries = 5
	}
	if cfg.BaseBackoff <= 0 {
		cfg.BaseBackoff = 1 * time.Second
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = 1 * time.Minute
	}
	if cfg.Retention < 0 {
		cfg.Retention = 7 * 24 * time.Hour
	}
	if cfg.CleanInterval <= 0 {
		cfg.CleanInterval = 1 * time.Hour
	}

	return &Relay{
		store:     store,
		publisher: publisher,
		cfg:       cfg,
		stopCh:    make(chan struct{}),
	}
}

// Start spawns the background polling and cleanup routines.
func (r *Relay) Start(ctx context.Context) {
	r.mu.Lock()
	if r.active {
		r.mu.Unlock()
		return
	}
	r.active = true
	r.mu.Unlock()

	r.wg.Add(1)
	go r.pollLoop(ctx)

	if r.cfg.Retention > 0 {
		r.wg.Add(1)
		go r.cleanLoop(ctx)
	}
}

// Stop gracefully shuts down the background worker.
func (r *Relay) Stop() {
	r.mu.Lock()
	if !r.active {
		r.mu.Unlock()
		return
	}
	r.active = false
	close(r.stopCh)
	r.mu.Unlock()

	r.wg.Wait()
}

// ProcessBatch fetches and dispatches a single batch of pending messages.
// Returns the count of messages processed in this batch.
func (r *Relay) ProcessBatch(ctx context.Context) (int, error) {
	messages, err := r.store.FetchPending(ctx, r.cfg.BatchSize)
	if err != nil {
		return 0, err
	}
	if len(messages) == 0 {
		return 0, nil
	}

	for _, msg := range messages {
		pubErr := r.publisher.Publish(ctx, msg.Topic, msg.Key, msg.Payload, msg.Headers)
		if pubErr == nil {
			_ = r.store.MarkPublished(ctx, msg.ID)
			continue
		}

		// Calculate exponential backoff with jitter
		newRetry := msg.RetryCount + 1
		isFinal := newRetry >= r.cfg.MaxRetries

		shift := newRetry - 1
		if shift > 10 {
			shift = 10
		}
		backoff := r.cfg.BaseBackoff * time.Duration(1<<shift)
		if backoff > r.cfg.MaxBackoff {
			backoff = r.cfg.MaxBackoff
		}
		// Add up to 20% jitter
		jitter := time.Duration(rand.Int63n(int64(backoff/5 + 1))) //nolint:gosec // pseudo-random jitter is sufficient for retry backoff
		nextRetry := time.Now().UTC().Add(backoff + jitter)

		_ = r.store.MarkFailed(ctx, msg.ID, pubErr, nextRetry, isFinal)
	}

	return len(messages), nil
}

func (r *Relay) pollLoop(ctx context.Context) {
	defer r.wg.Done()
	ticker := time.NewTicker(r.cfg.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-r.stopCh:
			return
		case <-ticker.C:
			// Process up to exhaustion of queue
			for {
				count, err := r.ProcessBatch(ctx)
				if err != nil || count < r.cfg.BatchSize {
					break
				}
			}
		}
	}
}

func (r *Relay) cleanLoop(ctx context.Context) {
	defer r.wg.Done()
	ticker := time.NewTicker(r.cfg.CleanInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-r.stopCh:
			return
		case <-ticker.C:
			cutoff := time.Now().UTC().Add(-r.cfg.Retention)
			_, _ = r.store.Cleanup(ctx, cutoff)
		}
	}
}
