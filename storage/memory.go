package storage

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type memObject struct {
	data []byte
	info ObjectInfo
}

// MemoryStorage provides an in-memory, thread-safe implementation of Storage.
// It is ideal for local testing, rapid prototyping, and zero-dependency unit tests.
type MemoryStorage struct {
	mu      sync.RWMutex
	objects map[string]memObject
	secret  []byte
	baseURL string
}

// NewMemory creates a new in-memory Storage instance.
func NewMemory() *MemoryStorage {
	return &MemoryStorage{
		objects: make(map[string]memObject),
		secret:  []byte("gokit-memory-storage-presign-secret"),
		baseURL: "http://storage.local",
	}
}

// Put writes object data to memory.
func (m *MemoryStorage) Put(_ context.Context, key string, r io.Reader, opts ...PutOption) (*ObjectInfo, error) {
	if strings.TrimSpace(key) == "" {
		return nil, ErrInvalidKey
	}

	var options PutOptions
	for _, opt := range opts {
		opt(&options)
	}

	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("storage: read payload: %w", err)
	}

	contentType := options.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	sum := sha256.Sum256(data)
	etag := hex.EncodeToString(sum[:])

	metaCopy := make(map[string]string)
	for k, v := range options.Metadata {
		metaCopy[k] = v
	}

	info := ObjectInfo{
		Key:          key,
		Size:         int64(len(data)),
		ContentType:  contentType,
		ETag:         etag,
		LastModified: time.Now().UTC(),
		Metadata:     metaCopy,
	}

	m.mu.Lock()
	m.objects[key] = memObject{
		data: data,
		info: info,
	}
	m.mu.Unlock()

	return &info, nil
}

// Get retrieves an object from memory.
func (m *MemoryStorage) Get(_ context.Context, key string, opts ...GetOption) (io.ReadCloser, *ObjectInfo, error) {
	if strings.TrimSpace(key) == "" {
		return nil, nil, ErrInvalidKey
	}

	var options GetOptions
	for _, opt := range opts {
		opt(&options)
	}

	m.mu.RLock()
	obj, exists := m.objects[key]
	m.mu.RUnlock()

	if !exists {
		return nil, nil, ErrNotFound
	}

	data := obj.data
	info := obj.info

	// Handle byte range slicing if specified
	if options.RangeStart != nil && options.RangeEnd != nil {
		start := *options.RangeStart
		end := *options.RangeEnd
		if start < 0 || end < start || start >= int64(len(data)) {
			return nil, nil, ErrInvalidRange
		}
		if end >= int64(len(data)) {
			end = int64(len(data)) - 1
		}
		data = data[start : end+1]
		info.Size = int64(len(data))
	}

	return io.NopCloser(bytes.NewReader(data)), &info, nil
}

// Delete removes an object from memory.
func (m *MemoryStorage) Delete(_ context.Context, key string) error {
	if strings.TrimSpace(key) == "" {
		return ErrInvalidKey
	}

	m.mu.Lock()
	delete(m.objects, key)
	m.mu.Unlock()

	return nil
}

// DeleteMulti removes multiple objects in a single batch.
func (m *MemoryStorage) DeleteMulti(_ context.Context, keys ...string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, k := range keys {
		delete(m.objects, k)
	}
	return nil
}

// Exists checks if an object is present in memory.
func (m *MemoryStorage) Exists(_ context.Context, key string) (bool, error) {
	if strings.TrimSpace(key) == "" {
		return false, ErrInvalidKey
	}

	m.mu.RLock()
	_, exists := m.objects[key]
	m.mu.RUnlock()

	return exists, nil
}

// Stat retrieves object metadata without reading the body.
func (m *MemoryStorage) Stat(_ context.Context, key string) (*ObjectInfo, error) {
	if strings.TrimSpace(key) == "" {
		return nil, ErrInvalidKey
	}

	m.mu.RLock()
	obj, exists := m.objects[key]
	m.mu.RUnlock()

	if !exists {
		return nil, ErrNotFound
	}

	info := obj.info
	return &info, nil
}

