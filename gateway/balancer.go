package gateway

import (
	"math/rand"
	"net/http/httputil"
	"net/url"
	"sync/atomic"
)

// Target represents a downstream backend service endpoint.
type Target struct {
	URL          *url.URL
	Alive        atomic.Bool
	ActiveConns  atomic.Int64
	ReverseProxy *httputil.ReverseProxy
}

// NewTarget creates a new Target for the given backend URL.
func NewTarget(u *url.URL) *Target {
	t := &Target{
		URL: u,
	}
	t.Alive.Store(true)
	return t
}

// IsAlive returns whether the target is considered healthy.
func (t *Target) IsAlive() bool {
	return t.Alive.Load()
}

// SetAlive updates the health status of the target.
func (t *Target) SetAlive(alive bool) {
	t.Alive.Store(alive)
}

// Balancer defines an algorithm for selecting an active backend target.
type Balancer interface {
	Next(targets []*Target) (*Target, error)
}

// RoundRobinBalancer selects healthy targets sequentially in circular order.
type RoundRobinBalancer struct {
	index atomic.Uint64
}

// NewRoundRobinBalancer creates a new RoundRobinBalancer.
func NewRoundRobinBalancer() *RoundRobinBalancer {
	return &RoundRobinBalancer{}
}

// Next selects the next healthy target using round-robin.
func (b *RoundRobinBalancer) Next(targets []*Target) (*Target, error) {
	if len(targets) == 0 {
		return nil, ErrNoHealthyTargets
	}

	healthy := filterHealthy(targets)
	if len(healthy) == 0 {
		return nil, ErrNoHealthyTargets
	}

	idx := b.index.Add(1) - 1
	return healthy[idx%uint64(len(healthy))], nil
}

// LeastConnBalancer selects the healthy target with the fewest active connections.
type LeastConnBalancer struct{}

// NewLeastConnBalancer creates a new LeastConnBalancer.
func NewLeastConnBalancer() *LeastConnBalancer {
	return &LeastConnBalancer{}
}

// Next selects the healthy target with the minimum active connections.
func (b *LeastConnBalancer) Next(targets []*Target) (*Target, error) {
	healthy := filterHealthy(targets)
	if len(healthy) == 0 {
		return nil, ErrNoHealthyTargets
	}

	best := healthy[0]
	minConns := best.ActiveConns.Load()

	for _, t := range healthy[1:] {
		conns := t.ActiveConns.Load()
		if conns < minConns {
			best = t
			minConns = conns
		}
	}

	return best, nil
}

// RandomBalancer selects a healthy target uniformly at random.
type RandomBalancer struct{}

// NewRandomBalancer creates a new RandomBalancer.
func NewRandomBalancer() *RandomBalancer {
	return &RandomBalancer{}
}

// Next selects a random healthy target.
func (b *RandomBalancer) Next(targets []*Target) (*Target, error) {
	healthy := filterHealthy(targets)
	if len(healthy) == 0 {
		return nil, ErrNoHealthyTargets
	}

	idx := rand.Intn(len(healthy)) //nolint:gosec // PRNG is sufficient for load balancing distribution
	return healthy[idx], nil
}

func filterHealthy(targets []*Target) []*Target {
	healthy := make([]*Target, 0, len(targets))
	for _, t := range targets {
		if t.IsAlive() {
			healthy = append(healthy, t)
		}
	}
	return healthy
}
