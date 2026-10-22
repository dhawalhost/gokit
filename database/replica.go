package database

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"gorm.io/gorm"
)

type primaryCtxKey struct{}

// WithPrimary forces all subsequent database operations within the context
// to route to the Primary database instance (bypassing read replicas).
// Use this to guarantee immediate read-after-write consistency.
func WithPrimary(ctx context.Context) context.Context {
	return context.WithValue(ctx, primaryCtxKey{}, true)
}

// IsPrimaryForced reports whether the context explicitly forces queries to the primary database.
func IsPrimaryForced(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	forced, ok := ctx.Value(primaryCtxKey{}).(bool)
	return ok && forced
}

// ReplicaRouter manages primary (write) and replica (read) connection pools with round-robin load balancing.
type ReplicaRouter struct {
	primary  *DB
	replicas []*DB
	counter  atomic.Uint64
}

// NewReplicaRouter creates a router that distributes read queries across replicas and routes writes to primary.
func NewReplicaRouter(primary *DB, replicas ...*DB) (*ReplicaRouter, error) {
	if primary == nil {
		return nil, fmt.Errorf("database: primary database is required")
	}

	return &ReplicaRouter{
		primary:  primary,
		replicas: replicas,
	}, nil
}

// Write returns the primary database connection pool for mutation operations (INSERT, UPDATE, DELETE).
func (r *ReplicaRouter) Write() *DB {
	return r.primary
}

// Read returns an appropriate database connection pool for SELECT queries.
// If WithPrimary(ctx) is set or no replicas are available, it returns the primary pool.
// Otherwise, it round-robins across the available read replicas.
func (r *ReplicaRouter) Read(ctx context.Context) *DB {
	if IsPrimaryForced(ctx) || len(r.replicas) == 0 {
		return r.primary
	}

	idx := r.counter.Add(1) - 1
	return r.replicas[idx%uint64(len(r.replicas))]
}

// GORM returns a context-bound GORM handle routed to a read replica or primary based on context.
func (r *ReplicaRouter) GORM(ctx context.Context) *gorm.DB {
	return r.Read(ctx).GORM.WithContext(ctx)
}

// WriteGORM returns a context-bound GORM handle directly on the primary database.
func (r *ReplicaRouter) WriteGORM(ctx context.Context) *gorm.DB {
	return r.primary.GORM.WithContext(ctx)
}

// HealthCheck verifies connectivity to both primary and all read replicas.
func (r *ReplicaRouter) HealthCheck(ctx context.Context) error {
	if err := r.primary.Ping(ctx); err != nil {
		return fmt.Errorf("database: primary health check failed: %w", err)
	}

	for i, replica := range r.replicas {
		if err := replica.Ping(ctx); err != nil {
			return fmt.Errorf("database: replica %d health check failed: %w", i, err)
		}
	}
	return nil
}

// Close gracefully closes the primary and all replica connection pools.
func (r *ReplicaRouter) Close() error {
	var firstErr error
	if err := r.primary.Close(); err != nil && firstErr == nil {
		firstErr = err
	}

	for _, replica := range r.replicas {
		if err := replica.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// ReadYourWritesTracker tracks recent write operations per client/session
// to ensure subsequent reads within the replication lag window route to Primary.
type ReadYourWritesTracker struct {
	mu     sync.RWMutex
	pinned map[string]time.Time
}

// NewReadYourWritesTracker creates a tracker for causal read-your-writes consistency.
func NewReadYourWritesTracker() *ReadYourWritesTracker {
	return &ReadYourWritesTracker{
		pinned: make(map[string]time.Time),
	}
}

// Pin marks a client key (e.g. user ID or session ID) to route all queries to Primary for window duration.
func (t *ReadYourWritesTracker) Pin(key string, window time.Duration) {
	if key == "" {
		return
	}
	t.mu.Lock()
	t.pinned[key] = time.Now().Add(window)
	t.mu.Unlock()
}

// IsPinned reports whether the client key is currently within a pinned primary routing window.
func (t *ReadYourWritesTracker) IsPinned(key string) bool {
	if key == "" {
		return false
	}
	t.mu.RLock()
	expiry, exists := t.pinned[key]
	t.mu.RUnlock()

	if !exists {
		return false
	}
	if time.Now().Before(expiry) {
		return true
	}

	t.mu.Lock()
	delete(t.pinned, key)
	t.mu.Unlock()
	return false
}

// ReadYourWritesMiddleware returns an HTTP middleware that automatically enforces
// read-your-writes consistency by routing subsequent reads to the primary database
// within window after any mutating HTTP request (POST, PUT, PATCH, DELETE).
func ReadYourWritesMiddleware(tracker *ReadYourWritesTracker, keyExtractor func(r *http.Request) string, window time.Duration) func(http.Handler) http.Handler {
	if window <= 0 {
		window = 5 * time.Second
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := ""
			if keyExtractor != nil {
				key = keyExtractor(r)
			}

			// Check if client is pinned or explicitly requested primary via header
			if (key != "" && tracker.IsPinned(key)) || r.Header.Get("X-Force-Primary") == "true" {
				r = r.WithContext(WithPrimary(r.Context()))
			}

			isMutation := r.Method == http.MethodPost || r.Method == http.MethodPut ||
				r.Method == http.MethodPatch || r.Method == http.MethodDelete

			next.ServeHTTP(w, r)

			if isMutation && key != "" {
				tracker.Pin(key, window)
			}
		})
	}
}
