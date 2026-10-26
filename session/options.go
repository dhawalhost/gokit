package session

import (
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"strings"
	"time"
)

// CookieConfig controls the security attributes of the session cookie.
type CookieConfig struct {
	Name        string
	Domain      string
	Path        string
	MaxAge      time.Duration
	Secure      bool
	HTTPOnly    bool
	SameSite    http.SameSite
	Partitioned bool
}

// Config defines the complete operational parameters for the session Manager.
type Config struct {
	Store           Store
	Cookie          CookieConfig
	IdleTimeout     time.Duration
	AbsoluteTimeout time.Duration
	AntiHijacking   bool
	FingerprintFunc func(r *http.Request) string
}

// Option configures a session Manager.
type Option func(*Config)

// WithCookieName overrides the session cookie name (default: "session_id").
func WithCookieName(name string) Option {
	return func(c *Config) {
		if name != "" {
			c.Cookie.Name = name
		}
	}
}

// WithCookieDomain sets the Domain attribute for the session cookie.
func WithCookieDomain(domain string) Option {
	return func(c *Config) {
		c.Cookie.Domain = domain
	}
}

// WithCookiePath sets the Path attribute for the session cookie (default: "/").
func WithCookiePath(path string) Option {
	return func(c *Config) {
		if path != "" {
			c.Cookie.Path = path
		}
	}
}

// WithCookieSecure sets whether the cookie requires HTTPS.
func WithCookieSecure(secure bool) Option {
	return func(c *Config) {
		c.Cookie.Secure = secure
	}
}

// WithCookieHTTPOnly sets the HttpOnly flag on the cookie (default: true).
func WithCookieHTTPOnly(httpOnly bool) Option {
	return func(c *Config) {
		c.Cookie.HTTPOnly = httpOnly
	}
}

// WithCookieSameSite configures the SameSite attribute (default: SameSiteLaxMode).
func WithCookieSameSite(sameSite http.SameSite) Option {
	return func(c *Config) {
		c.Cookie.SameSite = sameSite
	}
}

// WithCookiePartitioned sets the Partitioned (CHIPS) attribute on the cookie.
func WithCookiePartitioned(partitioned bool) Option {
	return func(c *Config) {
		c.Cookie.Partitioned = partitioned
	}
}

// WithIdleTimeout sets the sliding inactivity timeout for sessions (default: 30m).
func WithIdleTimeout(d time.Duration) Option {
	return func(c *Config) {
		if d > 0 {
			c.IdleTimeout = d
		}
	}
}

// WithAbsoluteTimeout sets the hard maximum lifetime from creation (default: 24h).
func WithAbsoluteTimeout(d time.Duration) Option {
	return func(c *Config) {
		if d > 0 {
			c.AbsoluteTimeout = d
		}
	}
}

// WithAntiHijacking enables client fingerprint validation across requests.
func WithAntiHijacking(enabled bool) Option {
	return func(c *Config) {
		c.AntiHijacking = enabled
	}
}

// WithFingerprintFunc customizes the client fingerprint generator.
func WithFingerprintFunc(fn func(r *http.Request) string) Option {
	return func(c *Config) {
		if fn != nil {
			c.FingerprintFunc = fn
		}
	}
}

// DefaultFingerprint generates a SHA-256 fingerprint from the client IP subnet (/24 for IPv4, /48 for IPv6)
// and User-Agent header, balancing roaming tolerance with session hijacking defense.
func DefaultFingerprint(r *http.Request) string {
	ipStr := r.Header.Get("X-Forwarded-For")
	if ipStr == "" {
		ipStr = r.RemoteAddr
	}
	if host, _, err := net.SplitHostPort(ipStr); err == nil {
		ipStr = host
	}
	if comma := strings.Index(ipStr, ","); comma != -1 {
		ipStr = strings.TrimSpace(ipStr[:comma])
	}

	ip := net.ParseIP(ipStr)
	var maskedIP string
	if ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			mask := net.CIDRMask(24, 32)
			maskedIP = ip4.Mask(mask).String()
		} else {
			mask := net.CIDRMask(48, 128)
			maskedIP = ip.Mask(mask).String()
		}
	} else {
		maskedIP = ipStr
	}

	ua := r.UserAgent()
	h := sha256.New()
	h.Write([]byte(maskedIP + "|" + ua))
	return hex.EncodeToString(h.Sum(nil))
}
