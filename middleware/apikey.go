package middleware

import (
	"net/http"
)

// APIKeyConfig holds configuration for the API key middleware.
type APIKeyConfig struct {
	// Header is the HTTP header name to read the API key from (e.g. "X-API-Key").
	Header string
	// Validate is called with the raw key value; return (true, nil) to allow.
	Validate func(key string) (bool, error)
}

// APIKey returns a middleware that validates an API key present in the configured header.
func APIKey(cfg APIKeyConfig) func(http.Handler) http.Handler {
	if cfg.Header == "" {
		cfg.Header = "X-API-Key"
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := r.Header.Get(cfg.Header)
			if key == "" {
				http.Error(w, `{"code":"UNAUTHORIZED","message":"missing api key"}`, http.StatusUnauthorized)
				return
			}
			ok, err := cfg.Validate(key)
			if err != nil || !ok {
				http.Error(w, `{"code":"UNAUTHORIZED","message":"invalid api key"}`, http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
