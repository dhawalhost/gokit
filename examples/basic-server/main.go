// basic-server demonstrates a minimal production-ready HTTP server using gokit.
// It wires the full middleware stack, health probes, and Prometheus metrics.
//
// Run:
//
//	APP_SERVER_ADDR=:8080 APP_LOG_LEVEL=info go run main.go
package main

import (
	"context"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/dhawalhost/gokit/config"
	"github.com/dhawalhost/gokit/health"
	"github.com/dhawalhost/gokit/logger"
	mw "github.com/dhawalhost/gokit/middleware"
	"github.com/dhawalhost/gokit/observability"
	"github.com/dhawalhost/gokit/response"
	"github.com/dhawalhost/gokit/server"
)

func main() {
	cfg := config.MustLoad(os.Getenv("CONFIG_FILE"))

	log, err := logger.New(cfg.Log.Level, cfg.Log.Development)
	if err != nil {
		panic("failed to build logger: " + err.Error())
	}
	defer func() { _ = log.Sync() }()
	logger.SetGlobal(log)

	// Prometheus metrics must be initialised before the Metrics() middleware.
	serviceName := cfg.Telemetry.ServiceName
	if serviceName == "" {
		serviceName = "basic_server"
	}
	observability.InitMetrics(serviceName)

	// OpenTelemetry tracing (no-op when cfg.Telemetry.Enabled == false).
	ctx := context.Background()
	shutdownTracer, err := observability.InitTracer(ctx, cfg.Telemetry)
	if err != nil {
		log.Fatal("failed to initialise tracer", zap.Error(err))
	}
	defer func() { _ = shutdownTracer(ctx) }()

	// Health handler — no checkers registered here; register DB/Redis in more
	// complex examples. This responds to orchestrator liveness and readiness probes.
	healthHandler := health.NewHandlerWithTimeouts(5*time.Second, 10*time.Second)

	srv := server.New(
		server.WithAddr(cfg.Server.Addr),
		server.WithReadTimeout(cfg.Server.ReadTimeout),
		server.WithWriteTimeout(cfg.Server.WriteTimeout),
		server.WithIdleTimeout(cfg.Server.IdleTimeout),
		server.WithShutdownTimeout(cfg.Server.ShutdownTimeout),
	)

	// Global middleware — order matters.
	srv.Use(
		mw.RequestID(),     // inject / propagate X-Request-ID
		mw.Recovery(log),   // catch panics, return 500
		mw.SecureHeaders(), // HSTS, CSP, X-Frame-Options, …
		mw.CORS([]string{"https://yourfrontend.com"}), // restrict origins in production
		mw.Logger(log),                     // structured access logs (skips /health)
		observability.Metrics(),            // Prometheus counter / histogram
		observability.Tracing(serviceName), // OpenTelemetry span per request
	)

	// Infrastructure endpoints (no per-request timeout needed here).
	srv.Mount("/health", buildHealthRouter(healthHandler))
	srv.Mount("/metrics", observability.MetricsHandler())

	// API — apply a per-request timeout inside the sub-router.
	srv.Mount("/api/v1", buildAPIRouter(log, cfg.Server.WriteTimeout))

	log.Info("server listening", zap.String("addr", cfg.Server.Addr))
	if err := srv.Run(ctx); err != nil {
		log.Fatal("server error", zap.Error(err))
	}
}

// buildHealthRouter mounts the liveness and readiness probes.
func buildHealthRouter(h *health.Handler) http.Handler {
	r := chi.NewRouter()
	r.Get("/live", h.LiveHandler())
	r.Get("/ready", h.ReadyHandler())
	return r
}

// buildAPIRouter builds the versioned API sub-router.
func buildAPIRouter(_ *zap.Logger, writeTimeout time.Duration) http.Handler {
	r := chi.NewRouter()
	// Leave enough margin between request timeout and server write timeout.
	r.Use(mw.Timeout(writeTimeout - 5*time.Second))

	r.Get("/ping", pingHandler)
	r.Get("/echo", echoHandler)

	return r
}

// pingHandler is a trivial liveness check for the API layer.
func pingHandler(w http.ResponseWriter, r *http.Request) {
	response.Ok(w, r, map[string]string{"message": "pong"})
}

// echoHandler returns the request ID and remote addr so the caller can verify
// that middleware is working end-to-end.
func echoHandler(w http.ResponseWriter, r *http.Request) {
	response.Ok(w, r, map[string]string{
		"request_id":  mw.RequestIDFromContext(r.Context()),
		"remote_addr": r.RemoteAddr,
		"user_agent":  r.UserAgent(),
	})
}
