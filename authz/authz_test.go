package authz_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/dhawalhost/gokit/authz"
	"github.com/dhawalhost/gokit/middleware"
)

func TestMatcher(t *testing.T) {
	tests := []struct {
		pattern    string
		permission string
		want       bool
	}{
		{"*", "anything", true},
		{"users:read", "users:read", true},
		{"users:read", "users:write", false},
		{"users:*", "users:read", true},
		{"users:*", "users:write", true},
		{"users:*", "orders:read", false},
		{"*:read", "users:read", true},
		{"*:read", "orders:read", true},
		{"*:read", "orders:write", false},
		{"billing:invoices:*", "billing:invoices:create", true},
		{"billing:invoices:*", "billing:invoices:refund", true},
		{"billing:invoices:*", "billing:subscriptions:cancel", false},
	}

	for _, tc := range tests {
		got := authz.Match(tc.pattern, tc.permission)
		if got != tc.want {
			t.Errorf("Match(%q, %q) = %v, want %v", tc.pattern, tc.permission, got, tc.want)
		}
	}
}

func TestEnforcer_DirectPermissionsAndRoles(t *testing.T) {
	roles := authz.NewRoleMap(map[string][]string{
		"admin":  {"*"},
		"editor": {"documents:*", "comments:write"},
		"viewer": {"documents:read", "comments:read"},
	})

	enforcer := authz.NewEnforcer(authz.WithRoleMap(roles))

	// 1. Subject with direct permission
	ctxDirect := authz.WithSubject(context.Background(), authz.Subject{
		ID:          "user-1",
		Permissions: []string{"custom:export"},
	})
	if err := enforcer.Enforce(ctxDirect, "custom:export"); err != nil {
		t.Errorf("expected custom:export allowed, got %v", err)
	}
	if err := enforcer.Enforce(ctxDirect, "documents:read"); err != authz.ErrForbidden {
		t.Errorf("expected documents:read forbidden, got %v", err)
	}

	// 2. Subject with role 'editor'
	ctxEditor := authz.WithSubject(context.Background(), authz.Subject{
		ID:    "user-2",
		Roles: []string{"editor"},
	})
	if err := enforcer.Enforce(ctxEditor, "documents:write"); err != nil {
		t.Errorf("expected editor documents:write allowed, got %v", err)
	}
	if err := enforcer.Enforce(ctxEditor, "billing:view"); err != authz.ErrForbidden {
		t.Errorf("expected editor billing:view forbidden, got %v", err)
	}

	// 3. Subject with role 'admin'
	ctxAdmin := authz.WithSubject(context.Background(), authz.Subject{
		ID:    "user-3",
		Roles: []string{"admin"},
	})
	if err := enforcer.Enforce(ctxAdmin, "anything:at:all"); err != nil {
		t.Errorf("expected admin allowed everything, got %v", err)
	}

	// 4. Missing subject
	if err := enforcer.Enforce(context.Background(), "test"); err != authz.ErrUnauthorized {
		t.Errorf("expected ErrUnauthorized when no subject, got %v", err)
	}
}

func TestEnforcer_ABACRule(t *testing.T) {
	// Custom rule: allow action if subject attribute "is_beta_tester" is true
	betaRule := func(_ context.Context, sub authz.Subject, perm string) (bool, error) {
		if perm == "features:beta" {
			if isBeta, ok := sub.Attributes["is_beta_tester"].(bool); ok && isBeta {
				return true, nil
			}
		}
		return false, nil
	}

	enforcer := authz.NewEnforcer(authz.WithABACRule(betaRule))

	ctxBeta := authz.WithSubject(context.Background(), authz.Subject{
		ID: "beta-user",
		Attributes: map[string]any{
			"is_beta_tester": true,
		},
	})
	if err := enforcer.Enforce(ctxBeta, "features:beta"); err != nil {
		t.Errorf("expected beta user allowed, got %v", err)
	}

	ctxRegular := authz.WithSubject(context.Background(), authz.Subject{
		ID: "reg-user",
		Attributes: map[string]any{
			"is_beta_tester": false,
		},
	})
	if err := enforcer.Enforce(ctxRegular, "features:beta"); err != authz.ErrForbidden {
		t.Errorf("expected regular user forbidden from beta, got %v", err)
	}
}

