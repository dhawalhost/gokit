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

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/dhawalhost/gokit/cache"
	"github.com/dhawalhost/gokit/multitenancy"
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
	// MaxBodyBytes limits the request body size read for fingerprinting (defaults to 1 MiB).
	// Set to a negative value to disable the limit.
	MaxBodyBytes int64
	// Cache5xx controls whether 5xx responses are cached and replayed (defaults to false).
	Cache5xx bool
	// KeyFunc derives the storage key from the request and header value.
	// Defaults to scoping by tenant ID and authenticated subject plus header value.
	KeyFunc func(r *http.Request, rawKey string) string
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
	cache  cache.Cache
	mu     sync.Mutex
	tokens map[string]string
}

// NewCacheStore creates an Idempotency Store backed by cache.Cache.
func NewCacheStore(c cache.Cache) *CacheStore {
	return &CacheStore{
		cache:  c,
		tokens: make(map[string]string),
	}
}

// Get retrieves a previously cached response for key from the cache.
func (c *CacheStore) Get(ctx context.Context, key string) (*Response, bool, error) {
	val, err := c.cache.Get(ctx, "idemp:resp:"+key)
	if err != nil {
		if errors.Is(err, redis.Nil) || strings.Contains(strings.ToLower(err.Error()), "not found") {
			return nil, false, nil
		}
		return nil, false, err
	}
	var resp Response
	if err := json.Unmarshal([]byte(val), &resp); err != nil {
		return nil, false, err
	}
	return &resp, true, nil
}

// Lock attempts to acquire an in-flight lock for key in the cache using an atomic
// SetNX primitive when available.
func (c *CacheStore) Lock(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	lockKey := "idemp:lock:" + key
	token := uuid.NewString()

	if locker, ok := c.cache.(cache.AtomicLocker); ok {
		acquired, err := locker.SetNX(ctx, lockKey, token, ttl)
		if err != nil || !acquired {
			return false, err
		}
		c.mu.Lock()
		if c.tokens == nil {
			c.tokens = make(map[string]string)
		}
		c.tokens[key] = token
		c.mu.Unlock()
		return true, nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	exists, err := c.cache.Exists(ctx, lockKey)
	if err != nil {
		return false, err
	}
	if exists {
		return false, nil
	}
	if err := c.cache.Set(ctx, lockKey, token, ttl); err != nil {
		return false, err
	}
	if c.tokens == nil {
		c.tokens = make(map[string]string)
	}
	c.tokens[key] = token
	return true, nil
}

func (c *CacheStore) releaseLock(ctx context.Context, key string) error {
	lockKey := "idemp:lock:" + key
	c.mu.Lock()
	token, hasToken := c.tokens[key]
	if hasToken {
		delete(c.tokens, key)
	}
	c.mu.Unlock()

	if locker, ok := c.cache.(cache.AtomicLocker); ok {
		if !hasToken {
			return nil
		}
		_, err := locker.CompareAndDelete(ctx, lockKey, token)
		return err
	}
	return c.cache.Delete(ctx, lockKey)
}

// Save stores the completed response for key with the given TTL in the cache.
func (c *CacheStore) Save(ctx context.Context, key string, resp *Response, ttl time.Duration) error {
	data, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("idempotency: marshal response: %w", err)
	}
	_ = c.releaseLock(ctx, key)
	return c.cache.Set(ctx, "idemp:resp:"+key, string(data), ttl)
}

// Unlock releases an in-flight lock for key from the cache.
func (c *CacheStore) Unlock(ctx context.Context, key string) error {
	return c.releaseLock(ctx, key)
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

func defaultScopedKey(r *http.Request, rawKey string) string {
	tenant, _ := multitenancy.FromContext(r.Context())
	var subject string
	if claims, ok := multitenancy.ClaimsMapFromContext(r.Context()); ok {
		if sub, ok := claims["sub"].(string); ok {
			subject = strings.TrimSpace(sub)
		} else if uid, ok := claims["user_id"].(string); ok {
			subject = strings.TrimSpace(uid)
		}
	}
	if tenant == "" && subject == "" {
		return rawKey
	}
	return tenant + ":" + subject + ":" + rawKey
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
	maxBodyBytes := cfg.MaxBodyBytes
	if maxBodyBytes == 0 {
		maxBodyBytes = 1 << 20 // 1 MiB
	}
	keyFunc := cfg.KeyFunc
	if keyFunc == nil {
		keyFunc = defaultScopedKey
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

			// Read bounded request body for fingerprinting and re-attach for downstream handler
			var bodyBytes []byte
			if r.Body != nil {
				bodyReader := io.ReadCloser(r.Body)
				if maxBodyBytes > 0 {
					bodyReader = http.MaxBytesReader(w, r.Body, maxBodyBytes)
				}
				var err error
				bodyBytes, err = io.ReadAll(bodyReader)
				_ = bodyReader.Close()
				if err != nil {
					var maxErr *http.MaxBytesError
					if errors.As(err, &maxErr) {
						writeJSONError(w, http.StatusRequestEntityTooLarge,
							`{"code":"PAYLOAD_TOO_LARGE","message":"request body exceeds maximum allowed size"}`)
						return
					}
					writeJSONError(w, http.StatusBadRequest, `{"code":"BAD_REQUEST","message":"unable to read request body"}`)
					return
				}
				r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
			}

			fingerprint := computeFingerprint(r, bodyBytes)
			scopedKey := keyFunc(r, idempotencyKey)
			ctx := r.Context()

			// Check if we already have a completed response
			cachedResp, found, err := cfg.Store.Get(ctx, scopedKey)
			if err != nil {
				writeJSONError(w, http.StatusServiceUnavailable,
					`{"code":"IDEMPOTENCY_STORE_UNAVAILABLE","message":"idempotency store unavailable"}`)
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
			locked, err := cfg.Store.Lock(ctx, scopedKey, cfg.LockTimeout)
			if err != nil {
				writeJSONError(w, http.StatusServiceUnavailable,
					`{"code":"IDEMPOTENCY_STORE_UNAVAILABLE","message":"idempotency store unavailable"}`)
				return
			}
			if !locked {
				writeJSONError(w, http.StatusConflict,
					`{"code":"IDEMPOTENCY_CONFLICT","message":"a request with this idempotency key is currently in progress"}`)
				return
			}

			// Ensure lock is released in case of panic, 5xx skip, or unhandled error
			var completed bool
			defer func() {
				if !completed {
					_ = cfg.Store.Unlock(context.Background(), scopedKey)
				}
			}()

			rec := &responseRecorder{
				ResponseWriter: w,
				statusCode:     http.StatusOK,
			}

			next.ServeHTTP(rec, r)

			// Skip caching 5xx server errors unless explicitly enabled
			if rec.statusCode >= http.StatusInternalServerError && !cfg.Cache5xx {
				return
			}

			// Cache response on completion
			headersCopy := make(map[string][]string)
			for k, v := range rec.Header() {
				headersCopy[k] = v
			}

			saveErr := cfg.Store.Save(context.Background(), scopedKey, &Response{
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
