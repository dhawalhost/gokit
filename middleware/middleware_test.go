package middleware_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"go.uber.org/zap"

	"github.com/dhawalhost/gokit/middleware"
	"github.com/dhawalhost/gokit/ratelimit"
)

var noopHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
})

func makeHS256Token(t *testing.T, secret string, expOffset time.Duration) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": "user-1",
		"exp": time.Now().Add(expOffset).Unix(),
	})
	str, err := token.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return str
}

type stubStore struct {
	allowed bool
}

func (s *stubStore) Allow(_ context.Context, _ string, _ float64, _ int) (bool, error) {
	return s.allowed, nil
}

func TestRequestID_TableDriven(t *testing.T) {
	tests := []struct {
		name          string
		incomingID    string
		expectSame    bool
		checkContext  bool
		expectContext string
	}{
		{
			name:       "missing header generates random valid Request-ID",
			incomingID: "",
			expectSame: false,
		},
		{
			name:          "valid incoming ID is preserved in response and context",
			incomingID:    "req-12345-abcdef",
			expectSame:    true,
			checkContext:  true,
			expectContext: "req-12345-abcdef",
		},
		{
			name:       "insecure characters in ID are sanitized and replaced",
			incomingID: "<invalid_id_with_special_chars!>",
			expectSame: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var capturedID string
			handler := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				capturedID = middleware.RequestIDFromContext(r.Context())
			})

			w := httptest.NewRecorder()
			r, _ := http.NewRequest(http.MethodGet, "/", nil)
			if tc.incomingID != "" {
				r.Header.Set("X-Request-ID", tc.incomingID)
			}

			middleware.RequestID()(handler).ServeHTTP(w, r)
			outID := w.Header().Get("X-Request-ID")
			if outID == "" {
				t.Fatal("expected non-empty X-Request-ID header")
			}

			if tc.expectSame && outID != tc.incomingID {
				t.Errorf("expected outID %q, got %q", tc.incomingID, outID)
			}
			if !tc.expectSame && tc.incomingID != "" && outID == tc.incomingID {
				t.Errorf("expected sanitized ID to differ from insecure input %q", tc.incomingID)
			}
			if tc.checkContext && capturedID != tc.expectContext {
				t.Errorf("expected context ID %q, got %q", tc.expectContext, capturedID)
			}
		})
	}

	// Context helper on empty request
	rEmpty, _ := http.NewRequest(http.MethodGet, "/", nil)
	if id := middleware.RequestIDFromContext(rEmpty.Context()); id != "" {
		t.Errorf("expected empty from context, got %q", id)
	}
}

func TestSecureHeaders_TableDriven(t *testing.T) {
	tests := []struct {
		name       string
		headerName string
	}{
		{name: "X-Content-Type-Options is set", headerName: "X-Content-Type-Options"},
		{name: "X-Frame-Options is set", headerName: "X-Frame-Options"},
		{name: "X-XSS-Protection is set", headerName: "X-XSS-Protection"},
		{name: "Referrer-Policy is set", headerName: "Referrer-Policy"},
		{name: "Content-Security-Policy is set", headerName: "Content-Security-Policy"},
		{name: "Strict-Transport-Security is set", headerName: "Strict-Transport-Security"},
		{name: "Permissions-Policy is set", headerName: "Permissions-Policy"},
	}

	w := httptest.NewRecorder()
	r, _ := http.NewRequest(http.MethodGet, "/", nil)
	middleware.SecureHeaders()(noopHandler).ServeHTTP(w, r)

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if w.Header().Get(tc.headerName) == "" {
				t.Errorf("expected header %q to be set", tc.headerName)
			}
		})
	}
}

func TestLoggerMiddleware_TableDriven(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		wantStatus int
	}{
		{
			name:       "regular endpoint passes through with 200 OK",
			path:       "/api/v1/resource",
			wantStatus: http.StatusOK,
		},
		{
			name:       "health probe route passes through cleanly",
			path:       "/health/live",
			wantStatus: http.StatusOK,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			log := zap.NewNop()
			w := httptest.NewRecorder()
			r, _ := http.NewRequest(http.MethodGet, tc.path, nil)
			middleware.Logger(log)(noopHandler).ServeHTTP(w, r)

			if w.Code != tc.wantStatus {
				t.Errorf("expected status %d, got %d", tc.wantStatus, w.Code)
			}
		})
	}
}

