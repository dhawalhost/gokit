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
	// Algorithm is the expected signing algorithm (e.g. "HS256").
	Algorithm string
	// ContextKey is the context key under which claims are stored (defaults to "claims").
	ContextKey string
}

type jwtClaimsKey struct{ key string }

// JWT returns a middleware that validates Bearer tokens using HS256.
// On success the jwt.MapClaims are stored in the request context.
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
				http.Error(w, `{"code":"UNAUTHORIZED","message":"missing or invalid authorization header"}`, http.StatusUnauthorized)
				return
			}
			tokenStr := strings.TrimPrefix(authHeader, "Bearer ")
			token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) {
				if t.Method.Alg() != cfg.Algorithm {
					return nil, jwt.ErrSignatureInvalid
				}
				return cfg.SecretKey, nil
			})
			if err != nil || !token.Valid {
				http.Error(w, `{"code":"UNAUTHORIZED","message":"invalid token"}`, http.StatusUnauthorized)
				return
			}
			claims, ok := token.Claims.(jwt.MapClaims)
			if !ok {
				http.Error(w, `{"code":"UNAUTHORIZED","message":"invalid token claims"}`, http.StatusUnauthorized)
				return
			}
			ctx := context.WithValue(r.Context(), jwtClaimsKey{cfg.ContextKey}, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// ClaimsFromContext retrieves JWT claims stored by the JWT middleware.
func ClaimsFromContext(ctx context.Context) (jwt.MapClaims, bool) {
	claims, ok := ctx.Value(jwtClaimsKey{"claims"}).(jwt.MapClaims)
	return claims, ok
}
