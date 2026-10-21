package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.uber.org/zap"

	"github.com/dhawalhost/gokit/circuitbreaker"
	"github.com/dhawalhost/gokit/logger"
	"github.com/dhawalhost/gokit/middleware"
)

// Client is a resilient, thread-safe outbound HTTP client.
type Client struct {
	cfg        Config
	httpClient *http.Client
}

// DefaultTransport creates a production-hardened http.Transport with sensible connection pooling and timeouts.
func DefaultTransport() *http.Transport {
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   20,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second,
	}
}

// New creates and initializes a new Client with the provided options.
func New(opts ...Option) *Client {
	cfg := Config{
		Timeout:            30 * time.Second,
		Retry:              DefaultRetryPolicy(),
		PropagateRequestID: true,
		PropagateTracing:   true,
		Logger:             logger.Global(),
	}

	for _, opt := range opts {
		opt(&cfg)
	}

	if cfg.Logger == nil {
		cfg.Logger = zap.NewNop()
	}

	transport := cfg.Transport
	if transport == nil {
		transport = DefaultTransport()
	}

	httpClient := &http.Client{
		Transport: transport,
	}

	return &Client{
		cfg:        cfg,
		httpClient: httpClient,
	}
}

// Do executes an HTTP request with retry logic, circuit breaker protection, tracing, and logging.
func (c *Client) Do(req *http.Request) (*http.Response, error) {
	ctx := req.Context()

	// Apply overall client timeout if configured and not already bounded
	var cancel context.CancelFunc
	if c.cfg.Timeout > 0 {
		if _, hasDeadline := ctx.Deadline(); !hasDeadline {
			ctx, cancel = context.WithTimeout(ctx, c.cfg.Timeout)
			defer cancel()
			req = req.WithContext(ctx)
		}
	}

	// Prepare request body for potential replays across retries
	if req.Body != nil && req.GetBody == nil {
		bodyBytes, err := io.ReadAll(req.Body)
		if err != nil {
			_ = req.Body.Close()
			return nil, fmt.Errorf("client: read request body for retry: %w", err)
		}
		_ = req.Body.Close()
		req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		req.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(bodyBytes)), nil
		}
	}

	// Attach default headers
	for key, values := range c.cfg.DefaultHeaders {
		for _, val := range values {
			if req.Header.Get(key) == "" {
				req.Header.Add(key, val)
			}
		}
	}

	// Forward Request ID if available in context
	if c.cfg.PropagateRequestID && req.Header.Get("X-Request-ID") == "" {
		if reqID := middleware.RequestIDFromContext(ctx); reqID != "" {
			req.Header.Set("X-Request-ID", reqID)
		}
	}

	// Inject W3C TraceContext into request headers
	if c.cfg.PropagateTracing {
		otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(req.Header))
	}

	var lastResp *http.Response
	var lastErr error
	maxAttempts := c.cfg.Retry.MaxRetries + 1

	for attempt := 0; attempt < maxAttempts; attempt++ {
		// If retrying, reset the request body
		if attempt > 0 && req.GetBody != nil {
			newBody, err := req.GetBody()
			if err != nil {
				return nil, fmt.Errorf("client: reset body for attempt %d: %w", attempt+1, err)
			}
			req.Body = newBody
		}

		// Perform attempt under circuit breaker if configured
		start := time.Now()
		var resp *http.Response
		var err error

		if c.cfg.CircuitBreaker != nil && c.cfg.CircuitBreaker.State() == circuitbreaker.StateOpen {
			return nil, fmt.Errorf("circuitbreaker: %s: %w", c.cfg.Name, circuitbreaker.ErrOpen)
		}

		resp, err = c.doSingleAttempt(req)

		if c.cfg.CircuitBreaker != nil {
			if err != nil || (resp != nil && resp.StatusCode >= 500) {
				_ = c.cfg.CircuitBreaker.Execute(func() error {
					if err != nil {
						return err
					}
					return fmt.Errorf("client: downstream %s returned status %d", c.cfg.Name, resp.StatusCode)
				})
			} else {
				_ = c.cfg.CircuitBreaker.Execute(func() error {
					return nil
				})
			}
		}

		duration := time.Since(start)

		// Check if the result is retryable
		if attempt < c.cfg.Retry.MaxRetries && c.cfg.Retry.isRetryable(ctx, resp, err) {
			backoff := c.cfg.Retry.calculateBackoff(attempt, resp)

			c.cfg.Logger.Warn("outbound HTTP request failed, retrying",
				zap.String("service", c.cfg.Name),
				zap.String("method", req.Method),
				zap.String("url", req.URL.String()),
				zap.Int("attempt", attempt+1),
				zap.Duration("duration", duration),
				zap.Duration("backoff", backoff),
				zap.Error(err),
			)

			// Drain and close previous response body to allow connection reuse
			if resp != nil && resp.Body != nil {
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
			}

			// Wait for backoff or context cancellation
			select {
			case <-time.After(backoff):
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		lastResp = resp
		lastErr = err
		break
	}

	return lastResp, lastErr
}

// doSingleAttempt executes a single HTTP request attempt with optional attempt timeout.
func (c *Client) doSingleAttempt(req *http.Request) (*http.Response, error) {
	if c.cfg.AttemptTimeout > 0 {
		attemptCtx, cancel := context.WithTimeout(req.Context(), c.cfg.AttemptTimeout)
		defer cancel()
		req = req.WithContext(attemptCtx)
	}

	return c.httpClient.Do(req)
}

// resolveURL constructs the full URL using BaseURL if a relative path is provided.
func (c *Client) resolveURL(rawURL string) (string, error) {
	if c.cfg.BaseURL == "" {
		return rawURL, nil
	}

	base, err := url.Parse(c.cfg.BaseURL)
	if err != nil {
		return "", fmt.Errorf("client: invalid base URL %q: %w", c.cfg.BaseURL, err)
	}

	rel, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("client: invalid target URL %q: %w", rawURL, err)
	}

	return base.ResolveReference(rel).String(), nil
}

