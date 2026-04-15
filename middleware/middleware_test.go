package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/dhawalhost/gokit/middleware"
	"github.com/dhawalhost/gokit/ratelimit"
	"go.uber.org/zap"
)

var noopHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
})

func TestRequestIDGeneratesID(t *testing.T) {
	w := httptest.NewRecorder()
	r, _ := http.NewRequest(http.MethodGet, "/", nil)
	middleware.RequestID()(noopHandler).ServeHTTP(w, r)

	id := w.Header().Get("X-Request-ID")
	if id == "" {
		t.Fatal("expected X-Request-ID header to be set")
	}
}

func TestRequestIDReusesExistingID(t *testing.T) {
	w := httptest.NewRecorder()
	r, _ := http.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-Request-ID", "my-id")
	middleware.RequestID()(noopHandler).ServeHTTP(w, r)

	if w.Header().Get("X-Request-ID") != "my-id" {
		t.Error("expected existing X-Request-ID to be preserved")
	}
}

func TestRequestIDFromContext(t *testing.T) {
	var capturedID string
	handler := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		capturedID = middleware.RequestIDFromContext(r.Context())
	})
	w := httptest.NewRecorder()
	r, _ := http.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-Request-ID", "ctx-id")
	middleware.RequestID()(handler).ServeHTTP(w, r)

	if capturedID != "ctx-id" {
		t.Errorf("expected ctx-id, got %q", capturedID)
	}
}

func TestRequestIDFromContextEmpty(t *testing.T) {
	r, _ := http.NewRequest(http.MethodGet, "/", nil)
	id := middleware.RequestIDFromContext(r.Context())
	if id != "" {
		t.Errorf("expected empty, got %q", id)
	}
}

func TestSecureHeaders(t *testing.T) {
	w := httptest.NewRecorder()
	r, _ := http.NewRequest(http.MethodGet, "/", nil)
	middleware.SecureHeaders()(noopHandler).ServeHTTP(w, r)

	headers := []string{
		"X-Content-Type-Options",
		"X-Frame-Options",
		"X-XSS-Protection",
		"Referrer-Policy",
		"Content-Security-Policy",
		"Strict-Transport-Security",
	}
	for _, h := range headers {
		if w.Header().Get(h) == "" {
			t.Errorf("expected header %q to be set", h)
		}
	}
}

func TestLoggerMiddlewarePassesThrough(t *testing.T) {
	log := zap.NewNop()
	w := httptest.NewRecorder()
	r, _ := http.NewRequest(http.MethodGet, "/test", nil)
	middleware.Logger(log)(noopHandler).ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestLoggerMiddlewareSkipsHealth(t *testing.T) {
	log := zap.NewNop()
	w := httptest.NewRecorder()
	r, _ := http.NewRequest(http.MethodGet, "/health/live", nil)
	middleware.Logger(log)(noopHandler).ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestRecoveryMiddleware(t *testing.T) {
	panicHandler := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		panic("test panic")
	})
	log := zap.NewNop()
	w := httptest.NewRecorder()
	r, _ := http.NewRequest(http.MethodGet, "/", nil)
	middleware.Recovery(log)(panicHandler).ServeHTTP(w, r)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 after panic, got %d", w.Code)
	}
}

func TestTimeoutMiddlewarePassesThrough(t *testing.T) {
	w := httptest.NewRecorder()
	r, _ := http.NewRequest(http.MethodGet, "/", nil)
	middleware.Timeout(5*time.Second)(noopHandler).ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestTenantIDMiddleware(t *testing.T) {
	var got string
	var ok bool
	handler := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got, ok = middleware.TenantIDFromContext(r.Context())
	})
	w := httptest.NewRecorder()
	r, _ := http.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-Tenant-ID", "tenant-abc")
	middleware.TenantID()(handler).ServeHTTP(w, r)
	if !ok || got != "tenant-abc" {
		t.Errorf("expected tenant-abc, got %q ok=%v", got, ok)
	}
}

func TestTenantIDEmpty(t *testing.T) {
	var ok bool
	handler := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, ok = middleware.TenantIDFromContext(r.Context())
	})
	w := httptest.NewRecorder()
	r, _ := http.NewRequest(http.MethodGet, "/", nil)
	middleware.TenantID()(handler).ServeHTTP(w, r)
	if ok {
		t.Error("expected ok=false when no tenant header")
	}
}

func TestCORSMiddleware(t *testing.T) {
	w := httptest.NewRecorder()
	r, _ := http.NewRequest(http.MethodOptions, "/", nil)
	r.Header.Set("Origin", "https://example.com")
	r.Header.Set("Access-Control-Request-Method", "GET")
	middleware.CORS([]string{"https://example.com"})(noopHandler).ServeHTTP(w, r)
	if w.Header().Get("Access-Control-Allow-Origin") == "" {
		t.Error("expected Access-Control-Allow-Origin to be set")
	}
}

