package audit

import (
	"strings"
	"time"
)

// Status represents the outcome of an audited action.
type Status string

const (
	// StatusSuccess indicates the action completed successfully.
	StatusSuccess Status = "success"
	// StatusFailure indicates the action failed due to an error.
	StatusFailure Status = "failure"
	// StatusDenied indicates the action was blocked or unauthorized.
	StatusDenied Status = "denied"
)

// Actor represents the entity performing the action.
type Actor struct {
	ID    string `json:"id"`
	Type  string `json:"type"` // e.g. "user", "service", "system", "api_key"
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
	Role  string `json:"role,omitempty"`
}

// Resource represents the target entity acted upon.
type Resource struct {
	Type string `json:"type"` // e.g. "user", "workspace", "billing_profile"
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

// FieldChange captures a state mutation on a specific attribute.
type FieldChange struct {
	Field    string `json:"field"`
	OldValue any    `json:"old_value,omitempty"`
	NewValue any    `json:"new_value,omitempty"`
}

// Event represents a compliance-ready, immutable audit log entry.
type Event struct {
	ID        string         `json:"id"`
	Timestamp time.Time      `json:"timestamp"`
	TenantID  string         `json:"tenant_id,omitempty"`
	Actor     Actor          `json:"actor"`
	Action    string         `json:"action"` // e.g. "user.create", "api_key.revoke"
	Resource  Resource       `json:"resource"`
	Status    Status         `json:"status"`
	Reason    string         `json:"reason,omitempty"`
	Changes   []FieldChange  `json:"changes,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

const maskedPlaceholder = "********"

// isSensitiveKey checks if a key likely contains sensitive/PII data.
func isSensitiveKey(k string) bool {
	key := strings.ToLower(k)
	return strings.Contains(key, "password") ||
		strings.Contains(key, "secret") ||
		strings.Contains(key, "token") ||
		strings.Contains(key, "authorization") ||
		strings.Contains(key, "cookie") ||
		strings.Contains(key, "api_key") ||
		strings.Contains(key, "apikey") ||
		strings.Contains(key, "ssn") ||
		strings.Contains(key, "credit_card") ||
		strings.Contains(key, "card_number") ||
		strings.Contains(key, "cvv") ||
		strings.Contains(key, "dsn") ||
		strings.Contains(key, "private_key")
}

// Sanitize creates a shallow clone of the event with sensitive fields masked.
func (e *Event) Sanitize() *Event {
	if e == nil {
		return nil
	}
	clone := *e

	if len(e.Changes) > 0 {
		clone.Changes = make([]FieldChange, len(e.Changes))
		for i, c := range e.Changes {
			if isSensitiveKey(c.Field) {
				clone.Changes[i] = FieldChange{
					Field:    c.Field,
					OldValue: maskedPlaceholder,
					NewValue: maskedPlaceholder,
				}
			} else {
				clone.Changes[i] = c
			}
		}
	}

	if len(e.Metadata) > 0 {
		clone.Metadata = make(map[string]any, len(e.Metadata))
		for k, v := range e.Metadata {
			if isSensitiveKey(k) {
				clone.Metadata[k] = maskedPlaceholder
			} else {
				clone.Metadata[k] = v
			}
		}
	}

	return &clone
}
