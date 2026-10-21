package outbox

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/dhawalhost/gokit/idgen"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrNotFound is returned when an outbox message ID is not found.
var ErrNotFound = errors.New("outbox: message not found")

// Store defines persistence operations for transactional outbox records.
type Store interface {
	Save(ctx context.Context, msg *Message) error
	FetchPending(ctx context.Context, limit int) ([]*Message, error)
	MarkPublished(ctx context.Context, id string) error
	MarkFailed(ctx context.Context, id string, err error, nextRetry time.Time, isFinal bool) error
	Cleanup(ctx context.Context, olderThan time.Time) (int64, error)
}

// Enqueue inserts a new pending message into the outbox within an existing GORM transaction or session.
func Enqueue(ctx context.Context, tx *gorm.DB, topic, key string, payload []byte, headers map[string]string) (*Message, error) {
	now := time.Now().UTC()
	msg := &Message{
		ID:          idgen.NewUUIDv7(),
		Topic:       topic,
		Key:         key,
		Payload:     payload,
		Headers:     headers,
		Status:      StatusPending,
		RetryCount:  0,
		MaxRetries:  5,
		CreatedAt:   now,
		ScheduledAt: now,
	}

	if err := tx.WithContext(ctx).Create(msg).Error; err != nil {
		return nil, err
	}
	return msg, nil
}

// GORMStore implements Store using a PostgreSQL database managed by GORM.
type GORMStore struct {
	db *gorm.DB
}

// NewGORMStore constructs a GORMStore wrapping the given *gorm.DB.
func NewGORMStore(db *gorm.DB) *GORMStore {
	return &GORMStore{db: db}
}

// AutoMigrate ensures the outbox table and indexes exist.
func (s *GORMStore) AutoMigrate() error {
	return s.db.AutoMigrate(&Message{})
}

// Save inserts a new message into the outbox table.
func (s *GORMStore) Save(ctx context.Context, msg *Message) error {
	if msg.ID == "" {
		msg.ID = idgen.NewUUIDv7()
	}
	if msg.CreatedAt.IsZero() {
		msg.CreatedAt = time.Now().UTC()
	}
	if msg.ScheduledAt.IsZero() {
		msg.ScheduledAt = msg.CreatedAt
	}
	if msg.Status == "" {
		msg.Status = StatusPending
	}
	if msg.MaxRetries == 0 {
		msg.MaxRetries = 5
	}
	return s.db.WithContext(ctx).Create(msg).Error
}

// FetchPending retrieves pending messages eligible for dispatch, using SKIP LOCKED to prevent concurrency conflicts.
func (s *GORMStore) FetchPending(ctx context.Context, limit int) ([]*Message, error) {
	var messages []*Message
	now := time.Now().UTC()
	err := s.db.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
		Where("status = ? AND scheduled_at <= ?", StatusPending, now).
		Order("scheduled_at ASC").
		Limit(limit).
		Find(&messages).Error
	if err != nil {
		return nil, err
	}
	return messages, nil
}

// MarkPublished marks the message as successfully delivered.
func (s *GORMStore) MarkPublished(ctx context.Context, id string) error {
	now := time.Now().UTC()
	return s.db.WithContext(ctx).Model(&Message{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"status":       StatusPublished,
			"published_at": &now,
			"last_error":   "",
		}).Error
}

// MarkFailed records a failed attempt, increments retry count, and sets next retry time.
func (s *GORMStore) MarkFailed(ctx context.Context, id string, err error, nextRetry time.Time, isFinal bool) error {
	updates := map[string]any{
		"retry_count":  gorm.Expr("retry_count + ?", 1),
		"last_error":   err.Error(),
		"scheduled_at": nextRetry,
	}
	if isFinal {
		updates["status"] = StatusFailed
	}
	return s.db.WithContext(ctx).Model(&Message{}).
		Where("id = ?", id).
		Updates(updates).Error
}

// Cleanup permanently deletes published messages older than the given cutoff time.
func (s *GORMStore) Cleanup(ctx context.Context, olderThan time.Time) (int64, error) {
	res := s.db.WithContext(ctx).
		Where("status = ? AND published_at IS NOT NULL AND published_at < ?", StatusPublished, olderThan).
		Delete(&Message{})
	return res.RowsAffected, res.Error
}

// MemoryStore is an in-memory Store implementation for unit tests and local dev.
type MemoryStore struct {
	mu       sync.RWMutex
	messages map[string]*Message
}

// NewMemoryStore constructs a new in-memory outbox store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		messages: make(map[string]*Message),
	}
}

// Save stores the message in memory.
func (m *MemoryStore) Save(_ context.Context, msg *Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if msg.ID == "" {
		msg.ID = idgen.NewUUIDv7()
	}
	if msg.CreatedAt.IsZero() {
		msg.CreatedAt = time.Now().UTC()
	}
	if msg.ScheduledAt.IsZero() {
		msg.ScheduledAt = msg.CreatedAt
	}
	if msg.Status == "" {
		msg.Status = StatusPending
	}
	if msg.MaxRetries == 0 {
		msg.MaxRetries = 5
	}

	clone := *msg
	m.messages[msg.ID] = &clone
	return nil
}

// FetchPending retrieves up to limit pending messages whose ScheduledAt has arrived.
func (m *MemoryStore) FetchPending(_ context.Context, limit int) ([]*Message, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	now := time.Now().UTC()
	var candidates []*Message
	for _, msg := range m.messages {
		if msg.Status == StatusPending && !msg.ScheduledAt.After(now) {
			clone := *msg
			candidates = append(candidates, &clone)
		}
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].ScheduledAt.Before(candidates[j].ScheduledAt)
	})

	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	return candidates, nil
}

// MarkPublished marks the message published in memory.
func (m *MemoryStore) MarkPublished(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	msg, exists := m.messages[id]
	if !exists {
		return ErrNotFound
	}
	now := time.Now().UTC()
	msg.Status = StatusPublished
	msg.PublishedAt = &now
	msg.LastError = ""
	return nil
}

// MarkFailed updates retry details and next scheduled time in memory.
func (m *MemoryStore) MarkFailed(_ context.Context, id string, err error, nextRetry time.Time, isFinal bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	msg, exists := m.messages[id]
	if !exists {
		return ErrNotFound
	}
	msg.RetryCount++
	msg.LastError = err.Error()
	msg.ScheduledAt = nextRetry
	if isFinal {
		msg.Status = StatusFailed
	}
	return nil
}

// Cleanup removes published messages older than olderThan from memory.
func (m *MemoryStore) Cleanup(_ context.Context, olderThan time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var deleted int64
	for id, msg := range m.messages {
		if msg.Status == StatusPublished && msg.PublishedAt != nil && msg.PublishedAt.Before(olderThan) {
			delete(m.messages, id)
			deleted++
		}
	}
	return deleted, nil
}

// All returns a slice of all stored messages (for assertions).
func (m *MemoryStore) All() []*Message {
	m.mu.RLock()
	defer m.mu.RUnlock()

	res := make([]*Message, 0, len(m.messages))
	for _, msg := range m.messages {
		clone := *msg
		res = append(res, &clone)
	}
	return res
}
