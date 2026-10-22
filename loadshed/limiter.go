package loadshed

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// ErrLoadShed is returned when an incoming request is rejected to protect system stability.
var ErrLoadShed = errors.New("loadshed: system overloaded, request shed")

// Priority represents the urgency or criticality of an inbound request.
type Priority int

const (
	// PriorityLow is for non-critical, background, batch, or analytics workloads.
	// Shed first during incipient queue buildup.
	PriorityLow Priority = iota
	// PriorityStandard is for normal end-user interactive requests.
	PriorityStandard
	// PriorityCritical is for high-priority traffic (health probes, auth, billing, webhooks).
	// Admitted with reserved headroom even when system is shedding standard traffic.
	PriorityCritical
)

// DoneFunc must be invoked when the request finishes executing.
// It reports whether the operation succeeded or failed and updates the adaptive limit.
type DoneFunc func(err error)

// Stats holds a point-in-time snapshot of the limiter's performance and concurrency metrics.
type Stats struct {
	CurrentLimit  float64       `json:"current_limit"`
	InFlight      int64         `json:"in_flight"`
	MinRTT        time.Duration `json:"min_rtt"`
	SampleRTT     time.Duration `json:"sample_rtt"`
	TotalAdmitted uint64        `json:"total_admitted"`
	TotalShed     uint64        `json:"total_shed"`
}

// Config defines the tuning parameters for the adaptive concurrency limiter.
type Config struct {
	// InitialLimit is the starting concurrency limit (default: 20).
	InitialLimit float64
	// MinLimit is the absolute minimum concurrency floor (default: 5).
	MinLimit float64
	// MaxLimit is the maximum concurrency ceiling (default: 1000).
	MaxLimit float64
	// Smoothing is the EWMA weight applied to rolling RTT samples [0.0 - 1.0] (default: 0.2).
	Smoothing float64
	// MinRTTWindow is how long the baseline minimum RTT is remembered before refreshing (default: 5m).
	MinRTTWindow time.Duration
	// LowPriorityThreshold is the gradient threshold below which PriorityLow is shed (default: 0.95).
	LowPriorityThreshold float64
	// CriticalHeadroom is the extra fraction of capacity reserved for PriorityCritical (default: 0.20 = +20%).
	CriticalHeadroom float64
	// ProbeRatio is the additive increase factor when system is healthy and latency is flat (default: 1.0).
	ProbeRatio float64
	// QueueTolerance is beta cushion added to limit during gradient decreases (default: 2.0).
	QueueTolerance float64
}

// DefaultConfig returns production-ready defaults inspired by TCP Vegas and Netflix concurrency-limits.
func DefaultConfig() Config {
	return Config{
		InitialLimit:         20.0,
		MinLimit:             5.0,
		MaxLimit:             1000.0,
		Smoothing:            0.2,
		MinRTTWindow:         5 * time.Minute,
		LowPriorityThreshold: 0.95,
		CriticalHeadroom:     0.20,
		ProbeRatio:           1.0,
		QueueTolerance:       2.0,
	}
}

// Limiter manages adaptive concurrency limits based on observed round-trip latencies.
type Limiter struct {
	cfg Config

	mu          sync.Mutex
	inFlight    int64
	limit       float64
	minRTT      time.Duration
	minRTTTime  time.Time
	sampleRTT   time.Duration
	initialized bool

	totalAdmitted atomic.Uint64
	totalShed     atomic.Uint64
}

// NewLimiter constructs an adaptive latency-based concurrency limiter.
func NewLimiter(opts ...Option) *Limiter {
	cfg := DefaultConfig()
	for _, opt := range opts {
		opt(&cfg)
	}

	if cfg.InitialLimit < cfg.MinLimit {
		cfg.MinLimit = cfg.InitialLimit
	}
	if cfg.InitialLimit > cfg.MaxLimit {
		cfg.MaxLimit = cfg.InitialLimit
	}

	return &Limiter{
		cfg:   cfg,
		limit: cfg.InitialLimit,
	}
}

// Option configures Limiter parameters.
type Option func(*Config)

// WithInitialLimit sets the starting concurrency limit.
func WithInitialLimit(limit float64) Option {
	return func(c *Config) {
		if limit > 0 {
			c.InitialLimit = limit
		}
	}
}

// WithMinLimit sets the minimum concurrency floor.
func WithMinLimit(min float64) Option {
	return func(c *Config) {
		if min > 0 {
			c.MinLimit = min
		}
	}
}

// WithMaxLimit sets the maximum concurrency ceiling.
func WithMaxLimit(max float64) Option {
	return func(c *Config) {
		if max > 0 {
			c.MaxLimit = max
		}
	}
}

