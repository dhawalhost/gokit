package pipeline

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// Record encapsulates a stream payload alongside metadata, timestamp, correlation identifiers, and optional processing error.
type Record[T any] struct {
	ID        string            `json:"id"`
	Value     T                 `json:"value"`
	Timestamp time.Time         `json:"timestamp"`
	Headers   map[string]string `json:"headers,omitempty"`
	Error     error             `json:"error,omitempty"`
}

// NewRecord constructs a new Record with the provided value, a generated unique identifier, and the current UTC timestamp.
func NewRecord[T any](value T) Record[T] {
	return Record[T]{
		ID:        generateRecordID(),
		Value:     value,
		Timestamp: time.Now().UTC(),
		Headers:   make(map[string]string),
	}
}

// WithID returns a shallow copy of the record with a custom ID.
func (r Record[T]) WithID(id string) Record[T] {
	r.ID = id
	return r
}

// WithHeader returns a copy of the record with the given key-value header appended or overwritten.
func (r Record[T]) WithHeader(key, val string) Record[T] {
	newHeaders := make(map[string]string, len(r.Headers)+1)
	for k, v := range r.Headers {
		newHeaders[k] = v
	}
	newHeaders[key] = val
	r.Headers = newHeaders
	return r
}

// WithError returns a copy of the record marked with the provided processing error.
func (r Record[T]) WithError(err error) Record[T] {
	r.Error = err
	return r
}

// Clone returns an independent deep copy of the record's metadata headers.
func (r Record[T]) Clone() Record[T] {
	var clonedHeaders map[string]string
	if r.Headers != nil {
		clonedHeaders = make(map[string]string, len(r.Headers))
		for k, v := range r.Headers {
			clonedHeaders[k] = v
		}
	}
	return Record[T]{
		ID:        r.ID,
		Value:     r.Value,
		Timestamp: r.Timestamp,
		Headers:   clonedHeaders,
		Error:     r.Error,
	}
}

func generateRecordID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return hex.EncodeToString([]byte(time.Now().UTC().Format(time.RFC3339Nano)))
	}
	return hex.EncodeToString(b)
}
