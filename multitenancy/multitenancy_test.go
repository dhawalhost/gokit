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
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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

func TestContextHelpers_TableDriven(t *testing.T) {
	tests := []struct {
		name       string
		setupCtx   func() context.Context
		wantTenant string
		wantFound  bool
	}{
		{
			name: "empty context returns not found",
			setupCtx: func() context.Context {
				return context.Background()
			},
			wantTenant: "",
			wantFound:  false,
		},
		{
			name: "context with WithTenantID returns tenant string",
			setupCtx: func() context.Context {
				return multitenancy.WithTenantID(context.Background(), "tenant-alpha")
			},
			wantTenant: "tenant-alpha",
			wantFound:  true,
		},
		{
			name: "context with full WithTenant object returns tenant ID",
			setupCtx: func() context.Context {
				return multitenancy.WithTenant(context.Background(), multitenancy.Tenant{
					ID:   "tenant-beta",
					Name: "Beta Corp",
					Tier: "enterprise",
				})
			},
			wantTenant: "tenant-beta",
			wantFound:  true,
		},
		{
			name: "fallback to middleware.TenantID in context",
			setupCtx: func() context.Context {
				req := httptest.NewRequest(http.MethodGet, "/", nil)
				req.Header.Set("X-Tenant-ID", "tenant-middleware")
				var captured context.Context
				middleware.TenantID()(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
					captured = r.Context()
				})).ServeHTTP(httptest.NewRecorder(), req)
				return captured
			},
			wantTenant: "tenant-middleware",
			wantFound:  true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := tc.setupCtx()
			tid, ok := multitenancy.FromContext(ctx)
			if ok != tc.wantFound || tid != tc.wantTenant {
				t.Errorf("FromContext() = (%q, %v), want (%q, %v)", tid, ok, tc.wantTenant, tc.wantFound)
			}
		})
	}
}

func TestTenantModel(t *testing.T) {
	tests := []struct {
		name      string
		initialID string
		updatedID string
	}{
		{"standard tenant model mutation", "t-abc", "t-xyz"},
		{"uuid tenant mutation", "0192e21b-0000-7000-8000-000000000001", "0192e21b-0000-7000-8000-000000000002"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			model := &multitenancy.TenantModel{TenantID: tc.initialID}
			var scoped multitenancy.TenantScoped = model

			if scoped.GetTenantID() != tc.initialID {
				t.Errorf("GetTenantID() = %s, want %s", scoped.GetTenantID(), tc.initialID)
			}
			scoped.SetTenantID(tc.updatedID)
			if model.TenantID != tc.updatedID {
				t.Errorf("model.TenantID = %s, want %s", model.TenantID, tc.updatedID)
			}
		})
	}
}