// WithSmoothing sets the Exponential Weighted Moving Average (EWMA) factor.
func WithSmoothing(s float64) Option {
	return func(c *Config) {
		if s > 0 && s <= 1.0 {
			c.Smoothing = s
		}
	}
}

// WithMinRTTWindow sets how long baseline minRTT is retained before refreshing.
func WithMinRTTWindow(d time.Duration) Option {
	return func(c *Config) {
		if d > 0 {
			c.MinRTTWindow = d
		}
	}
}

// WithCriticalHeadroom sets the percentage buffer reserved for critical traffic.
func WithCriticalHeadroom(hr float64) Option {
	return func(c *Config) {
		if hr >= 0 {
			c.CriticalHeadroom = hr
		}
	}
}

// Acquire evaluates whether a request of the specified priority can be admitted.
// If admitted, it returns a DoneFunc that MUST be called when the operation finishes.
// If the system is overloaded, it returns ErrLoadShed.
func (l *Limiter) Acquire(ctx context.Context, priority Priority) (DoneFunc, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	l.mu.Lock()

	// Calculate gradient g = minRTT / sampleRTT
	gradient := 1.0
	if l.sampleRTT > 0 && l.minRTT > 0 {
		gradient = float64(l.minRTT) / float64(l.sampleRTT)
	}

	// Early drop for Low priority if latency is inflating
	if priority == PriorityLow && gradient < l.cfg.LowPriorityThreshold {
		l.mu.Unlock()
		l.totalShed.Add(1)
		return nil, ErrLoadShed
	}

	// Determine effective limit for the requested priority
	effectiveLimit := l.limit
	switch priority {
	case PriorityCritical:
		effectiveLimit = l.limit * (1.0 + l.cfg.CriticalHeadroom)
	case PriorityLow:
		effectiveLimit = l.limit * 0.8
	case PriorityStandard:
		effectiveLimit = l.limit
	}

	if float64(l.inFlight) >= effectiveLimit {
		l.mu.Unlock()
		l.totalShed.Add(1)
		return nil, ErrLoadShed
	}

	l.inFlight++
	l.totalAdmitted.Add(1)
	start := time.Now()
	l.mu.Unlock()

	var once sync.Once
	done := func(err error) {
		once.Do(func() {
			l.release(start, err)
		})
	}

	return done, nil
}

func (l *Limiter) release(start time.Time, err error) {
	rtt := time.Since(start)
	if rtt <= 0 {
		rtt = time.Microsecond
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.inFlight > 0 {
		l.inFlight--
	}

	now := time.Now()

	// Update baseline minRTT
	if !l.initialized || l.minRTT == 0 || rtt < l.minRTT || now.Sub(l.minRTTTime) > l.cfg.MinRTTWindow {
		l.minRTT = rtt
		l.minRTTTime = now
		l.initialized = true
	}

	// Update smoothed sampleRTT via EWMA
	if l.sampleRTT == 0 {
		l.sampleRTT = rtt
	} else {
		l.sampleRTT = time.Duration((1.0-l.cfg.Smoothing)*float64(l.sampleRTT) + l.cfg.Smoothing*float64(rtt))
	}

	// Compute Vegas gradient: g = minRTT / sampleRTT
	gradient := float64(l.minRTT) / float64(l.sampleRTT)

	// If the request errored due to server timeout or failure, apply backoff
	if err != nil && !errors.Is(err, context.Canceled) {
		gradient = gradient * 0.85
	}

	// Adjust concurrency limit
	if gradient >= 1.0 {
		// Healthy: Additive increase
		l.limit = l.limit + l.cfg.ProbeRatio
	} else {
		// Queuing detected: Multiplicative decrease with tolerance cushion
		l.limit = l.limit*gradient + l.cfg.QueueTolerance
	}

	// Clamp within bounds [MinLimit, MaxLimit]
	if l.limit < l.cfg.MinLimit {
		l.limit = l.cfg.MinLimit
	} else if l.limit > l.cfg.MaxLimit {
		l.limit = l.cfg.MaxLimit
	}
}

// Stats returns a snapshot of the current limiter state.
func (l *Limiter) Stats() Stats {
	l.mu.Lock()
	defer l.mu.Unlock()

	return Stats{
		CurrentLimit:  l.limit,
		InFlight:      l.inFlight,
		MinRTT:        l.minRTT,
		SampleRTT:     l.sampleRTT,
		TotalAdmitted: l.totalAdmitted.Load(),
		TotalShed:     l.totalShed.Load(),
	}
}
