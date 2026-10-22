package idempotency_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dhawalhost/gokit/idempotency"
)

type mockCache struct {
	data map[string]string
}

func newMockCache() *mockCache {
	return &mockCache{data: make(map[string]string)}
}

func (m *mockCache) Get(_ context.Context, key string) (string, error) {
	val, ok := m.data[key]
	if !ok {
		return "", fmt.Errorf("not found")
	}
	return val, nil
}

func (m *mockCache) Set(_ context.Context, key string, value any, _ time.Duration) error {
	m.data[key] = fmt.Sprintf("%v", value)
	return nil
}

func (m *mockCache) Delete(_ context.Context, keys ...string) error {
	for _, k := range keys {
		delete(m.data, k)
	}
	return nil
}

func (m *mockCache) Exists(_ context.Context, key string) (bool, error) {
	_, ok := m.data[key]
	return ok, nil
}

func (m *mockCache) Flush(_ context.Context) error {
	m.data = make(map[string]string)
	return nil
}

func TestIdempotency_Middleware_TableDriven(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "requests without idempotency key execute handler on every call",
			run: func(t *testing.T) {
				var executions int32
				handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					atomic.AddInt32(&executions, 1)
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write([]byte("ok"))
				})

				mw := idempotency.Middleware(idempotency.Config{})
				wrapped := mw(handler)

				req1 := httptest.NewRequest(http.MethodPost, "/charge", bytes.NewReader([]byte(`{"amount":100}`)))
				w1 := httptest.NewRecorder()
				wrapped.ServeHTTP(w1, req1)

				req2 := httptest.NewRequest(http.MethodPost, "/charge", bytes.NewReader([]byte(`{"amount":100}`)))
				w2 := httptest.NewRecorder()
				wrapped.ServeHTTP(w2, req2)

				if executions != 2 {
					t.Errorf("expected 2 executions without key, got %d", executions)
				}
			},
		},
		{
			name: "safe HTTP methods bypass idempotency processing",
			run: func(t *testing.T) {
				var executions int32
				handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					atomic.AddInt32(&executions, 1)
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write([]byte("data"))
				})

				mw := idempotency.Middleware(idempotency.Config{})
				wrapped := mw(handler)

				req1 := httptest.NewRequest(http.MethodGet, "/charge", nil)
				req1.Header.Set("Idempotency-Key", "key-123")
				w1 := httptest.NewRecorder()
				wrapped.ServeHTTP(w1, req1)

				req2 := httptest.NewRequest(http.MethodGet, "/charge", nil)
				req2.Header.Set("Idempotency-Key", "key-123")
				w2 := httptest.NewRecorder()
				wrapped.ServeHTTP(w2, req2)

				if executions != 2 {
					t.Errorf("expected GET to bypass idempotency, got %d executions", executions)
				}
			},
		},
		{
			name: "replays cached response with custom headers and replayed marker",
			run: func(t *testing.T) {
				var executions int32
				handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					atomic.AddInt32(&executions, 1)
					w.Header().Set("X-Custom-Header", "custom-value")
					w.WriteHeader(http.StatusCreated)
					_, _ = w.Write([]byte(`{"id":"order-123"}`))
				})

				store := idempotency.NewInMemoryStore()
				mw := idempotency.Middleware(idempotency.Config{
					Store: store,
				})
				wrapped := mw(handler)

				body := `{"amount":100,"currency":"usd"}`

				// First execution
				req1 := httptest.NewRequest(http.MethodPost, "/orders", bytes.NewReader([]byte(body)))
				req1.Header.Set("Idempotency-Key", "order-key-1")
				w1 := httptest.NewRecorder()
				wrapped.ServeHTTP(w1, req1)

				if w1.Code != http.StatusCreated {
					t.Errorf("expected status 201, got %d", w1.Code)
				}
				if w1.Header().Get("Idempotent-Replayed") == "true" {
					t.Error("first request should not be marked as replayed")
				}
				if executions != 1 {
					t.Errorf("expected 1 execution, got %d", executions)
				}

				// Replay execution
				req2 := httptest.NewRequest(http.MethodPost, "/orders", bytes.NewReader([]byte(body)))
				req2.Header.Set("Idempotency-Key", "order-key-1")
				w2 := httptest.NewRecorder()
				wrapped.ServeHTTP(w2, req2)

				if w2.Code != http.StatusCreated {
					t.Errorf("expected replayed status 201, got %d", w2.Code)
				}
				if w2.Header().Get("Idempotent-Replayed") != "true" {
					t.Error("expected Idempotent-Replayed header to be true")
				}
				if w2.Header().Get("X-Custom-Header") != "custom-value" {
					t.Errorf("expected custom header to be replayed, got %s", w2.Header().Get("X-Custom-Header"))
				}
				if w2.Body.String() != `{"id":"order-123"}` {
					t.Errorf("expected replayed body, got %s", w2.Body.String())
				}
				if executions != 1 {
					t.Errorf("handler was executed again: expected 1, got %d", executions)
				}
			},
		},
		{
			name: "payload mismatch returns 422 Unprocessable Entity",
			run: func(t *testing.T) {
				handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write([]byte("ok"))
				})

				mw := idempotency.Middleware(idempotency.Config{
					Store: idempotency.NewInMemoryStore(),
				})
				wrapped := mw(handler)

				req1 := httptest.NewRequest(http.MethodPost, "/charge", bytes.NewReader([]byte(`{"amount":10}`)))
				req1.Header.Set("Idempotency-Key", "key-mismatch-test")
				w1 := httptest.NewRecorder()
				wrapped.ServeHTTP(w1, req1)
				if w1.Code != http.StatusOK {
					t.Fatalf("expected 200, got %d", w1.Code)
				}

				req2 := httptest.NewRequest(http.MethodPost, "/charge", bytes.NewReader([]byte(`{"amount":9999}`)))
				req2.Header.Set("Idempotency-Key", "key-mismatch-test")
				w2 := httptest.NewRecorder()
				wrapped.ServeHTTP(w2, req2)

				if w2.Code != http.StatusUnprocessableEntity {
					t.Errorf("expected 422 for mismatched payload, got %d", w2.Code)
				}

				var errResp map[string]string
				_ = json.NewDecoder(w2.Body).Decode(&errResp)
				if errResp["code"] != "IDEMPOTENCY_KEY_MISMATCH" {
					t.Errorf("expected IDEMPOTENCY_KEY_MISMATCH code, got %s", errResp["code"])
				}
			},
		},
		{
			name: "concurrent in-flight request with same key returns 409 Conflict",
			run: func(t *testing.T) {
				hold := make(chan struct{})
				handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					<-hold
					w.WriteHeader(http.StatusOK)
				})

				store := idempotency.NewInMemoryStore()
				mw := idempotency.Middleware(idempotency.Config{
					Store:       store,
					LockTimeout: 10 * time.Second,
				})
				wrapped := mw(handler)

				body := []byte(`{"data":"test"}`)

				go func() {
					req := httptest.NewRequest(http.MethodPost, "/process", bytes.NewReader(body))
					req.Header.Set("Idempotency-Key", "concurrent-key")
					w := httptest.NewRecorder()
					wrapped.ServeHTTP(w, req)
				}()

				time.Sleep(20 * time.Millisecond)

				req2 := httptest.NewRequest(http.MethodPost, "/process", bytes.NewReader(body))
				req2.Header.Set("Idempotency-Key", "concurrent-key")
				w2 := httptest.NewRecorder()
				wrapped.ServeHTTP(w2, req2)

				close(hold)

				if w2.Code != http.StatusConflict {
					t.Errorf("expected 409 Conflict, got %d", w2.Code)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, tc.run)
	}
}

