// circuit-breaker demonstrates wrapping outbound HTTP calls with gokit's
// CircuitBreaker, including state monitoring, OnStateChange callbacks, and
// per-request execution timeouts.
//
// The example exposes:
//   - POST /api/v1/upstream/call  — calls a configurable downstream URL through
//     the circuit breaker; returns 503 when the circuit is open, 504 on timeout.
//   - GET  /api/v1/upstream/status — returns the current circuit-breaker state.
//   - GET  /simulate/fail          — always returns 500 (use as a fake downstream
//     to trip the circuit when running locally without a real dependency).
//
// Run:
//
//	APP_SERVER_ADDR=:8080 DOWNSTREAM_URL=http://localhost:8080/simulate/fail go run main.go
//
// Trip the circuit (needs MaxFailures=5 consecutive failures):
//
//	for i in $(seq 1 6); do
//	  curl -s -X POST http://localhost:8080/api/v1/upstream/call | jq .
//	done
//
// Check state:
//
//	curl http://localhost:8080/api/v1/upstream/status
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/dhawalhost/gokit/circuitbreaker"
	"github.com/dhawalhost/gokit/config"
	apperrors "github.com/dhawalhost/gokit/errors"
	"github.com/dhawalhost/gokit/health"
	"github.com/dhawalhost/gokit/logger"
	mw "github.com/dhawalhost/gokit/middleware"
	"github.com/dhawalhost/gokit/observability"
	"github.com/dhawalhost/gokit/response"
	"github.com/dhawalhost/gokit/server"
)

// ---------------------------------------------------------------------------
// Handler dependencies
// ---------------------------------------------------------------------------

type upstreamHandler struct {
	cb            *circuitbreaker.CircuitBreaker
	cbName        string
	downstreamURL string
	httpClient    *http.Client
	log           *zap.Logger
}

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

func main() {
	cfg := config.MustLoad(os.Getenv("CONFIG_FILE"))

	downstreamURL := os.Getenv("DOWNSTREAM_URL")
	if downstreamURL == "" {
		downstreamURL = "http://localhost:9090/health"
	}

	log, err := logger.New(cfg.Log.Level, cfg.Log.Development)
	if err != nil {
		panic("failed to build logger: " + err.Error())
	}
	defer func() { _ = log.Sync() }()
	logger.SetGlobal(log)

	serviceName := cfg.Telemetry.ServiceName
	if serviceName == "" {
		serviceName = "circuit_breaker"
	}
	observability.InitMetrics(serviceName)

	ctx := context.Background()
	shutdownTracer, err := observability.InitTracer(ctx, cfg.Telemetry)
	if err != nil {
		log.Fatal("failed to initialise tracer", zap.Error(err))
	}
	defer func() { _ = shutdownTracer(ctx) }()

	// Build the circuit breaker.
	cb := circuitbreaker.New(circuitbreaker.Config{
		Name:             "downstream",
		MaxFailures:      5,
		ResetTimeout:     30 * time.Second,
		ExecutionTimeout: 3 * time.Second,
		OnStateChange: func(name string, from, to circuitbreaker.State) {
			log.Warn("circuit breaker state changed",
				zap.String("name", name),
				zap.String("from", stateString(from)),
				zap.String("to", stateString(to)),
			)
		},
	})

	h := &upstreamHandler{
		cb:            cb,
		cbName:        "downstream",
		downstreamURL: downstreamURL,
		// Give the HTTP client a short dial/TLS timeout; the circuit breaker's
		// ExecutionTimeout is the upper bound for the whole call.
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
			Transport: &http.Transport{
				MaxIdleConnsPerHost:   10,
				IdleConnTimeout:       90 * time.Second,
				TLSHandshakeTimeout:   5 * time.Second,
				ResponseHeaderTimeout: 4 * time.Second,
			},
		},
		log: log,
	}

	healthHandler := health.NewHandler()

	srv := server.New(
		server.WithAddr(cfg.Server.Addr),
		server.WithReadTimeout(cfg.Server.ReadTimeout),
		server.WithWriteTimeout(cfg.Server.WriteTimeout),
		server.WithIdleTimeout(cfg.Server.IdleTimeout),
		server.WithShutdownTimeout(cfg.Server.ShutdownTimeout),
	)

	srv.Use(
		mw.RequestID(),
		mw.Recovery(log),
		mw.SecureHeaders(),
		mw.Logger(log),
		observability.Metrics(),
		observability.Tracing(serviceName),
	)

	srv.Mount("/health", buildHealthRouter(healthHandler))
	srv.Mount("/metrics", observability.MetricsHandler())
	srv.Mount("/api/v1", buildAPIRouter(h, cfg.Server.WriteTimeout))
	srv.Mount("/simulate", buildSimulateRouter())

	log.Info("server listening",
		zap.String("addr", cfg.Server.Addr),
		zap.String("downstream", downstreamURL),
	)
	if err := srv.Run(ctx); err != nil {
		log.Fatal("server error", zap.Error(err))
	}
}

