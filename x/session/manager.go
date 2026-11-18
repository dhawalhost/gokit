package session

import (
	"context"
	"errors"
	"net/http"
	"time"
)

type contextKey struct{}

var sessionContextKey = contextKey{}

// Manager coordinates the lifecycle, cookie transmission, and persistence of HTTP sessions.
type Manager struct {
	config Config
}

// NewManager constructs a new session Manager with the provided Store and Options.
func NewManager(store Store, opts ...Option) (*Manager, error) {
	if store == nil {
		return nil, ErrNilStore
	}

	cfg := Config{
		Store: store,
		Cookie: CookieConfig{
			Name:     "session_id",
			Path:     "/",
			MaxAge:   24 * time.Hour,
			Secure:   false,
			HTTPOnly: true,
			SameSite: http.SameSiteLaxMode,
		},
		IdleTimeout:     30 * time.Minute,
		AbsoluteTimeout: 24 * time.Hour,
		AntiHijacking:   true,
		FingerprintFunc: DefaultFingerprint,
	}

	for _, opt := range opts {
		opt(&cfg)
	}

	return &Manager{
		config: cfg,
	}, nil
}

// FromContext extracts the active Session from the request context, returning nil if not present.
func FromContext(ctx context.Context) *Session {
	if ctx == nil {
		return nil
	}
	s, ok := ctx.Value(sessionContextKey).(*Session)
	if !ok {
		return nil
	}
	return s
}

// WithSession injects a session into the given context.
func WithSession(ctx context.Context, s *Session) context.Context {
	return context.WithValue(ctx, sessionContextKey, s)
}

// Load inspects the HTTP request for a session cookie and retrieves the corresponding session from storage.
// If the cookie is absent or expired, a new uncommitted Session is initialized.
func (m *Manager) Load(r *http.Request) (*Session, error) {
	fingerprint := ""
	if m.config.FingerprintFunc != nil {
		fingerprint = m.config.FingerprintFunc(r)
	}

	cookie, err := r.Cookie(m.config.Cookie.Name)
	if err != nil || cookie == nil || cookie.Value == "" {
		return NewSession(fingerprint), nil
	}

	data, err := m.config.Store.Load(r.Context(), cookie.Value)
	if err != nil {
		if errors.Is(err, ErrSessionNotFound) || errors.Is(err, ErrInvalidSession) {
			return NewSession(fingerprint), nil
		}
		return nil, err
	}

	now := time.Now().UTC()

	// 1. Check absolute expiration
	if m.config.AbsoluteTimeout > 0 && now.Sub(data.CreatedAt) > m.config.AbsoluteTimeout {
		_ = m.config.Store.Destroy(r.Context(), cookie.Value)
		return NewSession(fingerprint), nil
	}

	// 2. Check idle expiration
	if m.config.IdleTimeout > 0 && now.Sub(data.AccessedAt) > m.config.IdleTimeout {
		_ = m.config.Store.Destroy(r.Context(), cookie.Value)
		return NewSession(fingerprint), nil
	}

	// 3. Anti-hijacking fingerprint verification
	if m.config.AntiHijacking && data.Fingerprint != "" && fingerprint != "" {
		if data.Fingerprint != fingerprint {
			return nil, ErrHijackingDetected
		}
	}

	return &Session{
		id:   cookie.Value,
		data: *data,
	}, nil
}

// Commit persists changes made to the session and writes the corresponding Set-Cookie header.
func (m *Manager) Commit(w http.ResponseWriter, s *Session) error {
	if s == nil {
		return nil
	}

	ctx := context.Background()

	s.mu.Lock()
	defer s.mu.Unlock()

	// 1. Handled destroyed session
	if s.isDestroyed {
		_ = m.config.Store.Destroy(ctx, s.id)
		//nolint:gosec // #nosec G124 -- cookie security attributes configured via CookieConfig with secure defaults
		http.SetCookie(w, &http.Cookie{
			Name:        m.config.Cookie.Name,
			Value:       "",
			Path:        m.config.Cookie.Path,
			Domain:      m.config.Cookie.Domain,
			MaxAge:      -1,
			Secure:      m.config.Cookie.Secure,
			HttpOnly:    m.config.Cookie.HTTPOnly,
			SameSite:    m.config.Cookie.SameSite,
			Partitioned: m.config.Cookie.Partitioned,
			Expires:     time.Unix(0, 0),
		})
		return nil
	}

	// 2. Handle session regeneration (re-keying to prevent session fixation)
	if s.regenerate {
		_ = m.config.Store.Destroy(ctx, s.id)
		s.id = generateSessionID()
		s.regenerate = false
		s.isModified = true
	}

	now := time.Now().UTC()
	s.data.AccessedAt = now

	// 3. Persist modified or new session
	if s.isNew || s.isModified {
		if err := m.config.Store.Save(ctx, s.id, &s.data, m.config.IdleTimeout); err != nil {
			return err
		}
		s.isNew = false
		s.isModified = false

		//nolint:gosec // #nosec G124 -- cookie security attributes configured via CookieConfig with secure defaults
		http.SetCookie(w, &http.Cookie{
			Name:        m.config.Cookie.Name,
			Value:       s.id,
			Path:        m.config.Cookie.Path,
			Domain:      m.config.Cookie.Domain,
			MaxAge:      int(m.config.IdleTimeout.Seconds()),
			Secure:      m.config.Cookie.Secure,
			HttpOnly:    m.config.Cookie.HTTPOnly,
			SameSite:    m.config.Cookie.SameSite,
			Partitioned: m.config.Cookie.Partitioned,
		})
		return nil
	}

	// 4. Sliding TTL touch on unmodified sessions
	return m.config.Store.Touch(ctx, s.id, m.config.IdleTimeout)
}

// Middleware returns an HTTP middleware that transparently loads and commits sessions.
func (m *Manager) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sess, err := m.Load(r)
			if err != nil {
				if errors.Is(err, ErrHijackingDetected) {
					http.Error(w, "Forbidden: session fingerprint mismatch", http.StatusForbidden)
					return
				}
				sess = NewSession(m.config.FingerprintFunc(r))
			}

			ctx := WithSession(r.Context(), sess)
			rw := &sessionResponseWriter{
				ResponseWriter: w,
				mgr:            m,
				session:        sess,
			}

			defer func() {
				if !rw.committed {
					rw.committed = true
					_ = m.Commit(w, sess)
				}
			}()

			next.ServeHTTP(rw, r.WithContext(ctx))
		})
	}
}

type sessionResponseWriter struct {
	http.ResponseWriter
	mgr       *Manager
	session   *Session
	committed bool
}

func (rw *sessionResponseWriter) WriteHeader(code int) {
	if !rw.committed {
		rw.committed = true
		_ = rw.mgr.Commit(rw.ResponseWriter, rw.session)
	}
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *sessionResponseWriter) Write(b []byte) (int, error) {
	if !rw.committed {
		rw.committed = true
		_ = rw.mgr.Commit(rw.ResponseWriter, rw.session)
	}
	return rw.ResponseWriter.Write(b)
}