func TestRecoveryMiddleware_TableDriven(t *testing.T) {
	tests := []struct {
		name       string
		handler    http.Handler
		wantStatus int
	}{
		{
			name: "panicking handler recovers and returns 500 Internal Server Error",
			handler: http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
				panic("unexpected nil pointer or failure")
			}),
			wantStatus: http.StatusInternalServerError,
		},
		{
			name:       "non-panicking handler passes through with 200 OK",
			handler:    noopHandler,
			wantStatus: http.StatusOK,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			log := zap.NewNop()
			w := httptest.NewRecorder()
			r, _ := http.NewRequest(http.MethodGet, "/", nil)
			middleware.Recovery(log)(tc.handler).ServeHTTP(w, r)

			if w.Code != tc.wantStatus {
				t.Fatalf("expected status %d, got %d", tc.wantStatus, w.Code)
			}
		})
	}
}

func TestTenantID_TableDriven(t *testing.T) {
	tests := []struct {
		name         string
		headerVal    string
		expectFound  bool
		expectTenant string
	}{
		{
			name:         "X-Tenant-ID header extracts into context successfully",
			headerVal:    "tenant-abc",
			expectFound:  true,
			expectTenant: "tenant-abc",
		},
		{
			name:        "missing X-Tenant-ID header returns found=false",
			headerVal:   "",
			expectFound: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got string
			var ok bool
			handler := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				got, ok = middleware.TenantIDFromContext(r.Context())
			})

			w := httptest.NewRecorder()
			r, _ := http.NewRequest(http.MethodGet, "/", nil)
			if tc.headerVal != "" {
				r.Header.Set("X-Tenant-ID", tc.headerVal)
			}

			middleware.TenantID()(handler).ServeHTTP(w, r)
			if ok != tc.expectFound {
				t.Fatalf("expectFound=%v, got ok=%v", tc.expectFound, ok)
			}
			if tc.expectFound && got != tc.expectTenant {
				t.Errorf("expected tenant %q, got %q", tc.expectTenant, got)
			}
		})
	}
}

func TestCORS_TableDriven(t *testing.T) {
	tests := []struct {
		name       string
		middleware func(http.Handler) http.Handler
		origin     string
		method     string
		check      func(t *testing.T, w *httptest.ResponseRecorder)
	}{
		{
			name:       "whitelisted origin sets Access-Control-Allow-Origin header",
			middleware: middleware.CORS([]string{"https://example.com"}),
			origin:     "https://example.com",
			method:     http.MethodOptions,
			check: func(t *testing.T, w *httptest.ResponseRecorder) {
				if w.Header().Get("Access-Control-Allow-Origin") == "" {
					t.Error("expected Access-Control-Allow-Origin to be set")
				}
			},
		},
		{
			name:       "CORSAllowAll permits any origin",
			middleware: middleware.CORSAllowAll(),
			origin:     "https://anything.example.com",
			method:     http.MethodOptions,
			check: func(t *testing.T, w *httptest.ResponseRecorder) {
				if w.Header().Get("Access-Control-Allow-Origin") == "" {
					t.Error("expected Access-Control-Allow-Origin to be set by CORSAllowAll")
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r, _ := http.NewRequest(tc.method, "/", nil)
			r.Header.Set("Origin", tc.origin)
			r.Header.Set("Access-Control-Request-Method", "GET")

			tc.middleware(noopHandler).ServeHTTP(w, r)
			tc.check(t, w)
		})
	}
}

func TestAPIKey_TableDriven(t *testing.T) {
	tests := []struct {
		name       string
		apiKey     string
		validator  func(key string) (bool, error)
		wantStatus int
	}{
		{
			name:       "valid API key passes with 200 OK",
			apiKey:     "valid-key",
			validator:  func(key string) (bool, error) { return key == "valid-key", nil },
			wantStatus: http.StatusOK,
		},
		{
			name:       "missing API key returns 401 Unauthorized",
			apiKey:     "",
			validator:  func(_ string) (bool, error) { return true, nil },
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "invalid API key returns 401 Unauthorized",
			apiKey:     "bad-key",
			validator:  func(_ string) (bool, error) { return false, nil },
			wantStatus: http.StatusUnauthorized,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := middleware.APIKeyConfig{Validate: tc.validator}
			w := httptest.NewRecorder()
			r, _ := http.NewRequest(http.MethodGet, "/", nil)
			if tc.apiKey != "" {
				r.Header.Set("X-API-Key", tc.apiKey)
			}

			middleware.APIKey(cfg)(noopHandler).ServeHTTP(w, r)
			if w.Code != tc.wantStatus {
				t.Fatalf("expected status %d, got %d", tc.wantStatus, w.Code)
			}
		})
	}
}