// Get issues an HTTP GET request to the specified URL.
func (c *Client) Get(ctx context.Context, rawURL string) (*http.Response, error) {
	fullURL, err := c.resolveURL(rawURL)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, fmt.Errorf("client: new GET request: %w", err)
	}

	return c.Do(req)
}

// Post issues an HTTP POST request with the specified content type and body.
func (c *Client) Post(ctx context.Context, rawURL, contentType string, body io.Reader) (*http.Response, error) {
	fullURL, err := c.resolveURL(rawURL)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fullURL, body)
	if err != nil {
		return nil, fmt.Errorf("client: new POST request: %w", err)
	}

	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	return c.Do(req)
}

// PostJSON marshals reqBody to JSON, executes a POST request, and unmarshals the response into respTarget (if non-nil).
func (c *Client) PostJSON(ctx context.Context, rawURL string, reqBody, respTarget any) (*http.Response, error) {
	var bodyReader io.Reader
	if reqBody != nil {
		data, err := json.Marshal(reqBody)
		if err != nil {
			return nil, fmt.Errorf("client: marshal json body: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	fullURL, err := c.resolveURL(rawURL)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fullURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("client: new POST JSON request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}

	if respTarget != nil {
		defer func() {
			_ = resp.Body.Close()
		}()

		respData, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return resp, fmt.Errorf("client: read response json body: %w", readErr)
		}

		if unmarshalErr := json.Unmarshal(respData, respTarget); unmarshalErr != nil {
			return resp, fmt.Errorf("client: unmarshal response json: %w", unmarshalErr)
		}
	}

	return resp, nil
}

// Put issues an HTTP PUT request with the specified content type and body.
func (c *Client) Put(ctx context.Context, rawURL, contentType string, body io.Reader) (*http.Response, error) {
	fullURL, err := c.resolveURL(rawURL)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, fullURL, body)
	if err != nil {
		return nil, fmt.Errorf("client: new PUT request: %w", err)
	}

	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	return c.Do(req)
}

// Delete issues an HTTP DELETE request to the specified URL.
func (c *Client) Delete(ctx context.Context, rawURL string) (*http.Response, error) {
	fullURL, err := c.resolveURL(rawURL)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, fullURL, nil)
	if err != nil {
		return nil, fmt.Errorf("client: new DELETE request: %w", err)
	}

	return c.Do(req)
}