func TestAPIKeyMiddlewareValid(t *testing.T) {
	cfg := middleware.APIKeyConfig{
		Validate: func(key string) (bool, error) { return key == "valid-key", nil },
	}
	w := httptest.NewRecorder()
	r, _ := http.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-API-Key", "valid-key")
	middleware.APIKey(cfg)(noopHandler).ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestAPIKeyMiddlewareMissing(t *testing.T) {
	cfg := middleware.APIKeyConfig{
		Validate: func(key string) (bool, error) { return true, nil },
	}
	w := httptest.NewRecorder()
	r, _ := http.NewRequest(http.MethodGet, "/", nil)
	middleware.APIKey(cfg)(noopHandler).ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestAPIKeyMiddlewareInvalid(t *testing.T) {
	cfg := middleware.APIKeyConfig{
		Validate: func(key string) (bool, error) { return false, nil },
	}
	w := httptest.NewRecorder()
	r, _ := http.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-API-Key", "bad-key")
	middleware.APIKey(cfg)(noopHandler).ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestJWTMiddlewareMissingHeader(t *testing.T) {
	cfg := middleware.JWTConfig{SecretKey: []byte("secret")}
	w := httptest.NewRecorder()
	r, _ := http.NewRequest(http.MethodGet, "/", nil)
	middleware.JWT(cfg)(noopHandler).ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestJWTMiddlewareInvalidToken(t *testing.T) {
	cfg := middleware.JWTConfig{SecretKey: []byte("secret")}
	w := httptest.NewRecorder()
	r, _ := http.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Authorization", "Bearer not.a.valid.token")
	middleware.JWT(cfg)(noopHandler).ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestRateLimitMiddlewareAllows(t *testing.T) {
	cfg := middleware.RateLimitConfig{
		RequestsPerSecond: 100,
		Burst:             100,
		Store:             ratelimit.NewInMemoryStore(),
	}
	w := httptest.NewRecorder()
	r, _ := http.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "127.0.0.1:1234"
	middleware.RateLimit(cfg)(noopHandler).ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestRateLimitMiddlewareBlocks(t *testing.T) {
	cfg := middleware.RateLimitConfig{
		RequestsPerSecond: 0.001,
		Burst:             1,
		Store:             ratelimit.NewInMemoryStore(),
	}
	handler := middleware.RateLimit(cfg)(noopHandler)
	r, _ := http.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.1:9999"

	w1 := httptest.NewRecorder()
	handler.ServeHTTP(w1, r)

	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, r)
	if w2.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d", w2.Code)
	}
}

func TestIPKeyFuncFromRemoteAddr(t *testing.T) {
	r, _ := http.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "192.168.1.1:4444"
	got := middleware.IPKeyFunc(r)
	if got != "192.168.1.1:4444" {
		t.Errorf("expected '192.168.1.1:4444', got %q", got)
	}
}

func TestIPKeyFuncFromRealIP(t *testing.T) {
	r, _ := http.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-Real-IP", "1.2.3.4")
	got := middleware.IPKeyFunc(r)
	if got != "1.2.3.4" {
		t.Errorf("expected '1.2.3.4', got %q", got)
	}
}

func TestIPKeyFuncFromForwardedFor(t *testing.T) {
	r, _ := http.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-Forwarded-For", "5.6.7.8")
	got := middleware.IPKeyFunc(r)
	if got != "5.6.7.8" {
		t.Errorf("expected '5.6.7.8', got %q", got)
	}
}

func makeHS256Token(t *testing.T, secret string) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": "user-1",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	str, err := token.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return str
}

func TestClaimsFromContext(t *testing.T) {
	const secret = "test-secret"
	var captured bool

	handler := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		claims, ok := middleware.ClaimsFromContext(r.Context())
		if !ok {
			t.Error("expected claims in context")
			return
		}
		if claims["sub"] != "user-1" {
			t.Errorf("expected sub=user-1, got %v", claims["sub"])
		}
		captured = true
	})

	cfg := middleware.JWTConfig{SecretKey: []byte(secret)}
	wrapped := middleware.JWT(cfg)(handler)

	w := httptest.NewRecorder()
	r, _ := http.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Authorization", "Bearer "+makeHS256Token(t, secret))
	wrapped.ServeHTTP(w, r)

	if !captured {
		t.Fatal("handler was not reached")
	}
}

func TestClaimsFromContextMissing(t *testing.T) {
	r, _ := http.NewRequest(http.MethodGet, "/", nil)
	_, ok := middleware.ClaimsFromContext(r.Context())
	if ok {
		t.Error("expected ok=false when no claims in context")
	}
}

func TestCORSAllowAll(t *testing.T) {
	w := httptest.NewRecorder()
	r, _ := http.NewRequest(http.MethodOptions, "/", nil)
	r.Header.Set("Origin", "https://anything.example.com")
	r.Header.Set("Access-Control-Request-Method", "DELETE")
	middleware.CORSAllowAll()(noopHandler).ServeHTTP(w, r)

	if w.Header().Get("Access-Control-Allow-Origin") == "" {
		t.Error("expected Access-Control-Allow-Origin to be set by CORSAllowAll")
	}
}

func TestTimeoutMiddlewareWritesBodyDirectly(t *testing.T) {
	bodyHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("direct body"))
	})
	w := httptest.NewRecorder()
	r, _ := http.NewRequest(http.MethodGet, "/", nil)
	middleware.Timeout(5*time.Second)(bodyHandler).ServeHTTP(w, r)

	if w.Body.String() != "direct body" {
		t.Errorf("expected 'direct body', got %q", w.Body.String())
	}
}

func TestTimeoutMiddlewareFires(t *testing.T) {
	slowHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	})
	w := httptest.NewRecorder()
	r, _ := http.NewRequest(http.MethodGet, "/", nil)
	middleware.Timeout(20*time.Millisecond)(slowHandler).ServeHTTP(w, r)

	time.Sleep(50 * time.Millisecond)

	if w.Code != http.StatusGatewayTimeout {
		t.Errorf("expected 504 Gateway Timeout, got %d", w.Code)
	}
}
