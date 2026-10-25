package batcher

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// BatchLoadFunc resolves a slice of keys and returns a mapping from key to value.
type BatchLoadFunc[K comparable, V any] func(ctx context.Context, keys []K) (map[K]V, error)

type dlResult[V any] struct {
	val V
	err error
}

type dlRequest[K comparable, V any] struct {
	key    K
	respCh chan dlResult[V]
}

// DataLoaderConfig configures a DataLoader.
type DataLoaderConfig struct {
	MaxBatchSize int
	Wait         time.Duration
	Cache        bool
}

// DefaultDataLoaderConfig returns recommended production defaults for a DataLoader.
func DefaultDataLoaderConfig() DataLoaderConfig {
	return DataLoaderConfig{
		MaxBatchSize: 100,
		Wait:         5 * time.Millisecond,
		Cache:        true,
	}
}

// DataLoaderOption configures a DataLoader.
type DataLoaderOption func(*DataLoaderConfig)

// WithDataLoaderBatchSize sets the maximum number of keys to load in a single batch.
func WithDataLoaderBatchSize(size int) DataLoaderOption {
	return func(c *DataLoaderConfig) {
		if size > 0 {
			c.MaxBatchSize = size
		}
	}
}

// WithDataLoaderWait sets the maximum delay before executing an unfulfilled batch.
func WithDataLoaderWait(wait time.Duration) DataLoaderOption {
	return func(c *DataLoaderConfig) {
		if wait > 0 {
			c.Wait = wait
		}
	}
}

// WithDataLoaderCache enables or disables in-memory memoization of loaded keys.
func WithDataLoaderCache(enabled bool) DataLoaderOption {
	return func(c *DataLoaderConfig) {
		c.Cache = enabled
	}
}

// DataLoader coalesces individual read requests into batches within a short time window.
type DataLoader[K comparable, V any] struct {
	cfg       DataLoaderConfig
	fn        BatchLoadFunc[K, V]
	mu        sync.RWMutex
	cache     map[K]V
	reqCh     chan dlRequest[K, V]
	stopCh    chan struct{}
	stopped   atomic.Bool
	closeOnce sync.Once
	wg        sync.WaitGroup
}

// NewDataLoader initializes and starts a new DataLoader.
func NewDataLoader[K comparable, V any](fn BatchLoadFunc[K, V], opts ...DataLoaderOption) *DataLoader[K, V] {
	if fn == nil {
		panic("batcher: dataloader batch function cannot be nil")
	}

	cfg := DefaultDataLoaderConfig()
	for _, opt := range opts {
		opt(&cfg)
	}

	dl := &DataLoader[K, V]{
		cfg:    cfg,
		fn:     fn,
		cache:  make(map[K]V),
		reqCh:  make(chan dlRequest[K, V], cfg.MaxBatchSize*10),
		stopCh: make(chan struct{}),
	}

	dl.wg.Add(1)
	go dl.loop()

	return dl
}

// Load retrieves the value for a single key, coalescing it with other concurrent loads.
func (dl *DataLoader[K, V]) Load(ctx context.Context, key K) (V, error) {
	var zero V

	if dl.stopped.Load() {
		return zero, ErrBatcherClosed
	}

	if dl.cfg.Cache {
		dl.mu.RLock()
		cached, exists := dl.cache[key]
		dl.mu.RUnlock()
		if exists {
			return cached, nil
		}
	}

	respCh := make(chan dlResult[V], 1)
	req := dlRequest[K, V]{
		key:    key,
		respCh: respCh,
	}

	select {
	case <-ctx.Done():
		return zero, ctx.Err()
	case <-dl.stopCh:
		return zero, ErrBatcherClosed
	case dl.reqCh <- req:
	}

	select {
	case <-ctx.Done():
		return zero, ctx.Err()
	case res := <-respCh:
		if res.err == nil && dl.cfg.Cache {
			dl.mu.Lock()
			dl.cache[key] = res.val
			dl.mu.Unlock()
		}
		return res.val, res.err
	}
}

// LoadMany retrieves values for multiple keys concurrently.
func (dl *DataLoader[K, V]) LoadMany(ctx context.Context, keys []K) ([]V, []error) {
	results := make([]V, len(keys))
	errorsList := make([]error, len(keys))

	var wg sync.WaitGroup
	wg.Add(len(keys))

	for i, key := range keys {
		idx := i
		k := key
		go func() {
			defer wg.Done()
			val, err := dl.Load(ctx, k)
			results[idx] = val
			errorsList[idx] = err
		}()
	}

	wg.Wait()
	return results, errorsList
}

// Prime pre-populates the cache with a known key-value pair.
func (dl *DataLoader[K, V]) Prime(key K, value V) {
	dl.mu.Lock()
	defer dl.mu.Unlock()
	dl.cache[key] = value
}

// Clear invalidates a cached key.
func (dl *DataLoader[K, V]) Clear(key K) {
	dl.mu.Lock()
	defer dl.mu.Unlock()
	delete(dl.cache, key)
}

// ClearAll purges all memoized keys from cache.
func (dl *DataLoader[K, V]) ClearAll() {
	dl.mu.Lock()
	defer dl.mu.Unlock()
	dl.cache = make(map[K]V)
}

// Close terminates the DataLoader and drains pending batches.
func (dl *DataLoader[K, V]) Close() {
	dl.closeOnce.Do(func() {
		dl.stopped.Store(true)
		close(dl.stopCh)
		close(dl.reqCh)
	})
	dl.wg.Wait()
}

func (dl *DataLoader[K, V]) loop() {
	defer dl.wg.Done()

	var keys []K
	listeners := make(map[K][]chan dlResult[V])
	ticker := time.NewTicker(dl.cfg.Wait)
	defer ticker.Stop()

	flush := func() {
		if len(keys) == 0 {
			return
		}

		currentKeys := keys
		currentListeners := listeners

		keys = nil
		listeners = make(map[K][]chan dlResult[V])

		go dl.dispatchBatch(currentKeys, currentListeners)
	}

	for {
		select {
		case req, ok := <-dl.reqCh:
			if !ok {
				flush()
				return
			}

			if _, exists := listeners[req.key]; !exists {
				keys = append(keys, req.key)
			}
			listeners[req.key] = append(listeners[req.key], req.respCh)

			if len(keys) >= dl.cfg.MaxBatchSize {
				flush()
				ticker.Reset(dl.cfg.Wait)
			}

		case <-ticker.C:
			flush()
		}
	}
}

func (dl *DataLoader[K, V]) dispatchBatch(keys []K, listeners map[K][]chan dlResult[V]) {
	ctx := context.Background()

	resultMap, err := dl.safeCall(ctx, keys)

	var zero V
	for _, key := range keys {
		chs := listeners[key]
		for _, ch := range chs {
			if err != nil {
				ch <- dlResult[V]{val: zero, err: err}
			} else if val, ok := resultMap[key]; ok {
				ch <- dlResult[V]{val: val, err: nil}
			} else {
				ch <- dlResult[V]{val: zero, err: ErrKeyNotFound}
			}
		}
	}
}

func (dl *DataLoader[K, V]) safeCall(ctx context.Context, keys []K) (res map[K]V, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("batcher: dataloader function panicked: %v", r)
		}
	}()
	return dl.fn(ctx, keys)
}
