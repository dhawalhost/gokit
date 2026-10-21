package authz_test

import (
	"context"
	"errors"
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
		name       string
		pattern    string
		permission string
		want       bool
	}{
		{
			name:       "global wildcard matches any string",
			pattern:    "*",
			permission: "anything",
			want:       true,
		},
		{
			name:       "exact permission match",
			pattern:    "users:read",
			permission: "users:read",
			want:       true,
		},
		{
			name:       "exact permission mismatch",
			pattern:    "users:read",
			permission: "users:write",
			want:       false,
		},
		{
			name:       "action wildcard matches read",
			pattern:    "users:*",
			permission: "users:read",
			want:       true,
		},
		{
			name:       "action wildcard matches write",
			pattern:    "users:*",
			permission: "users:write",
			want:       true,
		},
		{
			name:       "action wildcard rejects different resource",
			pattern:    "users:*",
			permission: "orders:read",
			want:       false,
		},
		{
			name:       "resource wildcard matches users",
			pattern:    "*:read",
			permission: "users:read",
			want:       true,
		},
		{
			name:       "resource wildcard matches orders",
			pattern:    "*:read",
			permission: "orders:read",
			want:       true,
		},
		{
			name:       "resource wildcard rejects different action",
			pattern:    "*:read",
			permission: "orders:write",
			want:       false,
		},
		{
			name:       "nested resource wildcard matches create",
			pattern:    "billing:invoices:*",
			permission: "billing:invoices:create",
			want:       true,
		},
		{
			name:       "nested resource wildcard matches refund",
			pattern:    "billing:invoices:*",
			permission: "billing:invoices:refund",
			want:       true,
		},
		{
			name:       "nested resource wildcard rejects other subresources",
			pattern:    "billing:invoices:*",
			permission: "billing:subscriptions:cancel",
			want:       false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := authz.Match(tc.pattern, tc.permission)
			if got != tc.want {
				t.Errorf("Match(%q, %q) = %v, want %v", tc.pattern, tc.permission, got, tc.want)
			}
		})
	}
}

func TestEnforcer_DirectPermissionsAndRoles(t *testing.T) {
	roles := authz.NewRoleMap(map[string][]string{
		"admin":  {"*"},
		"editor": {"documents:*", "comments:write"},
		"viewer": {"documents:read", "comments:read"},
	})
	enforcer := authz.NewEnforcer(authz.WithRoleMap(roles))

	tests := []struct {
		name       string
		subject    *authz.Subject
		permission string
		wantErr    error
	}{
		{
			name:       "subject with direct permission allowed",
			subject:    &authz.Subject{ID: "u1", Permissions: []string{"custom:export"}},
			permission: "custom:export",
			wantErr:    nil,
		},
		{
			name:       "subject without requested direct permission forbidden",
			subject:    &authz.Subject{ID: "u1", Permissions: []string{"custom:export"}},
			permission: "documents:read",
			wantErr:    authz.ErrForbidden,
		},
		{
			name:       "subject with editor role permitted for documents:write",
			subject:    &authz.Subject{ID: "u2", Roles: []string{"editor"}},
			permission: "documents:write",
			wantErr:    nil,
		},
		{
			name:       "subject with editor role forbidden for billing:view",
			subject:    &authz.Subject{ID: "u2", Roles: []string{"editor"}},
			permission: "billing:view",
			wantErr:    authz.ErrForbidden,
		},
		{
			name:       "subject with viewer role permitted for comments:read",
			subject:    &authz.Subject{ID: "u3", Roles: []string{"viewer"}},
			permission: "comments:read",
			wantErr:    nil,
		},
		{
			name:       "subject with viewer role forbidden for comments:write",
			subject:    &authz.Subject{ID: "u3", Roles: []string{"viewer"}},
			permission: "comments:write",
			wantErr:    authz.ErrForbidden,
		},
		{
			name:       "admin role permitted for any permission",
			subject:    &authz.Subject{ID: "u4", Roles: []string{"admin"}},
			permission: "super:sensitive:action",
			wantErr:    nil,
		},
		{
			name:       "unauthenticated context returns ErrUnauthorized",
			subject:    nil,
			permission: "any:action",
			wantErr:    authz.ErrUnauthorized,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			if tc.subject != nil {
				ctx = authz.WithSubject(ctx, *tc.subject)
			}
			err := enforcer.Enforce(ctx, tc.permission)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("Enforce(%q) = %v, want %v", tc.permission, err, tc.wantErr)
			}
		})
	}
}

func TestEnforcer_ABACRule(t *testing.T) {
	betaRule := func(_ context.Context, sub authz.Subject, perm string) (bool, error) {
		if perm == "features:beta" {
			if isBeta, ok := sub.Attributes["is_beta_tester"].(bool); ok && isBeta {
				return true, nil
			}
		}
		return false, nil
	}
	enforcer := authz.NewEnforcer(authz.WithABACRule(betaRule))

	tests := []struct {
		name       string
		subject    authz.Subject
		permission string
		wantErr    error
	}{
		{
			name: "beta tester attribute allows beta feature",
			subject: authz.Subject{
				ID:         "beta-1",
				Attributes: map[string]any{"is_beta_tester": true},
			},
			permission: "features:beta",
			wantErr:    nil,
		},
		{
			name: "non beta tester forbidden from beta feature",
			subject: authz.Subject{
				ID:         "reg-1",
				Attributes: map[string]any{"is_beta_tester": false},
			},
			permission: "features:beta",
			wantErr:    authz.ErrForbidden,
		},
		{
			name: "missing attribute forbidden from beta feature",
			subject: authz.Subject{
				ID:         "empty-1",
				Attributes: map[string]any{},
			},
			permission: "features:beta",
			wantErr:    authz.ErrForbidden,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := authz.WithSubject(context.Background(), tc.subject)
			err := enforcer.Enforce(ctx, tc.permission)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("Enforce(%q) = %v, want %v", tc.permission, err, tc.wantErr)
			}
		})
	}
}

