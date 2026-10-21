package outbox

import (
	"time"
)

// Status represents the lifecycle status of an outbox message.
type Status string

const (
	// StatusPending indicates the message is awaiting delivery.
	StatusPending Status = "pending"
	// StatusPublished indicates the message was successfully dispatched to the broker.
	StatusPublished Status = "published"
	// StatusFailed indicates delivery exceeded max retries and stopped.
	StatusFailed Status = "failed"
)

// Message is an outbox record stored transactionally with domain data.
type Message struct {
	ID          string            `gorm:"primaryKey;size:64" json:"id"`
	Topic       string            `gorm:"size:255;index:idx_outbox_scheduled;not null" json:"topic"`
	Key         string            `gorm:"size:255;index" json:"key,omitempty"`
	Payload     []byte            `gorm:"type:bytea;not null" json:"payload"`
	Headers     map[string]string `gorm:"serializer:json" json:"headers,omitempty"`
	Status      Status            `gorm:"size:32;index:idx_outbox_scheduled;default:'pending';not null" json:"status"`
	RetryCount  int               `gorm:"default:0;not null" json:"retry_count"`
	MaxRetries  int               `gorm:"default:5;not null" json:"max_retries"`
	LastError   string            `gorm:"type:text" json:"last_error,omitempty"`
	CreatedAt   time.Time         `gorm:"autoCreateTime;index" json:"created_at"`
	ScheduledAt time.Time         `gorm:"index:idx_outbox_scheduled;not null" json:"scheduled_at"`
	PublishedAt *time.Time        `json:"published_at,omitempty"`
}

// TableName returns the default PostgreSQL table name for outbox messages.
func (Message) TableName() string {
	return "outbox_messages"
}
