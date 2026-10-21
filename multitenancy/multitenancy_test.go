package multitenancy_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/dhawalhost/gokit/middleware"
	"github.com/dhawalhost/gokit/multitenancy"
)

type mockCache struct {
	mu    sync.RWMutex
	items map[string]string
}

func newMockCache() *mockCache {
	return &mockCache{items: make(map[string]string)}
}

func (m *mockCache) Get(_ context.Context, key string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	val, ok := m.items[key]
	if !ok {
		return "", nil
	}
	return val, nil
}

func (m *mockCache) Set(_ context.Context, key string, value any, _ time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.items[key] = value.(string)
	return nil
}

func (m *mockCache) Delete(_ context.Context, keys ...string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, k := range keys {
		delete(m.items, k)
	}
	return nil
}

func (m *mockCache) Exists(_ context.Context, key string) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.items[key]
	return ok, nil
}

func (m *mockCache) Flush(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.items = make(map[string]string)
	return nil
}

func TestContextHelpers(t *testing.T) {
	ctx := context.Background()

	// Initial empty
	if _, ok := multitenancy.FromContext(ctx); ok {
		t.Error("expected no tenant initially")
	}

	// Injected via WithTenantID
	ctx1 := multitenancy.WithTenantID(ctx, "tenant-1")
	tid, ok := multitenancy.FromContext(ctx1)
	if !ok || tid != "tenant-1" {
		t.Errorf("expected tenant-1, got %q (ok=%v)", tid, ok)
	}

	// Injected via WithTenant full object
	ctx2 := multitenancy.WithTenant(ctx, multitenancy.Tenant{
		ID:   "tenant-2",
		Name: "Acme Corp",
		Tier: "enterprise",
	})
	tenant, ok := multitenancy.TenantFromContext(ctx2)
	if !ok || tenant.ID != "tenant-2" || tenant.Name != "Acme Corp" || tenant.Tier != "enterprise" {
		t.Errorf("unexpected tenant: %+v", tenant)
	}

	// Fallback to middleware.TenantID
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Tenant-ID", "tenant-middleware")
	middlewareHandler := middleware.TenantID()(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got, ok := multitenancy.FromContext(r.Context())
		if !ok || got != "tenant-middleware" {
			t.Errorf("expected fallback to middleware tenant, got %q (ok=%v)", got, ok)
		}
	}))
	middlewareHandler.ServeHTTP(httptest.NewRecorder(), req)
}

func TestTenantModel(t *testing.T) {
	model := &multitenancy.TenantModel{TenantID: "t-abc"}
	var scoped multitenancy.TenantScoped = model

	if scoped.GetTenantID() != "t-abc" {
		t.Errorf("expected t-abc, got %s", scoped.GetTenantID())
	}
	scoped.SetTenantID("t-xyz")
	if model.TenantID != "t-xyz" {
		t.Errorf("expected t-xyz, got %s", model.TenantID)
	}
}

func TestScopedCache(t *testing.T) {
	raw := newMockCache()
	cacheA := multitenancy.NewScopedCache(raw, "tenant-a")
	cacheB := multitenancy.NewScopedCache(raw, "tenant-b")

	ctx := context.Background()

	// Store key "session" under tenant-a
	if err := cacheA.Set(ctx, "session", "token-a", time.Minute); err != nil {
		t.Fatalf("cacheA.Set: %v", err)
	}

	// Store key "session" under tenant-b with different value
	if err := cacheB.Set(ctx, "session", "token-b", time.Minute); err != nil {
		t.Fatalf("cacheB.Set: %v", err)
	}

	// Verify tenant-a gets token-a
	valA, err := cacheA.Get(ctx, "session")
	if err != nil || valA != "token-a" {
		t.Errorf("cacheA.Get = %q (err=%v), want token-a", valA, err)
	}

	// Verify tenant-b gets token-b
	valB, err := cacheB.Get(ctx, "session")
	if err != nil || valB != "token-b" {
		t.Errorf("cacheB.Get = %q (err=%v), want token-b", valB, err)
	}

	// Verify underlying raw keys are isolated
	existsA, _ := raw.Exists(ctx, "tenant:tenant-a:session")
	existsB, _ := raw.Exists(ctx, "tenant:tenant-b:session")
	if !existsA || !existsB {
		t.Errorf("expected both tenant namespaces in underlying raw cache")
	}

	// Delete from tenant-a does not delete tenant-b
	_ = cacheA.Delete(ctx, "session")
	existsA, _ = cacheA.Exists(ctx, "session")
	existsB, _ = cacheB.Exists(ctx, "session")
	if existsA {
		t.Error("expected tenant-a session to be deleted")
	}
	if !existsB {
		t.Error("expected tenant-b session to remain intact")
	}
}

func TestContextScopedCache(t *testing.T) {
	raw := newMockCache()
	ctxCache := multitenancy.NewContextScopedCache(raw)

	ctxA := multitenancy.WithTenantID(context.Background(), "t-1")
	ctxB := multitenancy.WithTenantID(context.Background(), "t-2")

	_ = ctxCache.Set(ctxA, "config", "cfg1", time.Minute)
	_ = ctxCache.Set(ctxB, "config", "cfg2", time.Minute)

	gotA, _ := ctxCache.Get(ctxA, "config")
	gotB, _ := ctxCache.Get(ctxB, "config")

	if gotA != "cfg1" || gotB != "cfg2" {
		t.Errorf("gotA=%q gotB=%q, want cfg1 and cfg2", gotA, gotB)
	}
}

func TestRequireTenantMiddleware(t *testing.T) {
	called := false
	handler := multitenancy.RequireTenant()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		tid, ok := multitenancy.FromContext(r.Context())
		if !ok || tid != "tenant-active" {
			t.Errorf("expected tenant-active in handler, got %q", tid)
		}
		w.WriteHeader(http.StatusOK)
	}))

	// 1. Missing tenant -> 400 Bad Request
	reqMissing := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	wMissing := httptest.NewRecorder()
	handler.ServeHTTP(wMissing, reqMissing)
	if wMissing.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request, got %d", wMissing.Code)
	}
	if called {
		t.Error("handler should not have been called on missing tenant")
	}

	// 2. Present header -> 200 OK
	called = false
	reqPresent := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	reqPresent.Header.Set("X-Tenant-ID", "tenant-active")
	wPresent := httptest.NewRecorder()
	handler.ServeHTTP(wPresent, reqPresent)
	if wPresent.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", wPresent.Code)
	}
	if !called {
		t.Error("expected handler to be invoked")
	}

	// 3. Custom options: header name and status code
	customHandler := multitenancy.RequireTenant(
		multitenancy.WithHeader("X-Org-ID"),
		multitenancy.WithStatusCode(http.StatusUnauthorized),
	)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	reqCustom := httptest.NewRequest(http.MethodGet, "/", nil)
	wCustom := httptest.NewRecorder()
	customHandler.ServeHTTP(wCustom, reqCustom)
	if wCustom.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized, got %d", wCustom.Code)
	}

	reqCustomOk := httptest.NewRequest(http.MethodGet, "/", nil)
	reqCustomOk.Header.Set("X-Org-ID", "org-123")
	wCustomOk := httptest.NewRecorder()
	customHandler.ServeHTTP(wCustomOk, reqCustomOk)
	if wCustomOk.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", wCustomOk.Code)
	}
}
