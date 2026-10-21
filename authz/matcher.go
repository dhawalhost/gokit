package authz

import (
	"strings"
)

// Match checks whether a permission matches a policy pattern.
// Supports wildcards:
// - "*" matches everything.
// - "users:*" matches "users:read", "users:write".
// - "*:read" matches "users:read", "billing:read".
// - "service:resource:action" exact colon-separated matching.
func Match(pattern, permission string) bool {
	if pattern == "*" {
		return true
	}
	if pattern == permission {
		return true
	}

	patternParts := strings.Split(pattern, ":")
	permParts := strings.Split(permission, ":")

	// If pattern ends with "*", it can match one or more remaining segments
	if len(patternParts) > 0 && patternParts[len(patternParts)-1] == "*" {
		prefixLen := len(patternParts) - 1
		if len(permParts) < prefixLen {
			return false
		}
		for i := 0; i < prefixLen; i++ {
			if patternParts[i] != "*" && patternParts[i] != permParts[i] {
				return false
			}
		}
		return true
	}

	if len(patternParts) != len(permParts) {
		return false
	}

	for i := 0; i < len(patternParts); i++ {
		if patternParts[i] == "*" {
			continue
		}
		if patternParts[i] != permParts[i] {
			return false
		}
	}

	return true
}

// MatchesAny reports whether permission matches at least one pattern in patterns.
func MatchesAny(patterns []string, permission string) bool {
	for _, p := range patterns {
		if Match(p, permission) {
			return true
		}
	}
	return false
}
