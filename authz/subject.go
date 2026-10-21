package authz

import (
	"context"

	"github.com/dhawalhost/gokit/middleware"
	"github.com/dhawalhost/gokit/multitenancy"
)

type subjectContextKey struct{}

// Subject represents an authenticated entity requesting access to a resource.
type Subject struct {
	ID          string         `json:"id"`
	TenantID    string         `json:"tenant_id,omitempty"`
	Roles       []string       `json:"roles,omitempty"`
	Permissions []string       `json:"permissions,omitempty"`
	Attributes  map[string]any `json:"attributes,omitempty"`
}

// WithSubject injects a Subject into context.
func WithSubject(ctx context.Context, sub Subject) context.Context {
	return context.WithValue(ctx, subjectContextKey{}, sub)
}

// SubjectFromContext retrieves the Subject from context, falling back to JWT claims if available.
func SubjectFromContext(ctx context.Context) (Subject, bool) {
	if s, ok := ctx.Value(subjectContextKey{}).(Subject); ok && s.ID != "" {
		return s, true
	}

	// Fallback to JWT claims from middleware
	if claims, ok := middleware.ClaimsFromContext(ctx); ok {
		sub := Subject{
			Attributes: make(map[string]any),
		}

		if id, ok := claims["sub"].(string); ok {
			sub.ID = id
		}
		if tid, ok := claims["tenant_id"].(string); ok {
			sub.TenantID = tid
		} else if tid, ok := multitenancy.FromContext(ctx); ok {
			sub.TenantID = tid
		}

		// Extract roles (supports []string or []any from JSON)
		if rolesRaw, ok := claims["roles"]; ok {
			sub.Roles = parseStringSlice(rolesRaw)
		} else if roleRaw, ok := claims["role"].(string); ok && roleRaw != "" {
			sub.Roles = []string{roleRaw}
		}

		// Extract permissions
		if permsRaw, ok := claims["permissions"]; ok {
			sub.Permissions = parseStringSlice(permsRaw)
		} else if permsRaw, ok := claims["scope"].(string); ok && permsRaw != "" {
			// OAuth2 space-separated scopes
			sub.Permissions = parseSpaceSeparated(permsRaw)
		}

		if sub.ID != "" {
			return sub, true
		}
	}

	return Subject{}, false
}

func parseStringSlice(raw any) []string {
	switch v := raw.(type) {
	case []string:
		return v
	case []any:
		res := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok && s != "" {
				res = append(res, s)
			}
		}
		return res
	default:
		return nil
	}
}

func parseSpaceSeparated(s string) []string {
	var res []string
	var cur []byte
	for i := 0; i < len(s); i++ {
		if s[i] == ' ' {
			if len(cur) > 0 {
				res = append(res, string(cur))
				cur = cur[:0]
			}
		} else {
			cur = append(cur, s[i])
		}
	}
	if len(cur) > 0 {
		res = append(res, string(cur))
	}
	return res
}
