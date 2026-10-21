package authz

import (
	"context"
	"errors"
	"sync"
)

var (
	// ErrUnauthorized is returned when no valid subject identity is found in context.
	ErrUnauthorized = errors.New("authz: unauthorized")
	// ErrForbidden is returned when the subject lacks required permissions.
	ErrForbidden = errors.New("authz: forbidden")
	// ErrTenantMismatch is returned when the subject's tenant does not match the target tenant.
	ErrTenantMismatch = errors.New("authz: tenant access denied")
)

// RoleMap maintains mappings between role names and allowed permissions.
type RoleMap struct {
	mu    sync.RWMutex
	roles map[string][]string
}

// NewRoleMap constructs a new RoleMap with the given role-to-permission mappings.
func NewRoleMap(initial map[string][]string) *RoleMap {
	rm := &RoleMap{
		roles: make(map[string][]string),
	}
	for k, v := range initial {
		perms := make([]string, len(v))
		copy(perms, v)
		rm.roles[k] = perms
	}
	return rm
}

// SetRole updates or registers the permissions granted to a role.
func (m *RoleMap) SetRole(role string, permissions ...string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	perms := make([]string, len(permissions))
	copy(perms, permissions)
	m.roles[role] = perms
}

// PermissionsForRoles aggregates all granted permissions across the specified roles.
func (m *RoleMap) PermissionsForRoles(roles ...string) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var perms []string
	seen := make(map[string]bool)
	for _, r := range roles {
		if pList, ok := m.roles[r]; ok {
			for _, p := range pList {
				if !seen[p] {
					seen[p] = true
					perms = append(perms, p)
				}
			}
		}
	}
	return perms
}

// ABACRule defines a function for dynamic attribute-based access decisions.
type ABACRule func(ctx context.Context, sub Subject, permission string) (bool, error)

// Option configures an Enforcer instance.
type Option func(*Enforcer)

// WithRoleMap configures role-to-permission definitions on an Enforcer.
func WithRoleMap(roleMap *RoleMap) Option {
	return func(e *Enforcer) {
		e.roleMap = roleMap
	}
}

// WithABACRule adds an attribute-based evaluation rule.
func WithABACRule(rule ABACRule) Option {
	return func(e *Enforcer) {
		if rule != nil {
			e.abacRules = append(e.abacRules, rule)
		}
	}
}

// Enforcer evaluates authorization decisions using RBAC and ABAC rules.
type Enforcer struct {
	roleMap   *RoleMap
	abacRules []ABACRule
}

// NewEnforcer creates a new Enforcer with the provided options.
func NewEnforcer(opts ...Option) *Enforcer {
	e := &Enforcer{
		roleMap: NewRoleMap(nil),
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// HasPermission checks whether the Subject in ctx is permitted to perform the action.
func (e *Enforcer) HasPermission(ctx context.Context, permission string) (bool, error) {
	sub, ok := SubjectFromContext(ctx)
	if !ok {
		return false, ErrUnauthorized
	}

	// 1. Direct permissions attached to the subject
	if MatchesAny(sub.Permissions, permission) {
		return true, nil
	}

	// 2. Permissions inherited through roles
	if e.roleMap != nil && len(sub.Roles) > 0 {
		rolePerms := e.roleMap.PermissionsForRoles(sub.Roles...)
		if MatchesAny(rolePerms, permission) {
			return true, nil
		}
	}

	// 3. Evaluate dynamic ABAC rules
	for _, rule := range e.abacRules {
		allowed, err := rule(ctx, sub, permission)
		if err != nil {
			return false, err
		}
		if allowed {
			return true, nil
		}
	}

	return false, nil
}

// HasRole checks whether the Subject in ctx possesses the specified role.
func (e *Enforcer) HasRole(ctx context.Context, role string) bool {
	sub, ok := SubjectFromContext(ctx)
	if !ok {
		return false
	}
	for _, r := range sub.Roles {
		if r == role || r == "admin" {
			return true
		}
	}
	return false
}

// Enforce verifies authorization, returning an error if unauthorized or forbidden.
func (e *Enforcer) Enforce(ctx context.Context, permission string) error {
	allowed, err := e.HasPermission(ctx, permission)
	if err != nil {
		return err
	}
	if !allowed {
		return ErrForbidden
	}
	return nil
}

// EnforceTenant ensures the subject's tenant matches the requested tenant ID.
func (e *Enforcer) EnforceTenant(ctx context.Context, targetTenantID string) error {
	sub, ok := SubjectFromContext(ctx)
	if !ok {
		return ErrUnauthorized
	}
	// "system" or global admin can access any tenant
	for _, r := range sub.Roles {
		if r == "system" || r == "superadmin" {
			return nil
		}
	}
	if sub.TenantID != targetTenantID {
		return ErrTenantMismatch
	}
	return nil
}
