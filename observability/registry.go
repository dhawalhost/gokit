package observability

import (
	"crypto/subtle"
	"fmt"
	"net/http"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// DefaultLatencyBuckets are standard HTTP/gRPC latency buckets in seconds.
var DefaultLatencyBuckets = []float64{
	0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10,
}

// Registry provides an isolated, thread-safe Prometheus metrics registry with pre-wired
// HTTP, gRPC, and Go runtime instrumentation.
type Registry struct {
	mu                 sync.RWMutex
	promReg            *prometheus.Registry
	namespace          string
	httpDuration       *prometheus.HistogramVec
	httpInFlight       prometheus.Gauge
	httpRequestsTotal  *prometheus.CounterVec
	grpcServerDuration *prometheus.HistogramVec
	grpcServerTotal    *prometheus.CounterVec
	grpcClientDuration *prometheus.HistogramVec
	grpcClientTotal    *prometheus.CounterVec
}

// RegistryOption configures a Registry.
type RegistryOption func(*registryConfig)

type registryConfig struct {
	includeGoRuntime bool
	includeProcess   bool
	buckets          []float64
}

// WithGoRuntimeMetrics enables Go runtime collectors (goroutines, memory, GC stats).
func WithGoRuntimeMetrics() RegistryOption {
	return func(c *registryConfig) {
		c.includeGoRuntime = true
	}
}

// WithProcessMetrics enables OS process collectors (CPU usage, memory RSS, open file descriptors).
func WithProcessMetrics() RegistryOption {
	return func(c *registryConfig) {
		c.includeProcess = true
	}
}

// WithLatencyBuckets configures custom histogram latency buckets.
func WithLatencyBuckets(buckets []float64) RegistryOption {
	return func(c *registryConfig) {
		if len(buckets) > 0 {
			c.buckets = buckets
		}
	}
}

// NewRegistry creates a new isolated metrics registry with pre-wired HTTP and gRPC metrics.
func NewRegistry(namespace string, opts ...RegistryOption) *Registry {
	cfg := registryConfig{
		includeGoRuntime: true,
		includeProcess:   true,
		buckets:          DefaultLatencyBuckets,
	}

	for _, opt := range opts {
		opt(&cfg)
	}

	promReg := prometheus.NewRegistry()

	if cfg.includeGoRuntime {
		_ = promReg.Register(collectors.NewGoCollector())
	}
	if cfg.includeProcess {
		_ = promReg.Register(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	}

	reg := &Registry{
		promReg:   promReg,
		namespace: namespace,
	}

	// Pre-wire HTTP metrics
	reg.httpDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Name:      "http_request_duration_seconds",
		Help:      "Histogram of HTTP request latencies in seconds.",
		Buckets:   cfg.buckets,
	}, []string{"method", "path", "status"})

	reg.httpInFlight = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "http_requests_in_flight",
		Help:      "Current number of HTTP requests being processed.",
	})

	reg.httpRequestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "http_requests_total",
		Help:      "Total count of HTTP requests served.",
	}, []string{"method", "path", "status"})

	// Pre-wire gRPC Server metrics
	reg.grpcServerDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Name:      "grpc_server_handling_seconds",
		Help:      "Histogram of gRPC server call latencies in seconds.",
		Buckets:   cfg.buckets,
	}, []string{"grpc_service", "grpc_method", "grpc_code"})

	reg.grpcServerTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "grpc_server_handled_total",
		Help:      "Total count of completed gRPC server calls.",
	}, []string{"grpc_service", "grpc_method", "grpc_code"})

	// Pre-wire gRPC Client metrics
	reg.grpcClientDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Name:      "grpc_client_handling_seconds",
		Help:      "Histogram of outbound gRPC client call latencies in seconds.",
		Buckets:   cfg.buckets,
	}, []string{"grpc_service", "grpc_method", "grpc_code"})

	reg.grpcClientTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "grpc_client_handled_total",
		Help:      "Total count of completed outbound gRPC client calls.",
	}, []string{"grpc_service", "grpc_method", "grpc_code"})

	_ = promReg.Register(reg.httpDuration)
	_ = promReg.Register(reg.httpInFlight)
	_ = promReg.Register(reg.httpRequestsTotal)
	_ = promReg.Register(reg.grpcServerDuration)
	_ = promReg.Register(reg.grpcServerTotal)
	_ = promReg.Register(reg.grpcClientDuration)
	_ = promReg.Register(reg.grpcClientTotal)

	return reg
}

// PrometheusRegistry returns the underlying *prometheus.Registry.
func (r *Registry) PrometheusRegistry() *prometheus.Registry {
	return r.promReg
}

// HTTPDuration returns the pre-wired HTTP duration histogram.
func (r *Registry) HTTPDuration() *prometheus.HistogramVec {
	return r.httpDuration
}

