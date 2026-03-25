package router_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dhawalhost/gokit/router"
)

func TestNewRouter(t *testing.T) {
	r := router.New()
	if r == nil {
		t.Fatal("expected non-nil router")
	}
	r.Get("/ping", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/ping", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestNewWithMiddleware(t *testing.T) {
	var called bool
	mw := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			next.ServeHTTP(w, r)
		})
	}
	r := router.NewWithMiddleware(mw)
	r.Get("/", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/", nil)
	r.ServeHTTP(w, req)
	if !called {
		t.Error("expected middleware to be called")
	}
}

// testService is a sample ServiceRouter implementation.
type testService struct{}

func (s *testService) Pattern() string { return "/api/v1" }
func (s *testService) Router() http.Handler {
	r := router.New()
	r.Get("/users", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	return r
}

func TestMount(t *testing.T) {
	mux := router.New()
	router.Mount(mux, &testService{})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/v1/users", nil)
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}
