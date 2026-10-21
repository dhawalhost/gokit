package webhook

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/dhawalhost/gokit/client"
)

// DeliveryResult contains diagnostic telemetry for an outbound webhook attempt.
type DeliveryResult struct {
	StatusCode int           `json:"status_code"`
	Duration   time.Duration `json:"duration"`
	Error      error         `json:"error,omitempty"`
	Success    bool          `json:"success"`
}

// Dispatcher manages outbound webhook deliveries with HMAC signing and retries.
type Dispatcher struct {
	httpClient *client.Client
}

// DispatcherConfig holds options for the outbound webhook dispatcher.
type DispatcherConfig struct {
	Timeout    time.Duration // HTTP request timeout (default: 10s)
	MaxRetries int           // Max retry attempts on failure (default: 3)
	BaseDelay  time.Duration // Exponential backoff base delay (default: 500ms)
}

// NewDispatcher creates a new outbound webhook Dispatcher.
func NewDispatcher(cfg DispatcherConfig) *Dispatcher {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Second
	}
	if cfg.MaxRetries <= 0 {
		cfg.MaxRetries = 3
	}
	if cfg.BaseDelay <= 0 {
		cfg.BaseDelay = 500 * time.Millisecond
	}

	retryPolicy := client.DefaultRetryPolicy()
	retryPolicy.MaxRetries = cfg.MaxRetries
	retryPolicy.InitialBackoff = cfg.BaseDelay
	retryPolicy.MaxBackoff = cfg.BaseDelay * 10

	cli := client.New(
		client.WithName("webhook-dispatcher"),
		client.WithTimeout(cfg.Timeout),
		client.WithRetryPolicy(retryPolicy),
	)

	return &Dispatcher{
		httpClient: cli,
	}
}

// Dispatch signs the payload with secret and sends it via HTTP POST to endpointURL.
func (d *Dispatcher) Dispatch(ctx context.Context, endpointURL string, payload []byte, secret []byte) (*DeliveryResult, error) {
	now := time.Now().UTC()
	timestamp := strconv.FormatInt(now.Unix(), 10)
	signature := ComputeSignature(payload, timestamp, secret)

	start := time.Now()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointURL, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("webhook: build request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(DefaultHeaderSignature, signature)
	req.Header.Set(DefaultHeaderTimestamp, timestamp)

	resp, err := d.httpClient.Do(req)
	duration := time.Since(start)

	if err != nil {
		return &DeliveryResult{
			StatusCode: 0,
			Duration:   duration,
			Error:      err,
			Success:    false,
		}, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return &DeliveryResult{
			StatusCode: resp.StatusCode,
			Duration:   duration,
			Success:    true,
		}, nil
	}

	statusErr := fmt.Errorf("webhook: received non-2xx status code: %d", resp.StatusCode)
	return &DeliveryResult{
		StatusCode: resp.StatusCode,
		Duration:   duration,
		Error:      statusErr,
		Success:    false,
	}, statusErr
}
