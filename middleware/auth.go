package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

// JWTConfig holds configuration for the JWT middleware.
type JWTConfig struct {
	// SecretKey is the HMAC secret used to verify tokens.
	SecretKey []byte
	// Algorithm is the expected signing algorithm (defaults to "HS256").
	Algorithm string
	// ContextKey is the context key under which claims are stored (defaults to "claims").
	ContextKey string
}

type jwtClaimsContextKey struct{ key string }
type defaultClaimsContextKey struct{}

// JWT returns a middleware that validates Bearer tokens.
// On success the jwt.MapClaims are stored in the request context.
// Tokens must include a valid signature, non-expired expiration (exp),
// and matching signing algorithm.
func JWT(cfg JWTConfig) func(http.Handler) http.Handler {
	if cfg.ContextKey == "" {
		cfg.ContextKey = "claims"
	}
	if cfg.Algorithm == "" {
		cfg.Algorithm = "HS256"
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
				writeJSONError(w, http.StatusUnauthorized, `{"code":"UNAUTHORIZED","message":"missing or invalid authorization header"}`)
				return
			}
			tokenStr := strings.TrimPrefix(authHeader, "Bearer ")
			token, err := jwt.Parse(tokenStr,
				func(t *jwt.Token) (interface{}, error) {
					if t.Method.Alg() != cfg.Algorithm {
						return nil, jwt.ErrSignatureInvalid
					}
					return cfg.SecretKey, nil
				},
				jwt.WithValidMethods([]string{cfg.Algorithm}),
				jwt.WithExpirationRequired(),
			)
			if err != nil || !token.Valid {
				writeJSONError(w, http.StatusUnauthorized, `{"code":"UNAUTHORIZED","message":"invalid token"}`)
				return
			}
			claims, ok := token.Claims.(jwt.MapClaims)
			if !ok {
				writeJSONError(w, http.StatusUnauthorized, `{"code":"UNAUTHORIZED","message":"invalid token claims"}`)
				return
			}

			// Store under both custom key and default key so retrieval always works
			ctx := context.WithValue(r.Context(), jwtClaimsContextKey{cfg.ContextKey}, claims)
			ctx = context.WithValue(ctx, defaultClaimsContextKey{}, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// ClaimsFromContext retrieves JWT claims stored by the JWT middleware.
// It checks both default context key and the canonical "claims" key.
func ClaimsFromContext(ctx context.Context) (jwt.MapClaims, bool) {
	if claims, ok := ctx.Value(defaultClaimsContextKey{}).(jwt.MapClaims); ok {
		return claims, true
	}
	if claims, ok := ctx.Value(jwtClaimsContextKey{"claims"}).(jwt.MapClaims); ok {
		return claims, true
	}
	return nil, false
}

// ClaimsFromContextWithKey retrieves JWT claims using a custom context key.
func ClaimsFromContextWithKey(ctx context.Context, key string) (jwt.MapClaims, bool) {
	claims, ok := ctx.Value(jwtClaimsContextKey{key}).(jwt.MapClaims)
	return claims, ok
}
