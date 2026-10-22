package cache

import (
	"errors"
	"fmt"
	"hash/fnv"
	"sort"
	"strconv"
	"sync"
)

// ErrEmptyRing is returned when attempting to route on an empty hash ring.
var ErrEmptyRing = errors.New("consistenthash: empty hash ring")

// ConsistentHashRing implements consistent hashing with virtual nodes (vnodes)
// to evenly balance keys across nodes and minimize rebalancing churn.
type ConsistentHashRing struct {
	vnodes int
	ring   []uint32
	nodes  map[uint32]string
	mu     sync.RWMutex
}

// NewConsistentHashRing constructs a hash ring with the specified virtual nodes per physical node (e.g. 100).
func NewConsistentHashRing(vnodes int) *ConsistentHashRing {
	if vnodes <= 0 {
		vnodes = 100
	}
	return &ConsistentHashRing{
		vnodes: vnodes,
		nodes:  make(map[uint32]string),
	}
}

// Add adds one or more physical nodes to the hash ring.
func (c *ConsistentHashRing) Add(nodes ...string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, node := range nodes {
		for i := 0; i < c.vnodes; i++ {
			vnodeKey := node + "#" + strconv.Itoa(i)
			h := hashKey(vnodeKey)
			c.ring = append(c.ring, h)
			c.nodes[h] = node
		}
	}
	sort.Slice(c.ring, func(i, j int) bool { return c.ring[i] < c.ring[j] })
}

// Remove removes one or more physical nodes from the ring.
func (c *ConsistentHashRing) Remove(nodes ...string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	toRemove := make(map[string]bool)
	for _, n := range nodes {
		toRemove[n] = true
	}

	newRing := make([]uint32, 0, len(c.ring))
	for _, h := range c.ring {
		node := c.nodes[h]
		if toRemove[node] {
			delete(c.nodes, h)
		} else {
			newRing = append(newRing, h)
		}
	}
	c.ring = newRing
}

// Get finds the closest clockwise physical node for key.
func (c *ConsistentHashRing) Get(key string) (string, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if len(c.ring) == 0 {
		return "", ErrEmptyRing
	}

	h := hashKey(key)
	idx := sort.Search(len(c.ring), func(i int) bool { return c.ring[i] >= h })
	if idx == len(c.ring) {
		idx = 0
	}
	return c.nodes[c.ring[idx]], nil
}

// SaltKey distributes writes across N salted sub-keys to prevent hot partition bottlenecks.
func SaltKey(key string, numStripes int, stripeIndex int) string {
	if numStripes <= 1 {
		return key
	}
	return fmt.Sprintf("%s:salt:%d", key, stripeIndex%numStripes)
}

func hashKey(key string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return h.Sum32()
}
