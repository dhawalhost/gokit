package idgen

// SetLastTimeForTest overrides lastTime to test clock anomalies.
func (g *ShardIDGenerator) SetLastTimeForTest(t int64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.lastTime = t
}

// SetSequenceForTest sets the sequence counter to test rollover behavior.
func (g *ShardIDGenerator) SetSequenceForTest(seq int64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.sequence = seq
}
