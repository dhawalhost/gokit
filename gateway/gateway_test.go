package gateway_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dhawalhost/gokit/gateway"
)

func TestBalancers(t *testing.T) {
	u1, _ := url.Parse("http://backend-1:8080")
	u2, _ := url.Parse("http://backend-2:8080")
	u3, _ := url.Parse("http://backend-3:8080")

	t1 := gateway.NewTarget(u1)
	t2 := gateway.NewTarget(u2)
	t3 := gateway.NewTarget(u3)

	t.Run("RoundRobinBalancer", func(t *testing.T) {
		rr := gateway.NewRoundRobinBalancer()

		if _, err := rr.Next(nil); !errors.Is(err, gateway.ErrNoHealthyTargets) {
			t.Errorf("expected ErrNoHealthyTargets for nil targets, got: %v", err)
		}

		targets := []*gateway.Target{t1, t2, t3}
		got1, _ := rr.Next(targets)
		got2, _ := rr.Next(targets)
		got3, _ := rr.Next(targets)
		got4, _ := rr.Next(targets)

		if got1 != t1 || got2 != t2 || got3 != t3 || got4 != t1 {
			t.Errorf("unexpected round-robin sequence")
		}

		// Mark t2 as dead
		t2.SetAlive(false)
		if t2.IsAlive() {
			t.Errorf("expected t2 to be dead")
		}

		deadNext1, _ := rr.Next(targets)
		deadNext2, _ := rr.Next(targets)
		if deadNext1 == t2 || deadNext2 == t2 {
			t.Errorf("expected dead target to be skipped")
		}

		t2.SetAlive(true)
	})

	t.Run("LeastConnBalancer", func(t *testing.T) {
		lc := gateway.NewLeastConnBalancer()

		if _, err := lc.Next(nil); !errors.Is(err, gateway.ErrNoHealthyTargets) {
			t.Errorf("expected ErrNoHealthyTargets for nil targets, got: %v", err)
		}

		t1.ActiveConns.Store(10)
		t2.ActiveConns.Store(2)
		t3.ActiveConns.Store(5)

		targets := []*gateway.Target{t1, t2, t3}
		best, err := lc.Next(targets)
		if err != nil || best != t2 {
			t.Errorf("expected t2 with 2 conns, got: %v (%v)", best, err)
		}

		// Increase t2 conns
		t2.ActiveConns.Store(20)
		best2, _ := lc.Next(targets)
		if best2 != t3 {
			t.Errorf("expected t3 with 5 conns, got: %v", best2)
		}
	})

	t.Run("RandomBalancer", func(t *testing.T) {
		rb := gateway.NewRandomBalancer()

		if _, err := rb.Next(nil); !errors.Is(err, gateway.ErrNoHealthyTargets) {
			t.Errorf("expected ErrNoHealthyTargets for nil targets, got: %v", err)
		}

		targets := []*gateway.Target{t1, t2}
		chosen, err := rb.Next(targets)
		if err != nil || (chosen != t1 && chosen != t2) {
			t.Errorf("unexpected random target: %v (%v)", chosen, err)
		}
	})
}

func TestProxy_RoutingAndForwarding(t *testing.T) {
	// Backend 1
	var backend1Calls atomic.Int64
	srv1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backend1Calls.Add(1)
		w.Header().Set("X-Backend", "srv1")
		if r.URL.Path == "/profile" && r.Header.Get("X-Gateway-Test") != "enabled" {
			t.Errorf("expected injected gateway header")
		}
		if r.Header.Get("X-Forwarded-For") == "" {
			t.Errorf("expected X-Forwarded-For header")
		}
		_, _ = w.Write([]byte("response from srv1: " + r.URL.Path))
	}))
	defer srv1.Close()

	// Backend 2
	var backend2Calls atomic.Int64
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backend2Calls.Add(1)
		w.Header().Set("X-Backend", "srv2")
		_, _ = w.Write([]byte("response from srv2: " + r.URL.Path))
	}))
	defer srv2.Close()

	proxy, err := gateway.NewProxy(
		gateway.RouteConfig{
			PathPrefix:  "/api/users",
			StripPrefix: true,
			Targets:     []string{srv1.URL, srv2.URL},
			Headers:     map[string]string{"X-Gateway-Test": "enabled"},
			Balancer:    gateway.NewRoundRobinBalancer(),
		},
		gateway.RouteConfig{
			PathPrefix:  "/api/users/special",
			StripPrefix: false,
			Targets:     []string{srv1.URL},
		},
	)
	if err != nil {
		t.Fatalf("failed to create proxy: %v", err)
	}

	t.Run("round-robin distribution and strip prefix", func(t *testing.T) {
		rec1 := httptest.NewRecorder()
		req1 := httptest.NewRequest(http.MethodGet, "/api/users/profile", nil)
		proxy.ServeHTTP(rec1, req1)

		if rec1.Code != http.StatusOK {
			t.Errorf("expected 200, got: %d", rec1.Code)
		}
		if rec1.Body.String() != "response from srv1: /profile" {
			t.Errorf("unexpected body: %s", rec1.Body.String())
		}

		rec2 := httptest.NewRecorder()
		req2 := httptest.NewRequest(http.MethodGet, "/api/users/profile", nil)
		proxy.ServeHTTP(rec2, req2)

		if rec2.Code != http.StatusOK {
			t.Errorf("expected 200, got: %d", rec2.Code)
		}
		if rec2.Body.String() != "response from srv2: /profile" {
			t.Errorf("unexpected body: %s", rec2.Body.String())
		}
	})

	t.Run("longest prefix match overrides shorter prefix", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/users/special/account", nil)
		proxy.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("expected 200, got: %d", rec.Code)
		}
		// StripPrefix was false for /api/users/special
		if rec.Body.String() != "response from srv1: /api/users/special/account" {
			t.Errorf("unexpected body: %s", rec.Body.String())
		}
	})

	t.Run("route not found", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/unregistered/path", nil)
		proxy.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Errorf("expected 404, got %d", rec.Code)
		}
	})
}