func TestEnforcer_EnforceTenant(t *testing.T) {
	enforcer := authz.NewEnforcer()

	ctxTenant := authz.WithSubject(context.Background(), authz.Subject{
		ID:       "user-tenant",
		TenantID: "tenant-100",
		Roles:    []string{"member"},
	})

	if err := enforcer.EnforceTenant(ctxTenant, "tenant-100"); err != nil {
		t.Errorf("expected tenant match, got %v", err)
	}
	if err := enforcer.EnforceTenant(ctxTenant, "tenant-200"); err != authz.ErrTenantMismatch {
		t.Errorf("expected ErrTenantMismatch, got %v", err)
	}

	// Superadmin bypass
	ctxSuper := authz.WithSubject(context.Background(), authz.Subject{
		ID:    "superadmin",
		Roles: []string{"superadmin"},
	})
	if err := enforcer.EnforceTenant(ctxSuper, "any-tenant"); err != nil {
		t.Errorf("expected superadmin allowed any tenant, got %v", err)
	}
}

func TestSubject_JWTFallback(t *testing.T) {
	roles := authz.NewRoleMap(map[string][]string{
		"manager": {"reports:*"},
	})
	enforcer := authz.NewEnforcer(authz.WithRoleMap(roles))

	// Setup JWT middleware with claims
	cfg := middleware.JWTConfig{SecretKey: []byte("authz-test-secret-32-bytes-long")}
	claims := jwt.MapClaims{
		"sub":         "jwt-subject-99",
		"tenant_id":   "org-apple",
		"roles":       []any{"manager"},
		"permissions": []any{"direct:perm"},
		"exp":         time.Now().Add(time.Hour).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenStr, _ := token.SignedString(cfg.SecretKey)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+tokenStr)

	h := middleware.JWT(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sub, ok := authz.SubjectFromContext(r.Context())
		if !ok || sub.ID != "jwt-subject-99" || sub.TenantID != "org-apple" {
			t.Fatalf("expected JWT claims extracted into Subject: %+v", sub)
		}

		if err := enforcer.Enforce(r.Context(), "reports:sales"); err != nil {
			t.Errorf("expected reports:sales permitted via manager role: %v", err)
		}
		if err := enforcer.Enforce(r.Context(), "direct:perm"); err != nil {
			t.Errorf("expected direct:perm permitted via claims: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))

	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", w.Code)
	}
}

func TestMiddlewares(t *testing.T) {
	roles := authz.NewRoleMap(map[string][]string{
		"operator": {"servers:restart", "metrics:read"},
	})
	enforcer := authz.NewEnforcer(authz.WithRoleMap(roles))

	// 1. Require middleware
	handler := authz.Require(enforcer, "servers:restart")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Unauthenticated
	req1 := httptest.NewRequest(http.MethodPost, "/restart", nil)
	w1 := httptest.NewRecorder()
	handler.ServeHTTP(w1, req1)
	if w1.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized, got %d", w1.Code)
	}

	// Forbidden
	req2 := httptest.NewRequest(http.MethodPost, "/restart", nil)
	ctx2 := authz.WithSubject(req2.Context(), authz.Subject{ID: "guest", Roles: []string{"guest"}})
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, req2.WithContext(ctx2))
	if w2.Code != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden, got %d", w2.Code)
	}

	// Permitted
	req3 := httptest.NewRequest(http.MethodPost, "/restart", nil)
	ctx3 := authz.WithSubject(req3.Context(), authz.Subject{ID: "op1", Roles: []string{"operator"}})
	w3 := httptest.NewRecorder()
	handler.ServeHTTP(w3, req3.WithContext(ctx3))
	if w3.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", w3.Code)
	}

	// 2. RequireAny
	anyHandler := authz.RequireAny(enforcer, "unknown:action", "metrics:read")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	wAny := httptest.NewRecorder()
	anyHandler.ServeHTTP(wAny, req3.WithContext(ctx3))
	if wAny.Code != http.StatusOK {
		t.Errorf("expected RequireAny to pass, got %d", wAny.Code)
	}

	// 3. RequireRole
	roleHandler := authz.RequireRole(enforcer, "operator")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	wRole := httptest.NewRecorder()
	roleHandler.ServeHTTP(wRole, req3.WithContext(ctx3))
	if wRole.Code != http.StatusOK {
		t.Errorf("expected RequireRole to pass, got %d", wRole.Code)
	}
}