// List enumerates objects matching prefix with optional pagination and delimiter.
func (m *MemoryStorage) List(_ context.Context, prefix string, opts ...ListOption) (*ListResult, error) {
	var options ListOptions
	for _, opt := range opts {
		opt(&options)
	}

	if options.MaxKeys <= 0 {
		options.MaxKeys = 1000
	}

	m.mu.RLock()
	allKeys := make([]string, 0, len(m.objects))
	for k := range m.objects {
		if strings.HasPrefix(k, prefix) {
			allKeys = append(allKeys, k)
		}
	}
	sort.Strings(allKeys)

	// Seek to continuation token if provided
	startIndex := 0
	if options.ContinuationToken != "" {
		for i, k := range allKeys {
			if k > options.ContinuationToken {
				startIndex = i
				break
			}
			if i == len(allKeys)-1 {
				startIndex = len(allKeys)
			}
		}
	}

	res := &ListResult{
		Objects:        make([]ObjectInfo, 0),
		CommonPrefixes: make([]string, 0),
	}

	prefixSet := make(map[string]struct{})
	count := 0

	for i := startIndex; i < len(allKeys); i++ {
		key := allKeys[i]

		if options.Delimiter != "" {
			trimmed := strings.TrimPrefix(key, prefix)
			if idx := strings.Index(trimmed, options.Delimiter); idx != -1 {
				commonPrefix := prefix + trimmed[:idx+len(options.Delimiter)]
				if _, seen := prefixSet[commonPrefix]; !seen {
					prefixSet[commonPrefix] = struct{}{}
					res.CommonPrefixes = append(res.CommonPrefixes, commonPrefix)
					count++
				}
				continue
			}
		}

		res.Objects = append(res.Objects, m.objects[key].info)
		count++

		if count >= options.MaxKeys {
			if i+1 < len(allKeys) {
				res.IsTruncated = true
				res.NextContinuationToken = key
			}
			break
		}
	}

	m.mu.RUnlock()
	return res, nil
}

// PresignGet generates a signed temporary URL for retrieving an object.
func (m *MemoryStorage) PresignGet(_ context.Context, key string, ttl time.Duration, _ ...PresignOption) (string, error) {
	return m.generatePresignedURL("GET", key, ttl, "")
}

// PresignPut generates a signed temporary URL for uploading an object.
func (m *MemoryStorage) PresignPut(_ context.Context, key string, ttl time.Duration, opts ...PresignOption) (string, error) {
	var options PresignOptions
	for _, opt := range opts {
		opt(&options)
	}
	return m.generatePresignedURL("PUT", key, ttl, options.ContentType)
}

func (m *MemoryStorage) generatePresignedURL(method, key string, ttl time.Duration, contentType string) (string, error) {
	if strings.TrimSpace(key) == "" {
		return "", ErrInvalidKey
	}

	expires := time.Now().Add(ttl).Unix()
	msg := fmt.Sprintf("%s\n%s\n%d\n%s", method, key, expires, contentType)

	mac := hmac.New(sha256.New, m.secret)
	mac.Write([]byte(msg))
	sig := hex.EncodeToString(mac.Sum(nil))

	u, err := url.Parse(m.baseURL)
	if err != nil {
		return "", fmt.Errorf("storage: parse base url: %w", err)
	}

	q := u.Query()
	q.Set("key", key)
	q.Set("method", method)
	q.Set("expires", strconv.FormatInt(expires, 10))
	q.Set("sig", sig)
	if contentType != "" {
		q.Set("content_type", contentType)
	}

	u.Path = "/storage/object"
	u.RawQuery = q.Encode()

	return u.String(), nil
}

// VerifyPresignedURL validates a signed URL produced by MemoryStorage or FileSystemStorage.
func VerifyPresignedURL(rawURL string, secret []byte) (method, key string, err error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", "", fmt.Errorf("storage: parse url: %w", err)
	}

	q := u.Query()
	key = q.Get("key")
	method = q.Get("method")
	expiresStr := q.Get("expires")
	sig := q.Get("sig")
	contentType := q.Get("content_type")

	if key == "" || method == "" || expiresStr == "" || sig == "" {
		return "", "", ErrPresignSignatureMismatch
	}

	expires, err := strconv.ParseInt(expiresStr, 10, 64)
	if err != nil {
		return "", "", ErrPresignSignatureMismatch
	}

	if time.Now().Unix() > expires {
		return "", "", ErrPresignExpired
	}

	msg := fmt.Sprintf("%s\n%s\n%d\n%s", method, key, expires, contentType)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(msg))
	expectedSig := hex.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(sig), []byte(expectedSig)) {
		return "", "", ErrPresignSignatureMismatch
	}

	return method, key, nil
}
