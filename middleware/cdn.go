package middleware

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

// CacheControlConfig configures the CacheControl middleware.
type CacheControlConfig struct {
	// MaxAge defines the maximum duration an edge or browser may cache the response.
	MaxAge time.Duration
	// StaleWhileRevalidate specifies how long stale responses may be served while refreshing.
	StaleWhileRevalidate time.Duration
	// Public declares the response cacheable by shared/CDN caches.
	Public bool
	// Vary specifies request headers that create cache variants (e.g. Accept-Encoding).
	Vary []string
}

// CacheControl sets standardized Cache-Control and Vary headers for CDN edge caching.
// To prevent cache poisoning of personalized PII, if a request carries an Authorization
// or session cookie on a non-public route, it forces Cache-Control to 'private, no-store'.
func CacheControl(cfg CacheControlConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Protection against PII caching: if authorized and not explicitly declared public
			if !cfg.Public && (r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "") {
				w.Header().Set("Cache-Control", "private, no-store, no-cache, must-revalidate")
				next.ServeHTTP(w, r)
				return
			}

			parts := make([]string, 0, 4)
			if cfg.Public {
				parts = append(parts, "public")
			} else {
				parts = append(parts, "private")
			}

			if cfg.MaxAge > 0 {
				parts = append(parts, fmt.Sprintf("max-age=%d", int(cfg.MaxAge.Seconds())))
			}

			if cfg.StaleWhileRevalidate > 0 {
				parts = append(parts, fmt.Sprintf("stale-while-revalidate=%d", int(cfg.StaleWhileRevalidate.Seconds())))
			}

			if len(parts) > 0 {
				w.Header().Set("Cache-Control", strings.Join(parts, ", "))
			}

			if len(cfg.Vary) > 0 {
				w.Header().Set("Vary", strings.Join(cfg.Vary, ", "))
			}

			next.ServeHTTP(w, r)
		})
	}
}