func TestScopedCache_TableDriven(t *testing.T) {
	raw := newMockCache()
	cacheA := multitenancy.NewScopedCache(raw, "tenant-a")
	cacheB := multitenancy.NewScopedCache(raw, "tenant-b")
	ctx := context.Background()

	tests := []struct {
		name       string
		action     func(t *testing.T)
		verifyRaw  func(t *testing.T)
		verifyGets func(t *testing.T)
	}{
		{
			name: "set values under isolated tenants",
			action: func(t *testing.T) {
				_ = cacheA.Set(ctx, "session", "token-a", time.Minute)
				_ = cacheB.Set(ctx, "session", "token-b", time.Minute)
			},
			verifyRaw: func(t *testing.T) {
				if exists, _ := raw.Exists(ctx, "tenant:tenant-a:session"); !exists {
					t.Error("expected raw key tenant:tenant-a:session to exist")
				}
				if exists, _ := raw.Exists(ctx, "tenant:tenant-b:session"); !exists {
					t.Error("expected raw key tenant:tenant-b:session to exist")
				}
			},
			verifyGets: func(t *testing.T) {
				valA, _ := cacheA.Get(ctx, "session")
				valB, _ := cacheB.Get(ctx, "session")
				if valA != "token-a" {
					t.Errorf("cacheA.Get() = %q, want token-a", valA)
				}
				if valB != "token-b" {
					t.Errorf("cacheB.Get() = %q, want token-b", valB)
				}
			},
		},
		{
			name: "delete under tenant A does not affect tenant B",
			action: func(t *testing.T) {
				_ = cacheA.Delete(ctx, "session")
			},
			verifyRaw: func(t *testing.T) {
				if exists, _ := raw.Exists(ctx, "tenant:tenant-a:session"); exists {
					t.Error("expected tenant A session to be removed from raw cache")
				}
				if exists, _ := raw.Exists(ctx, "tenant:tenant-b:session"); !exists {
					t.Error("expected tenant B session to remain in raw cache")
				}
			},
			verifyGets: func(t *testing.T) {
				existsA, _ := cacheA.Exists(ctx, "session")
				existsB, _ := cacheB.Exists(ctx, "session")
				if existsA {
					t.Error("expected session not to exist for tenant A")
				}
				if !existsB {
					t.Error("expected session to still exist for tenant B")
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.action(t)
			tc.verifyRaw(t)
			tc.verifyGets(t)
		})
	}
}

func TestRequireTenantMiddleware_TableDriven(t *testing.T) {
	tests := []struct {
		name           string
		middlewareOpts []multitenancy.MiddlewareOption
		requestHeader  map[string]string
		contextTenant  string
		wantStatus     int
		wantExecuted   bool
	}{
		{
			name:           "missing X-Tenant-ID returns 400 Bad Request",
			middlewareOpts: nil,
			requestHeader:  nil,
			contextTenant:  "",
			wantStatus:     http.StatusBadRequest,
			wantExecuted:   false,
		},
		{
			name:           "present X-Tenant-ID passes with 200 OK",
			middlewareOpts: nil,
			requestHeader:  map[string]string{"X-Tenant-ID": "acme-123"},
			contextTenant:  "",
			wantStatus:     http.StatusOK,
			wantExecuted:   true,
		},
		{
			name:           "tenant already in context passes without header",
			middlewareOpts: nil,
			requestHeader:  nil,
			contextTenant:  "pre-authenticated-tenant",
			wantStatus:     http.StatusOK,
			wantExecuted:   true,
		},
		{
			name: "custom header name missing returns configured 401 status",
			middlewareOpts: []multitenancy.MiddlewareOption{
				multitenancy.WithHeader("X-Organization-ID"),
				multitenancy.WithStatusCode(http.StatusUnauthorized),
			},
			requestHeader: map[string]string{"X-Tenant-ID": "wrong-header"},
			contextTenant: "",
			wantStatus:    http.StatusUnauthorized,
			wantExecuted:  false,
		},
		{
			name: "custom header name present passes with 200 OK",
			middlewareOpts: []multitenancy.MiddlewareOption{
				multitenancy.WithHeader("X-Organization-ID"),
				multitenancy.WithStatusCode(http.StatusUnauthorized),
			},
			requestHeader: map[string]string{"X-Organization-ID": "org-corp"},
			contextTenant: "",
			wantStatus:    http.StatusOK,
			wantExecuted:  true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			executed := false
			handler := multitenancy.RequireTenant(tc.middlewareOpts...)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				executed = true
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest(http.MethodGet, "/resource", nil)
			for k, v := range tc.requestHeader {
				req.Header.Set(k, v)
			}
			if tc.contextTenant != "" {
				req = req.WithContext(multitenancy.WithTenantID(req.Context(), tc.contextTenant))
			}

			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			if w.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tc.wantStatus)
			}
			if executed != tc.wantExecuted {
				t.Errorf("handler executed = %v, want %v", executed, tc.wantExecuted)
			}
		})
	}
}

func TestTenantFromContext_TableDriven(t *testing.T) {
	tests := []struct {
		name       string
		setup      func() context.Context
		wantTenant multitenancy.Tenant
		wantFound  bool
	}{
		{
			name: "full Tenant injected with WithTenant",
			setup: func() context.Context {
				return multitenancy.WithTenant(context.Background(), multitenancy.Tenant{
					ID:   "tenant-corp",
					Name: "Corp Inc",
					Tier: "enterprise",
				})
			},
			wantTenant: multitenancy.Tenant{ID: "tenant-corp", Name: "Corp Inc", Tier: "enterprise"},
			wantFound:  true,
		},
		{
			name: "fallback to middleware.WithTenantID",
			setup: func() context.Context {
				return middleware.WithTenantID(context.Background(), "middleware-tenant")
			},
			wantTenant: multitenancy.Tenant{ID: "middleware-tenant"},
			wantFound:  true,
		},
		{
			name: "empty context returns zero Tenant and false",
			setup: func() context.Context {
				return context.Background()
			},
			wantTenant: multitenancy.Tenant{},
			wantFound:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := multitenancy.TenantFromContext(tc.setup())
			if ok != tc.wantFound {
				t.Errorf("found = %v, want %v", ok, tc.wantFound)
			}
			if got != tc.wantTenant {
				t.Errorf("got tenant %+v, want %+v", got, tc.wantTenant)
			}
		})
	}
}

