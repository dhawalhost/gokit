// cache-ratelimit demonstrates Redis-backed caching and per-IP rate limiting
// using gokit's cache, ratelimit, and middleware packages.
//
// Run:
//
//	docker run -d -p 6379:6379 redis:7-alpine
//	APP_REDIS_ADDR=localhost:6379 APP_SERVER_ADDR=:8080 go run main.go
//
// Test:
//
//	# Set a value (JSON body, TTL is a Go duration string)
//	curl -X POST -H 'Content-Type: application/json' \
//	  -d '{"key":"greeting","value":"hello","ttl":"60s"}' \
//	  http://localhost:8080/api/v1/cache/set
//
//	# Get a value
//	curl "http://localhost:8080/api/v1/cache/get?key=greeting"
//
//	# Delete a value
//	curl -X DELETE "http://localhost:8080/api/v1/cache/delete?key=greeting"
//
//	# Trigger the rate limit (10 req/s sustained, burst 20)
//	for i in $(seq 1 30); do curl -s -o /dev/null -w "%{http_code}\n" "http://localhost:8080/api/v1/cache/get?key=x"; done
package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/dhawalhost/gokit/cache"
	"github.com/dhawalhost/gokit/config"
	apperrors "github.com/dhawalhost/gokit/errors"
	"github.com/dhawalhost/gokit/health"
	"github.com/dhawalhost/gokit/logger"
	mw "github.com/dhawalhost/gokit/middleware"
	"github.com/dhawalhost/gokit/observability"
	"github.com/dhawalhost/gokit/ratelimit"
	"github.com/dhawalhost/gokit/response"
	"github.com/dhawalhost/gokit/server"
)

// ---------------------------------------------------------------------------
// Handler dependencies
// ---------------------------------------------------------------------------

type cacheHandler struct {
	cache *cache.RedisCache
	log   *zap.Logger
}

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

func main() {
	cfg := config.MustLoad(os.Getenv("CONFIG_FILE"))

	if cfg.Redis.Addr == "" {
		panic("APP_REDIS_ADDR is required")
	}

	log, err := logger.New(cfg.Log.Level, cfg.Log.Development)
	if err != nil {
		panic("failed to build logger: " + err.Error())
	}
	defer func() { _ = log.Sync() }()
	logger.SetGlobal(log)

	serviceName := cfg.Telemetry.ServiceName
	if serviceName == "" {
		serviceName = "cache_ratelimit"
	}
	observability.InitMetrics(serviceName)

	ctx := context.Background()
	shutdownTracer, err := observability.InitTracer(ctx, cfg.Telemetry)
	if err != nil {
		log.Fatal("failed to initialise tracer", zap.Error(err))
	}
	defer func() { _ = shutdownTracer(ctx) }()

	redisCache, err := cache.NewRedis(cfg.Redis)
	if err != nil {
		log.Fatal("failed to connect to Redis", zap.Error(err))
	}
	defer func() { _ = redisCache.Close() }()

	// Build a raw *redis.Client for the rate-limit store so it shares the same
	// connection pool configuration as the cache.
	redisClient := redis.NewClient(&redis.Options{
		Addr:         cfg.Redis.Addr,
		Password:     cfg.Redis.Password,
		DB:           cfg.Redis.DB,
		DialTimeout:  cfg.Redis.DialTimeout,
		ReadTimeout:  cfg.Redis.ReadTimeout,
		WriteTimeout: cfg.Redis.WriteTimeout,
		PoolSize:     cfg.Redis.PoolSize,
		PoolTimeout:  cfg.Redis.PoolTimeout,
	})
	defer func() { _ = redisClient.Close() }()

	// Redis sliding-window rate limiter: 10 req/s sustained, burst of 20.
	rlStore := ratelimit.NewRedisStore(redisClient, 1*time.Second)
	rateLimitMW := mw.RateLimit(mw.RateLimitConfig{
		RequestsPerSecond: 10,
		Burst:             20,
		Store:             rlStore,
		KeyFunc:           mw.IPKeyFunc,
	})

	healthHandler := health.NewHandler()
	healthHandler.Register("redis", redisCache)

	h := &cacheHandler{cache: redisCache, log: log}

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
	srv.Mount("/api/v1", buildAPIRouter(h, rateLimitMW, cfg.Server.WriteTimeout))

	log.Info("server listening", zap.String("addr", cfg.Server.Addr))
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

func buildAPIRouter(h *cacheHandler, rateLimitMW func(http.Handler) http.Handler, writeTimeout time.Duration) http.Handler {
	r := chi.NewRouter()
	r.Use(mw.Timeout(writeTimeout - 5*time.Second))
	r.Use(rateLimitMW)

	r.Route("/cache", func(r chi.Router) {
		r.Get("/get", h.getKey)
		r.Post("/set", h.setKey)
		r.Delete("/delete", h.deleteKey)
	})

	return r
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

// getKey looks up a cache key.
//
// GET /api/v1/cache/get?key=<key>.
func (h *cacheHandler) getKey(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("key")
	if key == "" {
		apperrors.WriteError(w, r, apperrors.BadRequest("MISSING_KEY", "query parameter 'key' is required"))
		return
	}

	val, err := h.cache.Get(r.Context(), key)
	if err != nil {
		// Redis returns redis.Nil when the key does not exist.
		if errors.Is(err, redis.Nil) {
			apperrors.WriteError(w, r, apperrors.NotFound("KEY_NOT_FOUND", "key not found or expired"))
			return
		}
		h.log.Error("cache get failed", zap.String("key", key), zap.Error(err))
		apperrors.WriteError(w, r, apperrors.Internal("failed to read from cache"))
		return
	}

	response.Ok(w, r, map[string]string{"key": key, "value": val})
}

// setKey stores a value in the cache.
//
// POST /api/v1/cache/set
// Body: {"key":"...", "value":"...", "ttl":"30s"}.
func (h *cacheHandler) setKey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Key   string `json:"key"   validate:"required"`
		Value string `json:"value" validate:"required"`
		TTL   string `json:"ttl"   validate:"required"`
	}

	if err := parseJSON(r, &req); err != nil {
		apperrors.WriteError(w, r, apperrors.BadRequest("INVALID_BODY", err.Error()))
		return
	}

	ttl, err := time.ParseDuration(req.TTL)
	if err != nil || ttl <= 0 {
		apperrors.WriteError(w, r, apperrors.BadRequest("INVALID_TTL", "ttl must be a positive duration (e.g. '30s', '5m')"))
		return
	}

	if err := h.cache.Set(r.Context(), req.Key, req.Value, ttl); err != nil {
		h.log.Error("cache set failed", zap.String("key", req.Key), zap.Error(err))
		apperrors.WriteError(w, r, apperrors.Internal("failed to write to cache"))
		return
	}

	response.Ok(w, r, map[string]interface{}{
		"key":        req.Key,
		"expires_in": req.TTL,
	})
}

// deleteKey removes one or more keys.
//
// DELETE /api/v1/cache/delete?key=<key>.
func (h *cacheHandler) deleteKey(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("key")
	if key == "" {
		apperrors.WriteError(w, r, apperrors.BadRequest("MISSING_KEY", "query parameter 'key' is required"))
		return
	}

	if err := h.cache.Delete(r.Context(), key); err != nil {
		h.log.Error("cache delete failed", zap.String("key", key), zap.Error(err))
		apperrors.WriteError(w, r, apperrors.Internal("failed to delete from cache"))
		return
	}

	response.NoContent(w)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func parseJSON(r *http.Request, dst any) error {
	return json.NewDecoder(r.Body).Decode(dst)
}
