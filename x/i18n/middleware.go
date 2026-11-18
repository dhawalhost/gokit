package i18n

import (
	"net/http"
)

// MiddlewareOption configures the i18n HTTP middleware.
type MiddlewareOption func(*middlewareConfig)

type middlewareConfig struct {
	queryParam            string
	cookieName            string
	contentLanguageHeader bool
}

// WithQueryParam specifies the URL query parameter name to inspect for locale (default: "lang").
func WithQueryParam(param string) MiddlewareOption {
	return func(c *middlewareConfig) {
		c.queryParam = param
	}
}

// WithCookieName specifies the HTTP cookie name to inspect for locale (default: "locale").
func WithCookieName(name string) MiddlewareOption {
	return func(c *middlewareConfig) {
		c.cookieName = name
	}
}

// WithContentLanguageHeader configures whether to write the Content-Language response header.
func WithContentLanguageHeader(enabled bool) MiddlewareOption {
	return func(c *middlewareConfig) {
		c.contentLanguageHeader = enabled
	}
}

// Middleware returns an HTTP middleware that extracts the target locale from query parameters,
// cookies, or Accept-Language headers, constructs a Localizer, and attaches it to the request context.
func Middleware(bundle *Bundle, opts ...MiddlewareOption) func(http.Handler) http.Handler {
	cfg := middlewareConfig{
		queryParam:            "lang",
		cookieName:            "locale",
		contentLanguageHeader: true,
	}

	for _, opt := range opts {
		opt(&cfg)
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var requestedLocales []string

			// 1. Query parameter (highest precedence)
			if cfg.queryParam != "" {
				if q := r.URL.Query().Get(cfg.queryParam); q != "" {
					requestedLocales = append(requestedLocales, q)
				}
			}

			// 2. Cookie
			if cfg.cookieName != "" {
				if cookie, err := r.Cookie(cfg.cookieName); err == nil && cookie.Value != "" {
					requestedLocales = append(requestedLocales, cookie.Value)
				}
			}

			// 3. Accept-Language header
			if accept := r.Header.Get("Accept-Language"); accept != "" {
				requestedLocales = append(requestedLocales, ParseAcceptLanguage(accept)...)
			}

			localizer := bundle.NewLocalizer(requestedLocales...)
			ctx := WithLocalizer(r.Context(), localizer)

			if cfg.contentLanguageHeader {
				w.Header().Set("Content-Language", localizer.Locale().Tag)
			}

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