func TestContextScopedCache_TableDriven(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "ContextScopedCache Set, Get, Exists, and Delete operations",
			run: func(t *testing.T) {
				raw := newMockCache()
				csc := multitenancy.NewContextScopedCache(raw)
				ctx := multitenancy.WithTenantID(context.Background(), "tenant-a")

				// Set
				err := csc.Set(ctx, "session:1", "val-1", 10*time.Minute)
				if err != nil {
					t.Fatalf("Set error: %v", err)
				}

				// Exists
				exists, err := csc.Exists(ctx, "session:1")
				if err != nil || !exists {
					t.Fatalf("expected key to exist, got exists=%v, err=%v", exists, err)
				}

				// Get
				val, err := csc.Get(ctx, "session:1")
				if err != nil || val != "val-1" {
					t.Fatalf("expected val-1, got %q, err=%v", val, err)
				}

				// Delete empty keys
				err = csc.Delete(ctx)
				if err != nil {
					t.Fatalf("Delete empty keys error: %v", err)
				}

				// Delete multiple keys
				err = csc.Delete(ctx, "session:1")
				if err != nil {
					t.Fatalf("Delete key error: %v", err)
				}

				// Exists after delete
				exists, err = csc.Exists(ctx, "session:1")
				if err != nil || exists {
					t.Fatalf("expected key to not exist, got exists=%v", exists)
				}

				// Flush
				_ = csc.Set(ctx, "temp", "val", time.Hour)
				err = csc.Flush(ctx)
				if err != nil {
					t.Fatalf("Flush error: %v", err)
				}
				exists, _ = csc.Exists(ctx, "temp")
				if exists {
					t.Fatal("expected temp key to be flushed")
				}
			},
		},
		{
			name: "ScopedCache Flush and ScopedKeyFromContext fallback",
			run: func(t *testing.T) {
				raw := newMockCache()
				sc := multitenancy.NewScopedCache(raw, "tenant-b")
				ctx := context.Background()

				_ = sc.Set(ctx, "user:1", "data", time.Hour)
				err := sc.Flush(ctx)
				if err != nil {
					t.Fatalf("ScopedCache Flush error: %v", err)
				}

				// ScopedKeyFromContext with no tenant in context
				key := multitenancy.ScopedKeyFromContext(context.Background(), "unscoped")
				if key != "tenant:default:unscoped" {
					t.Fatalf("expected tenant:default:unscoped when no tenant in ctx, got %q", key)
				}

				// TenantModel GetTenantID and SetTenantID
				tm := &multitenancy.TenantModel{}
				tm.SetTenantID("t-model")
				if tm.GetTenantID() != "t-model" {
					t.Fatalf("expected t-model, got %q", tm.GetTenantID())
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, tc.run)
	}
}

func TestScope_TableDriven(t *testing.T) {
	tests := []struct {
		name     string
		tenantID string
		ctx      context.Context
	}{
		{
			name:     "Scope returns valid modifier",
			tenantID: "tenant-100",
			ctx:      context.Background(),
		},
		{
			name:     "ScopeFromContext with tenant in context",
			tenantID: "tenant-200",
			ctx:      multitenancy.WithTenantID(context.Background(), "tenant-200"),
		},
		{
			name:     "ScopeFromContext without tenant in context",
			tenantID: "",
			ctx:      context.Background(),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := &gorm.DB{Config: &gorm.Config{}, Statement: &gorm.Statement{Clauses: make(map[string]clause.Clause)}}
			var scoped *gorm.DB
			if tc.tenantID != "" && tc.ctx == context.Background() {
				scoped = multitenancy.Scope(tc.tenantID)(db)
			} else {
				scoped = multitenancy.ScopeFromContext(tc.ctx)(db)
			}
			if scoped == nil {
				t.Fatal("expected non-nil scoped db")
			}
		})
	}
}

func TestScopedCache_EmptyKeys_TableDriven(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "ScopedCache.Delete with zero keys returns nil",
			run: func(t *testing.T) {
				cache := multitenancy.NewScopedCache(newMockCache(), "t1")
				if err := cache.Delete(context.Background()); err != nil {
					t.Fatalf("expected nil error on empty delete, got %v", err)
				}
			},
		},
		{
			name: "ContextScopedCache.Delete with zero keys returns nil",
			run: func(t *testing.T) {
				cache := multitenancy.NewContextScopedCache(newMockCache())
				if err := cache.Delete(context.Background()); err != nil {
					t.Fatalf("expected nil error on empty delete, got %v", err)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, tc.run)
	}
}
