// Package idempotency provides an HTTP middleware and storage backends
// to prevent duplicate execution of mutating operations (e.g. POST, PUT, PATCH).
// It adheres to IETF draft-ietf-httpapi-idempotency-key-header standards.
package idempotency

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/dhawalhost/gokit/cache"
)

// Response captures a cached HTTP response to replay for idempotent requests.
type Response struct {
	Fingerprint string              `json:"fingerprint"`
	StatusCode  int                 `json:"status_code"`
	Headers     map[string][]string `json:"headers"`
	Body        []byte              `json:"body"`
	CreatedAt   time.Time           `json:"created_at"`
}

// Store defines the storage operations required for idempotency control.
type Store interface {
	// Get retrieves a previously cached response for key.
	Get(ctx context.Context, key string) (*Response, bool, error)
	// Lock attempts to acquire an in-flight lock for key.
	// Returns true if acquired, false if already locked.
	Lock(ctx context.Context, key string, ttl time.Duration) (bool, error)
	// Save stores the completed response for key with the given TTL.
	Save(ctx context.Context, key string, resp *Response, ttl time.Duration) error
	// Unlock releases an in-flight lock for key.
	Unlock(ctx context.Context, key string) error
}

// Config configures the Idempotency middleware.
type Config struct {
	// Header is the HTTP header containing the idempotency key (defaults to "Idempotency-Key").
	Header string
	// Store is the storage backend. If nil, an in-memory store is used.
	Store Store
	// TTL is how long successful responses are cached (defaults to 24 hours).
	TTL time.Duration
	// LockTimeout is how long an in-flight lock is held before expiration (defaults to 1 minute).
	LockTimeout time.Duration
	// EnforceMethods lists HTTP methods to enforce idempotency on (defaults to POST, PUT, PATCH, DELETE).
	EnforceMethods []string
	// ReplayedHeader is the HTTP header set on replayed responses (defaults to "Idempotent-Replayed").
	ReplayedHeader string
}

// InMemoryStore provides a thread-safe, memory-backed idempotency store with auto-cleanup.
type InMemoryStore struct {
	mu        sync.RWMutex
	responses map[string]*Response
	locks     map[string]time.Time
	expiries  map[string]time.Time
}

// NewInMemoryStore creates a new InMemoryStore.
func NewInMemoryStore() *InMemoryStore {
	s := &InMemoryStore{
		responses: make(map[string]*Response),
		locks:     make(map[string]time.Time),
		expiries:  make(map[string]time.Time),
	}
	// Start background cleanup
	go s.cleanupLoop()
	return s
}

func (s *InMemoryStore) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	for range ticker.C {
		s.cleanup()
	}
}

func (s *InMemoryStore) cleanup() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for k, exp := range s.expiries {
		if now.After(exp) {
			delete(s.responses, k)
			delete(s.expiries, k)
		}
	}
	for k, exp := range s.locks {
		if now.After(exp) {
			delete(s.locks, k)
		}
	}
}

// Get retrieves a previously cached response for key.
func (s *InMemoryStore) Get(_ context.Context, key string) (*Response, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	resp, ok := s.responses[key]
	if !ok {
		return nil, false, nil
	}
	if exp, hasExp := s.expiries[key]; hasExp && time.Now().After(exp) {
		return nil, false, nil
	}
	return resp, true, nil
}

// Lock attempts to acquire an in-flight lock for key.
func (s *InMemoryStore) Lock(_ context.Context, key string, ttl time.Duration) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if exp, exists := s.locks[key]; exists && now.Before(exp) {
		return false, nil
	}
	s.locks[key] = now.Add(ttl)
	return true, nil
}

// Save stores the completed response for key with the given TTL.
func (s *InMemoryStore) Save(_ context.Context, key string, resp *Response, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.locks, key)
	s.responses[key] = resp
	s.expiries[key] = time.Now().Add(ttl)
	return nil
}

// Unlock releases an in-flight lock for key.
func (s *InMemoryStore) Unlock(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.locks, key)
	return nil
}

// CacheStore implements Store backed by any cache.Cache backend (e.g. Redis).
type CacheStore struct {
	cache cache.Cache
}

// NewCacheStore creates an Idempotency Store backed by cache.Cache.
func NewCacheStore(c cache.Cache) *CacheStore {
	return &CacheStore{cache: c}
}

// Get retrieves a previously cached response for key from the cache.
func (c *CacheStore) Get(ctx context.Context, key string) (*Response, bool, error) {
	val, err := c.cache.Get(ctx, "idemp:resp:"+key)
	if err != nil {
		return nil, false, nil
	}
	var resp Response
	if err := json.Unmarshal([]byte(val), &resp); err != nil {
		return nil, false, err
	}
	return &resp, true, nil
}

// Lock attempts to acquire an in-flight lock for key in the cache.
func (c *CacheStore) Lock(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	lockKey := "idemp:lock:" + key
	exists, err := c.cache.Exists(ctx, lockKey)
	if err != nil {
		return false, err
	}
	if exists {
		return false, nil
	}
	if err := c.cache.Set(ctx, lockKey, "locked", ttl); err != nil {
		return false, err
	}
	return true, nil
}

// Save stores the completed response for key with the given TTL in the cache.
func (c *CacheStore) Save(ctx context.Context, key string, resp *Response, ttl time.Duration) error {
	data, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("idempotency: marshal response: %w", err)
	}
	_ = c.cache.Delete(ctx, "idemp:lock:"+key)
	return c.cache.Set(ctx, "idemp:resp:"+key, string(data), ttl)
}

