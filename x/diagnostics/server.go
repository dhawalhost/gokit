package diagnostics

import (
	"crypto/subtle"
	"encoding/json"
	"net"
	"net/http"
	"net/http/pprof"
	"strings"
)

// ServerOption configures the protected diagnostic HTTP handler.
type ServerOption func(*serverConfig)

type serverConfig struct {
	bearerToken    string
	allowedSubnets []*net.IPNet
	pathPrefix     string
	watchdog       *MemoryWatchdog
}

// WithBearerToken requires an Authorization: Bearer <token> header to access diagnostic routes.
func WithBearerToken(token string) ServerOption {
	return func(c *serverConfig) {
		c.bearerToken = token
	}
}

// WithAllowedCIDRs restricts diagnostic endpoint access to requests originating from the specified CIDR blocks.
func WithAllowedCIDRs(cidrs ...string) ServerOption {
	return func(c *serverConfig) {
		for _, cidr := range cidrs {
			_, ipNet, err := net.ParseCIDR(cidr)
			if err == nil && ipNet != nil {
				c.allowedSubnets = append(c.allowedSubnets, ipNet)
			}
		}
	}
}

// WithPathPrefix overrides the root route prefix (default: "/debug").
func WithPathPrefix(prefix string) ServerOption {
	return func(c *serverConfig) {
		if prefix != "" {
			c.pathPrefix = strings.TrimSuffix(prefix, "/")
		}
	}
}

// WithWatchdog attaches a MemoryWatchdog to expose its status via /debug/diagnostics/summary.
func WithWatchdog(w *MemoryWatchdog) ServerOption {
	return func(c *serverConfig) {
		c.watchdog = w
	}
}

// NewHandler constructs an authenticated, subnet-protected http.Handler mounting pprof and diagnostic status routes.
func NewHandler(opts ...ServerOption) http.Handler {
	cfg := serverConfig{
		pathPrefix: "/debug",
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	mux := http.NewServeMux()
	prefix := cfg.pathPrefix

	// Mount pprof standard routes
	mux.HandleFunc(prefix+"/pprof/", pprof.Index)
	mux.HandleFunc(prefix+"/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc(prefix+"/pprof/profile", pprof.Profile)
	mux.HandleFunc(prefix+"/pprof/symbol", pprof.Symbol)
	mux.HandleFunc(prefix+"/pprof/trace", pprof.Trace)
	mux.HandleFunc(prefix+"/pprof/heap", pprof.Handler("heap").ServeHTTP)
	mux.HandleFunc(prefix+"/pprof/goroutine", pprof.Handler("goroutine").ServeHTTP)
	mux.HandleFunc(prefix+"/pprof/block", pprof.Handler("block").ServeHTTP)
	mux.HandleFunc(prefix+"/pprof/mutex", pprof.Handler("mutex").ServeHTTP)

	// Summary diagnostics route
	mux.HandleFunc(prefix+"/diagnostics/summary", func(w http.ResponseWriter, r *http.Request) {
		summary := ReadMemorySummary()
		goroutines := CaptureGoroutines()

		resp := map[string]any{
			"memory":          summary,
			"goroutine_count": goroutines.Count,
		}

		if cfg.watchdog != nil {
			breached, _, _ := cfg.watchdog.CheckNow()
			resp["watchdog"] = map[string]any{
				"breached": breached,
			}
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	// Wrap mux with authentication and CIDR allowlist enforcement
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 1. CIDR Subnet Allowlist Check
		if len(cfg.allowedSubnets) > 0 {
			clientIPStr := r.Header.Get("X-Forwarded-For")
			if clientIPStr == "" {
				clientIPStr = r.RemoteAddr
			}
			if host, _, err := net.SplitHostPort(clientIPStr); err == nil {
				clientIPStr = host
			}
			if comma := strings.Index(clientIPStr, ","); comma != -1 {
				clientIPStr = strings.TrimSpace(clientIPStr[:comma])
			}

			clientIP := net.ParseIP(clientIPStr)
			allowed := false
			if clientIP != nil {
				for _, subnet := range cfg.allowedSubnets {
					if subnet.Contains(clientIP) {
						allowed = true
						break
					}
				}
			}

			if !allowed {
				http.Error(w, ErrForbidden.Error(), http.StatusForbidden)
				return
			}
		}

		// 2. Bearer Token Authentication Check
		if cfg.bearerToken != "" {
			authHeader := r.Header.Get("Authorization")
			token := ""
			if strings.HasPrefix(authHeader, "Bearer ") {
				token = strings.TrimPrefix(authHeader, "Bearer ")
			} else {
				token = r.URL.Query().Get("token")
			}

			if token == "" || subtle.ConstantTimeCompare([]byte(token), []byte(cfg.bearerToken)) != 1 {
				w.Header().Set("WWW-Authenticate", `Bearer realm="diagnostics"`)
				http.Error(w, ErrUnauthorized.Error(), http.StatusUnauthorized)
				return
			}
		}

		mux.ServeHTTP(w, r)
	})
}