func TestIdempotency_CacheStore_TableDriven(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T, store idempotency.Store, ctx context.Context)
	}{
		{
			name: "lock mutual exclusion and save/get roundtrip",
			run: func(t *testing.T, store idempotency.Store, ctx context.Context) {
				locked, err := store.Lock(ctx, "test-key", time.Minute)
				if err != nil || !locked {
					t.Fatalf("Lock failed: %v", err)
				}

				locked2, _ := store.Lock(ctx, "test-key", time.Minute)
				if locked2 {
					t.Error("expected second lock to fail")
				}

				resp := &idempotency.Response{
					Fingerprint: "fp123",
					StatusCode:  http.StatusAccepted,
					Body:        []byte("processed"),
				}
				if err := store.Save(ctx, "test-key", resp, time.Hour); err != nil {
					t.Fatalf("Save failed: %v", err)
				}

				got, found, err := store.Get(ctx, "test-key")
				if err != nil || !found {
					t.Fatalf("Get failed: %v, found: %v", err, found)
				}
				if got.StatusCode != http.StatusAccepted {
					t.Errorf("expected status 202, got %d", got.StatusCode)
				}
				if string(got.Body) != "processed" {
					t.Errorf("expected body 'processed', got %s", string(got.Body))
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := newMockCache()
			store := idempotency.NewCacheStore(c)
			tc.run(t, store, context.Background())
		})
	}
}

func TestStore_UnlockAndExpiration_TableDriven(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "InMemoryStore Unlock releases lock for immediate re-acquisition",
			run: func(t *testing.T) {
				store := idempotency.NewInMemoryStore()
				ctx := context.Background()

				acquired, err := store.Lock(ctx, "lock-key", time.Minute)
				if err != nil || !acquired {
					t.Fatalf("expected lock acquisition, got %v, err=%v", acquired, err)
				}

				// Unlock
				err = store.Unlock(ctx, "lock-key")
				if err != nil {
					t.Fatalf("Unlock error: %v", err)
				}

				// Re-acquire immediately
				acquired, err = store.Lock(ctx, "lock-key", time.Minute)
				if err != nil || !acquired {
					t.Fatalf("expected re-acquisition after unlock, got %v, err=%v", acquired, err)
				}
			},
		},
		{
			name: "CacheStore Unlock releases lock and Get handles unmarshal error",
			run: func(t *testing.T) {
				mc := newMockCache()
				store := idempotency.NewCacheStore(mc)
				ctx := context.Background()

				// Lock then Unlock
				_, _ = store.Lock(ctx, "cache-lock", time.Minute)
				err := store.Unlock(ctx, "cache-lock")
				if err != nil {
					t.Fatalf("CacheStore Unlock error: %v", err)
				}

				// Corrupt cached JSON
				_ = mc.Set(ctx, "idemp:resp:corrupt-key", "{invalid-json", time.Minute)
				_, found, err := store.Get(ctx, "corrupt-key")
				if err == nil || found {
					t.Errorf("expected error on corrupt JSON, got err=%v, found=%v", err, found)
				}
			},
		},
		{
			name: "InMemoryStore Cleanup purges expired entries",
			run: func(t *testing.T) {
				store := idempotency.NewInMemoryStore()
				store.SetExpiredResponse("expired-req")

				// Ensure entry was placed in internal store
				if !store.HasResponse("expired-req") {
					t.Fatal("expected expired-req to be present in internal map initially")
				}

				// Trigger cleanup
				store.CleanupForTest()

				// Verify purged from internal map
				if store.HasResponse("expired-req") {
					t.Fatal("expected expired-req to be purged by cleanup")
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, tc.run)
	}
}