func TestProxy_ValidationAndErrors(t *testing.T) {
	t.Run("empty path prefix", func(t *testing.T) {
		_, err := gateway.NewProxy(gateway.RouteConfig{
			PathPrefix: "",
			Targets:    []string{"http://localhost:8080"},
		})
		if !errors.Is(err, gateway.ErrRouteNotFound) {
			t.Errorf("expected ErrRouteNotFound, got: %v", err)
		}
	})

	t.Run("empty targets", func(t *testing.T) {
		_, err := gateway.NewProxy(gateway.RouteConfig{
			PathPrefix: "/test",
			Targets:    nil,
		})
		if !errors.Is(err, gateway.ErrNoHealthyTargets) {
			t.Errorf("expected ErrNoHealthyTargets, got: %v", err)
		}
	})

	t.Run("invalid target url", func(t *testing.T) {
		_, err := gateway.NewProxy(gateway.RouteConfig{
			PathPrefix: "/test",
			Targets:    []string{"://invalid-url"},
		})
		if !errors.Is(err, gateway.ErrInvalidTarget) {
			t.Errorf("expected ErrInvalidTarget, got: %v", err)
		}
	})

	t.Run("custom error handler when targets unhealthy", func(t *testing.T) {
		var customErrCalled bool
		p, _ := gateway.NewProxy(gateway.RouteConfig{
			PathPrefix: "/dead",
			Targets:    []string{"http://localhost:8080"},
			ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
				customErrCalled = true
				w.WriteHeader(http.StatusServiceUnavailable)
			},
		})

		// Make target dead manually
		u, _ := url.Parse("http://localhost:8080")
		t1 := gateway.NewTarget(u)
		t1.SetAlive(false)
		_ = p.AddRoute(gateway.RouteConfig{
			PathPrefix: "/dead2",
			Targets:    []string{"http://localhost:8080"},
			Balancer: &mockDeadBalancer{
				err: gateway.ErrNoHealthyTargets,
			},
			ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
				customErrCalled = true
				w.WriteHeader(http.StatusServiceUnavailable)
			},
		})

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/dead2/test", nil)
		p.ServeHTTP(rec, req)

		if !customErrCalled || rec.Code != http.StatusServiceUnavailable {
			t.Errorf("expected custom error handler called with 503")
		}
	})
}

type mockDeadBalancer struct {
	err error
}

func (m *mockDeadBalancer) Next(targets []*gateway.Target) (*gateway.Target, error) {
	return nil, m.err
}