func TestEnforcer_EnforceTenant(t *testing.T) {
	enforcer := authz.NewEnforcer()

	tests := []struct {
		name           string
		subject        *authz.Subject
		targetTenantID string
		wantErr        error
	}{
		{
			name:           "matching tenant ID is permitted",
			subject:        &authz.Subject{ID: "u1", TenantID: "tenant-100", Roles: []string{"member"}},
			targetTenantID: "tenant-100",
			wantErr:        nil,
		},
		{
			name:           "mismatched tenant ID returns ErrTenantMismatch",
			subject:        &authz.Subject{ID: "u1", TenantID: "tenant-100", Roles: []string{"member"}},
			targetTenantID: "tenant-200",
			wantErr:        authz.ErrTenantMismatch,
		},
		{
			name:           "superadmin role bypasses tenant check",
			subject:        &authz.Subject{ID: "admin-1", Roles: []string{"superadmin"}},
			targetTenantID: "any-tenant-id",
			wantErr:        nil,
		},
		{
			name:           "system role bypasses tenant check",
			subject:        &authz.Subject{ID: "sys-worker", Roles: []string{"system"}},
			targetTenantID: "any-tenant-id",
			wantErr:        nil,
		},
		{
			name:           "unauthenticated subject returns ErrUnauthorized",
			subject:        nil,
			targetTenantID: "tenant-100",
			wantErr:        authz.ErrUnauthorized,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			if tc.subject != nil {
				ctx = authz.WithSubject(ctx, *tc.subject)
			}
			err := enforcer.EnforceTenant(ctx, tc.targetTenantID)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("EnforceTenant(%q) = %v, want %v", tc.targetTenantID, err, tc.wantErr)
			}
		})
	}
}

func TestSubject_JWTFallback(t *testing.T) {
	roles := authz.NewRoleMap(map[string][]string{
		"manager": {"reports:*"},
	})
	enforcer := authz.NewEnforcer(authz.WithRoleMap(roles))

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

func TestMiddlewares_TableDriven(t *testing.T) {
	roles := authz.NewRoleMap(map[string][]string{
		"operator": {"servers:restart", "metrics:read"},
	})
	enforcer := authz.NewEnforcer(authz.WithRoleMap(roles))

	tests := []struct {
		name       string
		middleware func(http.Handler) http.Handler
		subject    *authz.Subject
		wantStatus int
	}{
		{
			name:       "Require without authentication returns 401",
			middleware: authz.Require(enforcer, "servers:restart"),
			subject:    nil,
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "Require with insufficient role returns 403",
			middleware: authz.Require(enforcer, "servers:restart"),
			subject:    &authz.Subject{ID: "guest", Roles: []string{"guest"}},
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "Require with matching role returns 200",
			middleware: authz.Require(enforcer, "servers:restart"),
			subject:    &authz.Subject{ID: "op1", Roles: []string{"operator"}},
			wantStatus: http.StatusOK,
		},
		{
			name:       "RequireAny with one matching permission returns 200",
			middleware: authz.RequireAny(enforcer, "unknown:perm", "metrics:read"),
			subject:    &authz.Subject{ID: "op1", Roles: []string{"operator"}},
			wantStatus: http.StatusOK,
		},
		{
			name:       "RequireAny without matches returns 403",
			middleware: authz.RequireAny(enforcer, "unknown:1", "unknown:2"),
			subject:    &authz.Subject{ID: "op1", Roles: []string{"operator"}},
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "RequireAll with all matching permissions returns 200",
			middleware: authz.RequireAll(enforcer, "servers:restart", "metrics:read"),
			subject:    &authz.Subject{ID: "op1", Roles: []string{"operator"}},
			wantStatus: http.StatusOK,
		},
		{
			name:       "RequireAll missing one permission returns 403",
			middleware: authz.RequireAll(enforcer, "servers:restart", "billing:write"),
			subject:    &authz.Subject{ID: "op1", Roles: []string{"operator"}},
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "RequireRole with matching role returns 200",
			middleware: authz.RequireRole(enforcer, "operator"),
			subject:    &authz.Subject{ID: "op1", Roles: []string{"operator"}},
			wantStatus: http.StatusOK,
		},
		{
			name:       "RequireRole missing role returns 403",
			middleware: authz.RequireRole(enforcer, "admin"),
			subject:    &authz.Subject{ID: "op1", Roles: []string{"operator"}},
			wantStatus: http.StatusForbidden,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			handler := tc.middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest(http.MethodPost, "/", nil)
			if tc.subject != nil {
				req = req.WithContext(authz.WithSubject(req.Context(), *tc.subject))
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			if w.Code != tc.wantStatus {
				t.Errorf("got status %d, want %d", w.Code, tc.wantStatus)
			}
		})
	}
}