// HTTPInFlight returns the pre-wired HTTP in-flight gauge.
func (r *Registry) HTTPInFlight() prometheus.Gauge {
	return r.httpInFlight
}

// HTTPRequestsTotal returns the pre-wired HTTP requests total counter.
func (r *Registry) HTTPRequestsTotal() *prometheus.CounterVec {
	return r.httpRequestsTotal
}

// GRPCServerDuration returns the pre-wired gRPC server duration histogram.
func (r *Registry) GRPCServerDuration() *prometheus.HistogramVec {
	return r.grpcServerDuration
}

// GRPCServerTotal returns the pre-wired gRPC server total counter.
func (r *Registry) GRPCServerTotal() *prometheus.CounterVec {
	return r.grpcServerTotal
}

// GRPCClientDuration returns the pre-wired gRPC client duration histogram.
func (r *Registry) GRPCClientDuration() *prometheus.HistogramVec {
	return r.grpcClientDuration
}

// GRPCClientTotal returns the pre-wired gRPC client total counter.
func (r *Registry) GRPCClientTotal() *prometheus.CounterVec {
	return r.grpcClientTotal
}

// NewCounter registers and returns a new CounterVec within this registry.
func (r *Registry) NewCounter(name, help string, labels ...string) (*prometheus.CounterVec, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	cv := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: r.namespace,
		Name:      name,
		Help:      help,
	}, labels)

	if err := r.promReg.Register(cv); err != nil {
		return nil, fmt.Errorf("observability: register counter %q: %w", name, err)
	}
	return cv, nil
}

// MustNewCounter creates a new CounterVec and panics if registration fails.
func (r *Registry) MustNewCounter(name, help string, labels ...string) *prometheus.CounterVec {
	cv, err := r.NewCounter(name, help, labels...)
	if err != nil {
		panic(err)
	}
	return cv
}

// NewGauge registers and returns a new GaugeVec within this registry.
func (r *Registry) NewGauge(name, help string, labels ...string) (*prometheus.GaugeVec, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	gv := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: r.namespace,
		Name:      name,
		Help:      help,
	}, labels)

	if err := r.promReg.Register(gv); err != nil {
		return nil, fmt.Errorf("observability: register gauge %q: %w", name, err)
	}
	return gv, nil
}

// MustNewGauge creates a new GaugeVec and panics if registration fails.
func (r *Registry) MustNewGauge(name, help string, labels ...string) *prometheus.GaugeVec {
	gv, err := r.NewGauge(name, help, labels...)
	if err != nil {
		panic(err)
	}
	return gv
}

// NewHistogram registers and returns a new HistogramVec within this registry.
func (r *Registry) NewHistogram(name, help string, buckets []float64, labels ...string) (*prometheus.HistogramVec, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(buckets) == 0 {
		buckets = DefaultLatencyBuckets
	}

	hv := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: r.namespace,
		Name:      name,
		Help:      help,
		Buckets:   buckets,
	}, labels)

	if err := r.promReg.Register(hv); err != nil {
		return nil, fmt.Errorf("observability: register histogram %q: %w", name, err)
	}
	return hv, nil
}

// MustNewHistogram creates a new HistogramVec and panics if registration fails.
func (r *Registry) MustNewHistogram(name, help string, buckets []float64, labels ...string) *prometheus.HistogramVec {
	hv, err := r.NewHistogram(name, help, buckets, labels...)
	if err != nil {
		panic(err)
	}
	return hv
}

// HandlerOption configures the HTTP metrics exposure endpoint.
type HandlerOption func(*handlerConfig)

type handlerConfig struct {
	authUser string
	authPass string
}

// WithBasicAuth protects the /metrics endpoint with HTTP Basic Authentication.
func WithBasicAuth(username, password string) HandlerOption {
	return func(c *handlerConfig) {
		c.authUser = username
		c.authPass = password
	}
}

// HTTPHandler returns an http.Handler that exports Prometheus metrics from this registry.
func (r *Registry) HTTPHandler(opts ...HandlerOption) http.Handler {
	var cfg handlerConfig
	for _, opt := range opts {
		opt(&cfg)
	}

	h := promhttp.HandlerFor(r.promReg, promhttp.HandlerOpts{
		EnableOpenMetrics: true,
	})

	if cfg.authUser != "" && cfg.authPass != "" {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			user, pass, ok := req.BasicAuth()
			if !ok || subtle.ConstantTimeCompare([]byte(user), []byte(cfg.authUser)) != 1 ||
				subtle.ConstantTimeCompare([]byte(pass), []byte(cfg.authPass)) != 1 {
				w.Header().Set("WWW-Authenticate", `Basic realm="metrics"`)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			h.ServeHTTP(w, req)
		})
	}

	return h
}
