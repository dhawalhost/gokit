// Package storage provides a cloud-agnostic, unified object storage abstraction
// supporting Amazon S3, Cloudflare R2, MinIO, Google Cloud Storage, local filesystem,
// and in-memory mock implementations.
package storage

import (
	"context"
	"errors"
	"io"
	"time"
)

var (
	// ErrNotFound is returned when the requested object key does not exist.
	ErrNotFound = errors.New("storage: object not found")

	// ErrInvalidKey is returned when the provided object key is empty or malformed.
	ErrInvalidKey = errors.New("storage: invalid object key")

	// ErrInvalidRange is returned when a requested byte range is invalid or out of bounds.
	ErrInvalidRange = errors.New("storage: invalid byte range")

	// ErrPresignExpired is returned when a presigned URL has expired.
	ErrPresignExpired = errors.New("storage: presigned url expired")

	// ErrPresignSignatureMismatch is returned when a presigned URL signature verification fails.
	ErrPresignSignatureMismatch = errors.New("storage: presigned url signature mismatch")
)

// ObjectInfo holds standard metadata describing an object stored in a bucket.
type ObjectInfo struct {
	// Key is the unique path/name of the object.
	Key string `json:"key"`

	// Size is the object payload size in bytes.
	Size int64 `json:"size"`

	// ContentType is the MIME type of the object (e.g. "image/png", "application/json").
	ContentType string `json:"content_type"`

	// ETag is an entity tag representing the content hash or version of the object.
	ETag string `json:"etag"`

	// LastModified is the timestamp when the object was last written.
	LastModified time.Time `json:"last_modified"`

	// Metadata contains optional user-defined custom metadata key-value pairs.
	Metadata map[string]string `json:"metadata,omitempty"`
}

// ListResult represents the outcome of a bucket listing query.
type ListResult struct {
	// Objects is the list of objects matching the prefix.
	Objects []ObjectInfo `json:"objects"`

	// CommonPrefixes contains directories/sub-prefixes when a delimiter is specified.
	CommonPrefixes []string `json:"common_prefixes,omitempty"`

	// NextContinuationToken is the token to retrieve the next page of results.
	NextContinuationToken string `json:"next_continuation_token,omitempty"`

	// IsTruncated indicates whether there are additional pages remaining.
	IsTruncated bool `json:"is_truncated"`
}

// Storage defines the uniform contract for object storage operations.
type Storage interface {
	// Put writes the payload from r to the specified key.
	Put(ctx context.Context, key string, r io.Reader, opts ...PutOption) (*ObjectInfo, error)

	// Get retrieves the object at key. The caller is responsible for closing the returned io.ReadCloser.
	Get(ctx context.Context, key string, opts ...GetOption) (io.ReadCloser, *ObjectInfo, error)

	// Delete removes the object at key. Returns nil if the object does not exist.
	Delete(ctx context.Context, key string) error

	// DeleteMulti removes multiple objects in a single batch.
	DeleteMulti(ctx context.Context, keys ...string) error

	// Exists checks whether an object exists at key.
	Exists(ctx context.Context, key string) (bool, error)

	// Stat returns the metadata for the object at key without reading its body.
	Stat(ctx context.Context, key string) (*ObjectInfo, error)

	// List searches objects matching prefix.
	List(ctx context.Context, prefix string, opts ...ListOption) (*ListResult, error)

	// PresignGet creates a temporary pre-authenticated URL for reading an object directly.
	PresignGet(ctx context.Context, key string, ttl time.Duration, opts ...PresignOption) (string, error)

	// PresignPut creates a temporary pre-authenticated URL for uploading an object directly.
	PresignPut(ctx context.Context, key string, ttl time.Duration, opts ...PresignOption) (string, error)
}

// PutOptions holds optional parameters for Put operations.
type PutOptions struct {
	ContentType   string
	ContentLength int64
	Metadata      map[string]string
}

// PutOption configures a PutOptions struct.
type PutOption func(*PutOptions)

// WithContentType sets the MIME type of the uploaded object.
func WithContentType(contentType string) PutOption {
	return func(o *PutOptions) {
		o.ContentType = contentType
	}
}

// WithContentLength specifies the expected payload size in bytes.
func WithContentLength(size int64) PutOption {
	return func(o *PutOptions) {
		o.ContentLength = size
	}
}

// WithMetadata sets user-defined custom metadata on the object.
func WithMetadata(meta map[string]string) PutOption {
	return func(o *PutOptions) {
		o.Metadata = meta
	}
}

// GetOptions holds optional parameters for Get operations.
type GetOptions struct {
	RangeStart *int64
	RangeEnd   *int64
}

// GetOption configures a GetOptions struct.
type GetOption func(*GetOptions)

// WithRange requests a specific byte range [start, end] inclusive.
func WithRange(start, end int64) GetOption {
	return func(o *GetOptions) {
		o.RangeStart = &start
		o.RangeEnd = &end
	}
}

// ListOptions holds optional parameters for List operations.
type ListOptions struct {
	MaxKeys           int
	ContinuationToken string
	Delimiter         string
}

// ListOption configures a ListOptions struct.
type ListOption func(*ListOptions)

// WithMaxKeys limits the number of object keys returned per page.
func WithMaxKeys(maxKeys int) ListOption {
	return func(o *ListOptions) {
		o.MaxKeys = maxKeys
	}
}

// WithContinuationToken sets the pagination token to retrieve subsequent results.
func WithContinuationToken(token string) ListOption {
	return func(o *ListOptions) {
		o.ContinuationToken = token
	}
}

// WithDelimiter sets the delimiter character (typically "/") to group keys into CommonPrefixes.
func WithDelimiter(delimiter string) ListOption {
	return func(o *ListOptions) {
		o.Delimiter = delimiter
	}
}

// PresignOptions holds optional parameters for presigned URL generation.
type PresignOptions struct {
	ContentType string
}

// PresignOption configures a PresignOptions struct.
type PresignOption func(*PresignOptions)

// WithPresignContentType sets the expected Content-Type header on presigned upload requests.
func WithPresignContentType(contentType string) PresignOption {
	return func(o *PresignOptions) {
		o.ContentType = contentType
	}
}