// ---------------------------------------------------------------------------
// Routers
// ---------------------------------------------------------------------------

func buildHealthRouter(h *health.Handler) http.Handler {
	r := chi.NewRouter()
	r.Get("/live", h.LiveHandler())
	r.Get("/ready", h.ReadyHandler())
	return r
}

func buildAPIRouter(h *upstreamHandler, writeTimeout time.Duration) http.Handler {
	r := chi.NewRouter()
	r.Use(mw.Timeout(writeTimeout - 5*time.Second))

	r.Route("/upstream", func(r chi.Router) {
		r.Post("/call", h.callUpstream)
		r.Get("/status", h.circuitStatus)
	})

	return r
}

// buildSimulateRouter returns a router with endpoints that simulate downstream
// failure and success, useful when running the example locally.
func buildSimulateRouter() http.Handler {
	r := chi.NewRouter()

	// Always returns 500 — use as a "broken" downstream to trip the circuit.
	r.Get("/fail", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"simulated downstream failure"}`, http.StatusInternalServerError)
	})

	// Always returns 200 — use to let reset-timeout elapse then recover.
	r.Get("/ok", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"status":"ok"}`)
	})

	return r
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

// callUpstream proxies a GET request to the configured downstream URL through
// the circuit breaker.
//
// POST /api/v1/upstream/call
//
// Responses:
//   - 200 OK             — downstream responded successfully
//   - 502 Bad Gateway    — downstream returned a non-2xx status
//   - 503 Service Unavailable — circuit is open
//   - 504 Gateway Timeout     — execution timeout exceeded
func (h *upstreamHandler) callUpstream(w http.ResponseWriter, r *http.Request) {
	var (
		statusCode int
		body       []byte
	)

	err := h.cb.Execute(func() error {
		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, h.downstreamURL, nil)
		if err != nil {
			return fmt.Errorf("build request: %w", err)
		}

		resp, err := h.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("downstream call: %w", err)
		}
		defer func() { _ = resp.Body.Close() }()

		body, err = io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		if err != nil {
			return fmt.Errorf("read response body: %w", err)
		}

		statusCode = resp.StatusCode

		if resp.StatusCode >= 500 {
			// Treat 5xx as a circuit-breaker failure so transient infrastructure
			// problems accumulate toward the trip threshold.
			return fmt.Errorf("downstream returned %d", resp.StatusCode)
		}

		return nil
	})

	if err != nil {
		switch {
		case errors.Is(err, circuitbreaker.ErrOpen):
			h.log.Warn("circuit open, rejecting request", zap.String("downstream", h.downstreamURL))
			apperrors.WriteError(w, r, apperrors.New(http.StatusServiceUnavailable, "CIRCUIT_OPEN",
				"downstream service is temporarily unavailable; please retry later"))
		case errors.Is(err, circuitbreaker.ErrExecutionTimeout):
			h.log.Error("downstream timed out", zap.String("downstream", h.downstreamURL), zap.Error(err))
			apperrors.WriteError(w, r, apperrors.New(http.StatusGatewayTimeout, "DOWNSTREAM_TIMEOUT",
				"downstream service did not respond in time"))
		default:
			h.log.Error("downstream call failed", zap.String("downstream", h.downstreamURL), zap.Error(err))
			apperrors.WriteError(w, r, apperrors.New(http.StatusBadGateway, "DOWNSTREAM_ERROR", err.Error()))
		}
		return
	}

	// Successfully proxied — pass through the response body and downstream status.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_, _ = w.Write(body)
}

// circuitStatus returns the current circuit-breaker state.
//
// GET /api/v1/upstream/status.
func (h *upstreamHandler) circuitStatus(w http.ResponseWriter, r *http.Request) {
	response.Ok(w, r, map[string]string{
		"circuit": h.cbName,
		"state":   stateString(h.cb.State()),
	})
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// stateString converts a circuitbreaker.State to a human-readable string,
// matching the conventional closed / open / half_open naming used in dashboards.
func stateString(s circuitbreaker.State) string {
	switch s {
	case circuitbreaker.StateClosed:
		return "closed"
	case circuitbreaker.StateOpen:
		return "open"
	case circuitbreaker.StateHalfOpen:
		return "half_open"
	default:
		return "unknown"
	}
}