func TestJWT_TableDriven(t *testing.T) {
	const secret = "test-secret"

	tests := []struct {
		name         string
		authHeader   string
		contextKey   string
		wantStatus   int
		verifyClaims bool
	}{
		{
			name:       "missing Authorization header returns 401 Unauthorized",
			authHeader: "",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "malformed token string returns 401 Unauthorized",
			authHeader: "Bearer not.a.valid.token",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "expired token returns 401 Unauthorized",
			authHeader: "Bearer " + makeHS256Token(t, secret, -time.Hour),
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:         "valid token populates claims in request context",
			authHeader:   "Bearer " + makeHS256Token(t, secret, time.Hour),
			wantStatus:   http.StatusOK,
			verifyClaims: true,
		},
		{
			name:         "valid token works with custom context key",
			authHeader:   "Bearer " + makeHS256Token(t, secret, time.Hour),
			contextKey:   "user_claims",
			wantStatus:   http.StatusOK,
			verifyClaims: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var captured bool
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.verifyClaims {
					claims, ok := middleware.ClaimsFromContext(r.Context())
					if !ok || claims["sub"] != "user-1" {
						t.Errorf("ClaimsFromContext failed: ok=%v, claims=%v", ok, claims)
					}
					captured = true
				}
				w.WriteHeader(http.StatusOK)
			})

			cfg := middleware.JWTConfig{
				SecretKey:  []byte(secret),
				ContextKey: tc.contextKey,
			}

			w := httptest.NewRecorder()
			r, _ := http.NewRequest(http.MethodGet, "/", nil)
			if tc.authHeader != "" {
				r.Header.Set("Authorization", tc.authHeader)
			}

			middleware.JWT(cfg)(handler).ServeHTTP(w, r)
			if w.Code != tc.wantStatus {
				t.Fatalf("expected status %d, got %d", tc.wantStatus, w.Code)
			}
			if tc.verifyClaims && !captured {
				t.Fatal("handler was not reached for claim verification")
			}
		})
	}

	// Context helper on empty request
	rEmpty, _ := http.NewRequest(http.MethodGet, "/", nil)
	if _, ok := middleware.ClaimsFromContext(rEmpty.Context()); ok {
		t.Error("expected ok=false when no claims in context")
	}
}

