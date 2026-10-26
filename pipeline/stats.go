package pipeline

import (
	"sync"
	"sync/atomic"
	"time"
)

// StageStats encapsulates performance and operational metrics for an individual pipeline stage.
type StageStats struct {
	Name             string `json:"name"`
	ItemsIn          int64  `json:"items_in"`
	ItemsOut         int64  `json:"items_out"`
	Errors           int64  `json:"errors"`
	DeadLettered     int64  `json:"dead_lettered"`
	TotalDurationNs  int64  `json:"total_duration_ns"`
	AvgItemLatencyNs int64  `json:"avg_item_latency_ns"`
}

// Stats provides an aggregated snapshot of pipeline throughput and health.
type Stats struct {
	TotalIn       int64         `json:"total_in"`
	TotalOut      int64         `json:"total_out"`
	TotalErrors   int64         `json:"total_errors"`
	TotalDeadLet  int64         `json:"total_dead_lettered"`
	Stages        []StageStats  `json:"stages"`
	StartTime     time.Time     `json:"start_time"`
	TotalDuration time.Duration `json:"total_duration"`
}

// StageMetrics tracks atomic counts and timing for a stage during execution.
type StageMetrics struct {
	name            string
	itemsIn         int64
	itemsOut        int64
	errors          int64
	deadLettered    int64
	totalDurationNs int64
}

// NewStageMetrics constructs a StageMetrics tracker.
func NewStageMetrics(name string) *StageMetrics {
	return &StageMetrics{name: name}
}

// IncIn increments items received count.
func (m *StageMetrics) IncIn() {
	atomic.AddInt64(&m.itemsIn, 1)
}

// IncOut increments items emitted count.
func (m *StageMetrics) IncOut() {
	atomic.AddInt64(&m.itemsOut, 1)
}

// IncErrors increments error count.
func (m *StageMetrics) IncErrors() {
	atomic.AddInt64(&m.errors, 1)
}

// IncDeadLetter increments dead-lettered count.
func (m *StageMetrics) IncDeadLetter() {
	atomic.AddInt64(&m.deadLettered, 1)
}

// RecordDuration adds elapsed duration to total latency.
func (m *StageMetrics) RecordDuration(d time.Duration) {
	atomic.AddInt64(&m.totalDurationNs, d.Nanoseconds())
}

// Snapshot returns a copy of the current stage metrics.
func (m *StageMetrics) Snapshot() StageStats {
	in := atomic.LoadInt64(&m.itemsIn)
	out := atomic.LoadInt64(&m.itemsOut)
	errs := atomic.LoadInt64(&m.errors)
	dl := atomic.LoadInt64(&m.deadLettered)
	dur := atomic.LoadInt64(&m.totalDurationNs)

	var avgLatency int64
	if in > 0 {
		avgLatency = dur / in
	}

	return StageStats{
		Name:             m.name,
		ItemsIn:          in,
		ItemsOut:         out,
		Errors:           errs,
		DeadLettered:     dl,
		TotalDurationNs:  dur,
		AvgItemLatencyNs: avgLatency,
	}
}

// metricsCollector manages StageMetrics across the pipeline execution graph.
type metricsCollector struct {
	mu     sync.RWMutex
	stages []*StageMetrics
}

func newMetricsCollector() *metricsCollector {
	return &metricsCollector{
		stages: make([]*StageMetrics, 0),
	}
}

func (c *metricsCollector) register(name string) *StageMetrics {
	c.mu.Lock()
	defer c.mu.Unlock()
	m := NewStageMetrics(name)
	c.stages = append(c.stages, m)
	return m
}

func (c *metricsCollector) snapshot(startTime time.Time) Stats {
	c.mu.RLock()
	defer c.mu.RUnlock()

	stages := make([]StageStats, len(c.stages))
	var totIn, totOut, totErrs, totDl int64

	for i, sm := range c.stages {
		s := sm.Snapshot()
		stages[i] = s
		totIn += s.ItemsIn
		totOut += s.ItemsOut
		totErrs += s.Errors
		totDl += s.DeadLettered
	}

	now := time.Now().UTC()
	var dur time.Duration
	if !startTime.IsZero() {
		dur = now.Sub(startTime)
	}

	return Stats{
		TotalIn:       totIn,
		TotalOut:      totOut,
		TotalErrors:   totErrs,
		TotalDeadLet:  totDl,
		Stages:        stages,
		StartTime:     startTime,
		TotalDuration: dur,
	}
}
