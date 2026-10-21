package database

import (
	"context"
	"fmt"
	"sync/atomic"

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
