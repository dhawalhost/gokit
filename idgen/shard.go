package idgen

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

const (
	// Custom epoch: 2024-01-01 00:00:00 UTC in milliseconds.
	shardEpoch int64 = 1704067200000

	sequenceBits   = 10
	shardIDBits    = 13
	maxShardID     = (1 << shardIDBits) - 1  // 8191
	maxSequence    = (1 << sequenceBits) - 1 // 1023
	shardIDShift   = sequenceBits
	timestampShift = sequenceBits + shardIDBits
)

// ShardOption configures a ShardIDGenerator.
type ShardOption func(*ShardIDGenerator)

// WithMaxBackwardDrift sets the maximum backward clock skew the generator
// waits out before returning an error. Defaults to 0 (fail immediately on skew).
func WithMaxBackwardDrift(d time.Duration) ShardOption {
	return func(g *ShardIDGenerator) {
		if d > 0 {
			g.maxBackwardDriftMs = d.Milliseconds()
		}
	}
}

// ShardIDGenerator generates 64-bit time-sortable IDs that embed the logical shard ID.
// Bit layout:
// - 41 bits: timestamp in milliseconds since custom epoch (covers ~69 years).
// - 13 bits: logical shard ID (0 to 8191 logical shards).
// - 10 bits: monotonic sequence (0 to 1023 IDs per millisecond per shard).
type ShardIDGenerator struct {
	mu                 sync.Mutex
	shardID            uint16
	lastTime           int64
	sequence           int64
	maxBackwardDriftMs int64
}

// NewShardIDGenerator creates an ID generator bound to a specific logical shard ID (0 - 8191).
func NewShardIDGenerator(shardID uint16, opts ...ShardOption) (*ShardIDGenerator, error) {
	if shardID > maxShardID {
		return nil, fmt.Errorf("idgen: shard ID %d exceeds maximum %d", shardID, maxShardID)
	}
	g := &ShardIDGenerator{
		shardID: shardID,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(g)
		}
	}
	return g, nil
}

// NextID generates the next 64-bit shard-aware time-sorted ID.
func (g *ShardIDGenerator) NextID() (int64, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	now := time.Now().UnixMilli()
	if now < g.lastTime {
		drift := g.lastTime - now
		if g.maxBackwardDriftMs <= 0 || drift > g.maxBackwardDriftMs {
			return 0, errors.New("idgen: clock moved backwards")
		}
		deadline := time.Now().Add(time.Duration(g.maxBackwardDriftMs) * time.Millisecond)
		for now < g.lastTime {
			if time.Now().After(deadline) {
				return 0, errors.New("idgen: clock moved backwards")
			}
			time.Sleep(100 * time.Microsecond)
			now = time.Now().UnixMilli()
		}
	}

	if now == g.lastTime {
		g.sequence = (g.sequence + 1) & maxSequence
		if g.sequence == 0 {
			for now <= g.lastTime {
				time.Sleep(100 * time.Microsecond)
				now = time.Now().UnixMilli()
			}
		}
	} else {
		g.sequence = 0
	}

	g.lastTime = now

	id := ((now - shardEpoch) << timestampShift) |
		(int64(g.shardID) << shardIDShift) |
		g.sequence

	return id, nil
}

// ExtractShardID extracts the logical shard ID from a 64-bit ID in O(1) bitshift.
// This allows microservices to route queries directly to the correct shard without a directory lookup!
func ExtractShardID(id int64) uint16 {
	return uint16((id >> shardIDShift) & maxShardID)
}

// ExtractTime extracts the time.Time timestamp embedded in the ID.
func ExtractTime(id int64) time.Time {
	milli := (id >> timestampShift) + shardEpoch
	return time.UnixMilli(milli)
}
