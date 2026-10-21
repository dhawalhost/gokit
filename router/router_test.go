package router_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dhawalhost/gokit/router"
)

type testService struct{}

func (s *testService) Pattern() string { return "/api/v1" }
func (s *testService) Router() http.Handler {
	r := router.New()
	r.Get("/users", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	return r
}

func TestRouter_TableDriven(t *testing.T) {
	tests := []struct {
		name       string
		setup      func() http.Handler
		requestURL string
		wantStatus int
	}{
		{
			name: "basic GET route registration and matching",
			setup: func() http.Handler {
				r := router.New()
				r.Get("/ping", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusOK)
				}))
				return r
			},
			requestURL: "/ping",
			wantStatus: http.StatusOK,
		},
		{
			name: "router with middleware executes middleware stack",
			setup: func() http.Handler {
				var called bool
				mw := func(next http.Handler) http.Handler {
					return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						called = true
						next.ServeHTTP(w, r)
					})
				}
				r := router.NewWithMiddleware(mw)
				r.Get("/", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					if !called {
						w.WriteHeader(http.StatusInternalServerError)
						return
					}
					w.WriteHeader(http.StatusOK)
				}))
				return r
			},
			requestURL: "/",
			wantStatus: http.StatusOK,
		},
		{
			name: "mounted service router routes subpaths correctly",
			setup: func() http.Handler {
				mux := router.New()
				router.Mount(mux, &testService{})
				return mux
			},
			requestURL: "/api/v1/users",
			wantStatus: http.StatusOK,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.setup()
			w := httptest.NewRecorder()
			req, _ := http.NewRequest(http.MethodGet, tc.requestURL, nil)
			h.ServeHTTP(w, req)

			if w.Code != tc.wantStatus {
				t.Fatalf("expected status %d, got %d", tc.wantStatus, w.Code)
			}
		})
	}
}