func TestRateLimit_TableDriven(t *testing.T) {
	tests := []struct {
		name  string
		rps   float64
		burst int
		store ratelimit.Store
		run   func(t *testing.T, mw func(http.Handler) http.Handler)
	}{
		{
			name:  "allows requests within burst rate",
			rps:   100,
			burst: 100,
			store: ratelimit.NewInMemoryStore(),
			run: func(t *testing.T, mw func(http.Handler) http.Handler) {
				w := httptest.NewRecorder()
				r, _ := http.NewRequest(http.MethodGet, "/", nil)
				r.RemoteAddr = "127.0.0.1:1234"
				mw(noopHandler).ServeHTTP(w, r)
				if w.Code != http.StatusOK {
					t.Errorf("expected 200, got %d", w.Code)
				}
			},
		},
		{
			name:  "blocks requests exceeding burst limit with 429 Too Many Requests",
			rps:   0.001,
			burst: 1,
			store: ratelimit.NewInMemoryStore(),
			run: func(t *testing.T, mw func(http.Handler) http.Handler) {
				r, _ := http.NewRequest(http.MethodGet, "/", nil)
				r.RemoteAddr = "10.0.0.1:9999"

				w1 := httptest.NewRecorder()
				mw(noopHandler).ServeHTTP(w1, r)

				w2 := httptest.NewRecorder()
				mw(noopHandler).ServeHTTP(w2, r)
				if w2.Code != http.StatusTooManyRequests {
					t.Errorf("expected 429, got %d", w2.Code)
				}
			},
		},
		{
			name:  "rate limit headers injected when blocked",
			rps:   1,
			burst: 5,
			store: &stubStore{allowed: false},
			run: func(t *testing.T, mw func(http.Handler) http.Handler) {
				w := httptest.NewRecorder()
				r, _ := http.NewRequest(http.MethodGet, "/", nil)
				mw(noopHandler).ServeHTTP(w, r)

				if w.Code != http.StatusTooManyRequests {
					t.Errorf("expected 429, got %d", w.Code)
				}
				if w.Header().Get("Retry-After") != "1" {
					t.Errorf("expected Retry-After header, got %s", w.Header().Get("Retry-After"))
				}
				if w.Header().Get("X-RateLimit-Limit") != "5" {
					t.Errorf("expected X-RateLimit-Limit 5, got %s", w.Header().Get("X-RateLimit-Limit"))
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := middleware.RateLimitConfig{
				RequestsPerSecond: tc.rps,
				Burst:             tc.burst,
				Store:             tc.store,
			}
			mw := middleware.RateLimit(cfg)
			tc.run(t, mw)
		})
	}
}

func TestIPKeyFunc_TableDriven(t *testing.T) {
	tests := []struct {
		name       string
		setupReq   func() *http.Request
		expectedIP string
	}{
		{
			name: "strips port from RemoteAddr",
			setupReq: func() *http.Request {
				r, _ := http.NewRequest(http.MethodGet, "/", nil)
				r.RemoteAddr = "192.168.1.1:4444"
				return r
			},
			expectedIP: "192.168.1.1",
		},
		{
			name: "extracts IP from X-Real-IP header",
			setupReq: func() *http.Request {
				r, _ := http.NewRequest(http.MethodGet, "/", nil)
				r.Header.Set("X-Real-IP", "1.2.3.4")
				return r
			},
			expectedIP: "1.2.3.4",
		},
		{
			name: "extracts first IP from comma-separated X-Forwarded-For header",
			setupReq: func() *http.Request {
				r, _ := http.NewRequest(http.MethodGet, "/", nil)
				r.Header.Set("X-Forwarded-For", "203.0.113.195, 70.41.3.18, 150.172.238.178")
				return r
			},
			expectedIP: "203.0.113.195",
		},
		{
			name: "rejects invalid IP in X-Forwarded-For and falls back to RemoteAddr",
			setupReq: func() *http.Request {
				r, _ := http.NewRequest(http.MethodGet, "/", nil)
				r.Header.Set("X-Forwarded-For", "<script>alert(1)</script>")
				r.RemoteAddr = "10.0.0.1:1234"
				return r
			},
			expectedIP: "10.0.0.1",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := tc.setupReq()
			got := middleware.IPKeyFunc(r)
			if got != tc.expectedIP {
				t.Errorf("expected IP %q, got %q", tc.expectedIP, got)
			}
		})
	}
}

func TestTimeout_TableDriven(t *testing.T) {
	tests := []struct {
		name       string
		timeout    time.Duration
		handler    http.Handler
		sleep      time.Duration
		wantStatus int
		verify     func(t *testing.T, w *httptest.ResponseRecorder)
	}{
		{
			name:       "handler completes within timeout and returns 200 OK",
			timeout:    5 * time.Second,
			handler:    noopHandler,
			wantStatus: http.StatusOK,
		},
		{
			name:    "handler body is written directly without buffering issues",
			timeout: 5 * time.Second,
			handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte("direct body"))
			}),
			wantStatus: http.StatusOK,
			verify: func(t *testing.T, w *httptest.ResponseRecorder) {
				if w.Body.String() != "direct body" {
					t.Errorf("expected 'direct body', got %q", w.Body.String())
				}
			},
		},
		{
			name:    "slow handler exceeding timeout returns 504 Gateway Timeout",
			timeout: 20 * time.Millisecond,
			handler: http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				<-r.Context().Done()
			}),
			sleep:      50 * time.Millisecond,
			wantStatus: http.StatusGatewayTimeout,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r, _ := http.NewRequest(http.MethodGet, "/", nil)
			middleware.Timeout(tc.timeout)(tc.handler).ServeHTTP(w, r)

			if tc.sleep > 0 {
				time.Sleep(tc.sleep)
			}

			if w.Code != tc.wantStatus {
				t.Fatalf("expected status %d, got %d", tc.wantStatus, w.Code)
			}
			if tc.verify != nil {
				tc.verify(t, w)
			}
		})
	}
}