func TestAggregator_ScatterGather(t *testing.T) {
	ctx := context.Background()

	t.Run("successful parallel aggregation", func(t *testing.T) {
		agg := gateway.NewAggregator().
			Add("user", func(ctx context.Context) (any, error) {
				time.Sleep(10 * time.Millisecond)
				return map[string]string{"name": "Alice", "id": "u-101"}, nil
			}, gateway.WithCritical(true)).
			Add("orders", func(ctx context.Context) (any, error) {
				time.Sleep(15 * time.Millisecond)
				return []string{"ord-1", "ord-2"}, nil
			}).
			Add("notifications_count", func(ctx context.Context) (any, error) {
				return 5, nil
			})

		res, err := agg.Execute(ctx)
		if err != nil {
			t.Fatalf("unexpected aggregation error: %v", err)
		}

		if res.HasErrors() {
			t.Errorf("expected no errors in result")
		}

		// Typed getter
		user, err := gateway.GetTyped[map[string]string](res, "user")
		if err != nil || user["name"] != "Alice" {
			t.Errorf("failed to get typed user: %v (%v)", user, err)
		}

		orders, err := gateway.GetTyped[[]string](res, "orders")
		if err != nil || len(orders) != 2 {
			t.Errorf("failed to get typed orders: %v (%v)", orders, err)
		}

		count, err := gateway.GetTyped[int](res, "notifications_count")
		if err != nil || count != 5 {
			t.Errorf("failed to get count: %d (%v)", count, err)
		}

		// JSON serialization check
		jsonBytes, err := res.JSON()
		if err != nil || len(jsonBytes) == 0 {
			t.Errorf("failed to serialize JSON: %v", err)
		}
	})

	t.Run("non-critical task fails with fallback", func(t *testing.T) {
		errRecs := errors.New("recommendations service down")
		agg := gateway.NewAggregator().
			Add("user", func(ctx context.Context) (any, error) {
				return "Alice", nil
			}, gateway.WithCritical(true)).
			Add("recommendations", func(ctx context.Context) (any, error) {
				return nil, errRecs
			}, gateway.WithFallback([]string{"default-item-1"})).
			Add("trending", func(ctx context.Context) (any, error) {
				return nil, errors.New("trending down without fallback")
			})

		res, err := agg.Execute(ctx)
		if err != nil {
			t.Fatalf("non-critical failure should not fail aggregation: %v", err)
		}

		if !res.HasErrors() {
			t.Errorf("expected HasErrors to be true")
		}

		recs, err := gateway.GetTyped[[]string](res, "recommendations")
		if err != nil || len(recs) != 1 || recs[0] != "default-item-1" {
			t.Errorf("expected fallback recommendations: %v (%v)", recs, err)
		}

		if res.Errors["trending"] == "" {
			t.Errorf("expected error recorded for trending")
		}
	})

	t.Run("critical task failure aborts aggregation", func(t *testing.T) {
		errAuth := errors.New("auth service timeout")
		agg := gateway.NewAggregator().
			Add("user", func(ctx context.Context) (any, error) {
				return nil, errAuth
			}, gateway.WithCritical(true)).
			Add("orders", func(ctx context.Context) (any, error) {
				return []string{"ord-1"}, nil
			})

		_, err := agg.Execute(ctx)
		if !errors.Is(err, gateway.ErrCriticalTaskFailed) {
			t.Errorf("expected ErrCriticalTaskFailed, got: %v", err)
		}
	})

	t.Run("task panic recovery", func(t *testing.T) {
		agg := gateway.NewAggregator().
			Add("panic_task", func(ctx context.Context) (any, error) {
				panic("task crashed!")
			})

		res, err := agg.Execute(ctx)
		if err != nil {
			t.Fatalf("expected graceful aggregation without crash, got err: %v", err)
		}

		if res.Errors["panic_task"] == "" {
			t.Errorf("expected panic captured in errors")
		}
	})

	t.Run("per-task timeout", func(t *testing.T) {
		agg := gateway.NewAggregator().
			Add("slow_task", func(ctx context.Context) (any, error) {
				select {
				case <-time.After(100 * time.Millisecond):
					return "done", nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}, gateway.WithTaskTimeout(10*time.Millisecond))

		res, err := agg.Execute(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Errors["slow_task"] == "" {
			t.Errorf("expected timeout error in task")
		}
	})

	t.Run("GetTyped errors", func(t *testing.T) {
		agg := gateway.NewAggregator().
			Add("number", func(ctx context.Context) (any, error) {
				return 42, nil
			})
		res, _ := agg.Execute(ctx)

		// Missing key
		_, err := gateway.GetTyped[int](res, "missing")
		if err == nil {
			t.Errorf("expected error for missing key")
		}

		// Type mismatch
		_, err = gateway.GetTyped[string](res, "number")
		if err == nil {
			t.Errorf("expected type mismatch error")
		}
	})

	t.Run("cancelled context before execute", func(t *testing.T) {
		cancCtx, cancel := context.WithCancel(ctx)
		cancel()

		agg := gateway.NewAggregator().Add("task", func(ctx context.Context) (any, error) {
			return 1, nil
		})
		_, err := agg.Execute(cancCtx)
		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected context.Canceled, got: %v", err)
		}
	})
}