// Unlock releases an in-flight lock for key from the cache.
func (c *CacheStore) Unlock(ctx context.Context, key string) error {
	return c.cache.Delete(ctx, "idemp:lock:"+key)
}

type responseRecorder struct {
	http.ResponseWriter
	statusCode int
	body       bytes.Buffer
}

func (r *responseRecorder) WriteHeader(code int) {
	r.statusCode = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *responseRecorder) Write(b []byte) (int, error) {
	r.body.Write(b)
	return r.ResponseWriter.Write(b)
}

// computeFingerprint hashes the request method, path, query, and body.
func computeFingerprint(r *http.Request, body []byte) string {
	h := sha256.New()
	h.Write([]byte(r.Method))
	h.Write([]byte("\n"))
	h.Write([]byte(r.URL.Path))
	h.Write([]byte("\n"))
	h.Write([]byte(r.URL.RawQuery))
	h.Write([]byte("\n"))
	bodyHash := sha256.Sum256(body)
	h.Write(bodyHash[:])
	return hex.EncodeToString(h.Sum(nil))
}

// Middleware returns an HTTP middleware enforcing idempotency.
func Middleware(cfg Config) func(http.Handler) http.Handler {
	if cfg.Header == "" {
		cfg.Header = "Idempotency-Key"
	}
	if cfg.Store == nil {
		cfg.Store = NewInMemoryStore()
	}
	if cfg.TTL == 0 {
		cfg.TTL = 24 * time.Hour
	}
	if cfg.LockTimeout == 0 {
		cfg.LockTimeout = 1 * time.Minute
	}
	if len(cfg.EnforceMethods) == 0 {
		cfg.EnforceMethods = []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete}
	}
	if cfg.ReplayedHeader == "" {
		cfg.ReplayedHeader = "Idempotent-Replayed"
	}

	methodMap := make(map[string]bool, len(cfg.EnforceMethods))
	for _, m := range cfg.EnforceMethods {
		methodMap[strings.ToUpper(m)] = true
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip methods that do not mutate state
			if !methodMap[r.Method] {
				next.ServeHTTP(w, r)
				return
			}

			idempotencyKey := strings.TrimSpace(r.Header.Get(cfg.Header))
			if idempotencyKey == "" {
				// No idempotency key provided; pass through
				next.ServeHTTP(w, r)
				return
			}

			// Read request body for fingerprinting and re-attach for downstream handler
			var bodyBytes []byte
			if r.Body != nil {
				var err error
				bodyBytes, err = io.ReadAll(r.Body)
				if err != nil {
					writeJSONError(w, http.StatusBadRequest, `{"code":"BAD_REQUEST","message":"unable to read request body"}`)
					return
				}
				r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
			}

			fingerprint := computeFingerprint(r, bodyBytes)
			ctx := r.Context()

			// Check if we already have a completed response
			cachedResp, found, err := cfg.Store.Get(ctx, idempotencyKey)
			if err != nil {
				// Storage failure; proceed without idempotency guarantee
				next.ServeHTTP(w, r)
				return
			}

			if found && cachedResp != nil {
				// Anti-collision check: verify payload fingerprint
				if cachedResp.Fingerprint != fingerprint {
					writeJSONError(w, http.StatusUnprocessableEntity,
						`{"code":"IDEMPOTENCY_KEY_MISMATCH","message":"idempotency key was previously used with a different request payload"}`)
					return
				}

				// Replay the cached response
				for k, vals := range cachedResp.Headers {
					for _, v := range vals {
						w.Header().Add(k, v)
					}
				}
				w.Header().Set(cfg.ReplayedHeader, "true")
				w.WriteHeader(cachedResp.StatusCode)
				//nolint:gosec // #nosec G705 -- replaying previously cached response body
				_, _ = w.Write(cachedResp.Body)
				return
			}

			// Acquire concurrent lock
			locked, err := cfg.Store.Lock(ctx, idempotencyKey, cfg.LockTimeout)
			if err != nil || !locked {
				writeJSONError(w, http.StatusConflict,
					`{"code":"IDEMPOTENCY_CONFLICT","message":"a request with this idempotency key is currently in progress"}`)
				return
			}

			// Ensure lock is released in case of panic or unhandled error
			var completed bool
			defer func() {
				if !completed {
					_ = cfg.Store.Unlock(context.Background(), idempotencyKey)
				}
			}()

			rec := &responseRecorder{
				ResponseWriter: w,
				statusCode:     http.StatusOK,
			}

			next.ServeHTTP(rec, r)

			// Cache response on completion
			headersCopy := make(map[string][]string)
			for k, v := range rec.Header() {
				headersCopy[k] = v
			}

			saveErr := cfg.Store.Save(context.Background(), idempotencyKey, &Response{
				Fingerprint: fingerprint,
				StatusCode:  rec.statusCode,
				Headers:     headersCopy,
				Body:        rec.body.Bytes(),
				CreatedAt:   time.Now(),
			}, cfg.TTL)

			if saveErr == nil {
				completed = true
			}
		})
	}
}

func writeJSONError(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

// Sentinel errors.
var (
	ErrLockFailed  = errors.New("idempotency: lock already held")
	ErrKeyNotFound = errors.New("idempotency: key not found")
)
