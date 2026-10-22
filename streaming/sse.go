package streaming

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
)

// Event represents a single Server-Sent Event frame.
type Event struct {
	// ID is an optional event identifier.
	ID string
	// Event is the event type/name (e.g., "message", "token", "error", "done").
	Event string
	// Data is the payload. Can be string, []byte, or any JSON-serializable value.
	Data any
	// Retry advises the client on reconnection wait time in milliseconds.
	Retry time.Duration
	// Comment is a comment line (: comment). Useful for keepalive pings.
	Comment string
}

// EventWriter writes Server-Sent Events to an HTTP response stream.
type EventWriter interface {
	// Send formats and sends an Event, immediately flushing it to the network.
	Send(event Event) error
	// SendJSON marshals data to JSON and sends it with the specified event name.
	SendJSON(event string, data any) error
	// Comment sends a comment line (: text\n\n) as a keepalive and flushes.
	Comment(text string) error
	// Context returns the underlying request context (canceled on client disconnect).
	Context() context.Context
	// Flush flushes buffered data to the client.
	Flush()
}

// sseWriter implements EventWriter.
type sseWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
	ctx     context.Context
	mu      sync.Mutex
	closed  atomic.Bool
	logger  *zap.Logger
}

// Send serializes and flushes an event to the client.
func (s *sseWriter) Send(event Event) error {
	if s.closed.Load() {
		return context.Canceled
	}
	select {
	case <-s.ctx.Done():
		return s.ctx.Err()
	default:
	}

	buf := &bytes.Buffer{}

	if event.Comment != "" {
		for _, line := range strings.Split(event.Comment, "\n") {
			buf.WriteString(": ")
			buf.WriteString(line)
			buf.WriteByte('\n')
		}
	}

	if event.ID != "" {
		buf.WriteString("id: ")
		buf.WriteString(event.ID)
		buf.WriteByte('\n')
	}

	if event.Event != "" {
		buf.WriteString("event: ")
		buf.WriteString(event.Event)
		buf.WriteByte('\n')
	}

	if event.Retry > 0 {
		fmt.Fprintf(buf, "retry: %d\n", event.Retry.Milliseconds())
	}

	if event.Data != nil {
		if err := writeDataLines(buf, event.Data); err != nil {
			return fmt.Errorf("streaming: marshal event data: %w", err)
		}
	}

	// Double newline indicates the end of an event frame.
	buf.WriteByte('\n')

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := s.w.Write(buf.Bytes()); err != nil {
		return err
	}
	s.flusher.Flush()
	return nil
}

// writeDataLines formats data with "data: " prefixes for each newline.
func writeDataLines(buf *bytes.Buffer, data any) error {
	var raw []byte

	switch v := data.(type) {
	case []byte:
		raw = v
	case string:
		raw = []byte(v)
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		raw = b
	}

	lines := bytes.Split(raw, []byte("\n"))
	for _, line := range lines {
		buf.WriteString("data: ")
		buf.Write(line)
		buf.WriteByte('\n')
	}
	return nil
}

// SendJSON marshals data as JSON and sends an event with the given event name.
func (s *sseWriter) SendJSON(event string, data any) error {
	return s.Send(Event{
		Event: event,
		Data:  data,
	})
}

// Comment sends a comment line (: text\n\n) as a keepalive and flushes.
func (s *sseWriter) Comment(text string) error {
	return s.Send(Event{
		Comment: text,
	})
}

// Context returns the request context.
func (s *sseWriter) Context() context.Context {
	return s.ctx
}

// Flush flushes buffered data to the client.
func (s *sseWriter) Flush() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.flusher.Flush()
}

// Close marks the writer closed.
func (s *sseWriter) Close() {
	s.closed.Store(true)
}

// SSEHandlerFunc is a user-supplied handler for an SSE stream.
type SSEHandlerFunc func(ctx context.Context, w EventWriter) error

// SSEConfig holds configuration for SSEHandler.
type SSEConfig struct {
	HeartbeatInterval time.Duration
	Headers           map[string]string
	Logger            *zap.Logger
	OnConnect         func(r *http.Request)
	OnDisconnect      func(r *http.Request, err error)
}

// SSEOption configures an SSEHandler.
type SSEOption func(*SSEConfig)

// WithSSEHeartbeat sets periodic heartbeat comment intervals (e.g. 15s).
// Set to 0 to disable heartbeats.
func WithSSEHeartbeat(d time.Duration) SSEOption {
	return func(c *SSEConfig) {
		c.HeartbeatInterval = d
	}
}

// WithSSEHeaders adds custom response headers (e.g. CORS).
func WithSSEHeaders(headers map[string]string) SSEOption {
	return func(c *SSEConfig) {
		if c.Headers == nil {
			c.Headers = make(map[string]string)
		}
		for k, v := range headers {
			c.Headers[k] = v
		}
	}
}

// WithSSELogger configures a custom zap.Logger.
func WithSSELogger(l *zap.Logger) SSEOption {
	return func(c *SSEConfig) {
		if l != nil {
			c.Logger = l
		}
	}
}

// WithSSEOnConnect sets a hook invoked when a client successfully connects.
func WithSSEOnConnect(fn func(r *http.Request)) SSEOption {
	return func(c *SSEConfig) {
		c.OnConnect = fn
	}
}

// WithSSEOnDisconnect sets a hook invoked when an SSE stream finishes or disconnects.
func WithSSEOnDisconnect(fn func(r *http.Request, err error)) SSEOption {
	return func(c *SSEConfig) {
		c.OnDisconnect = fn
	}
}

// SSEHandler returns an http.HandlerFunc that negotiates SSE headers,
// verifies Flusher support, starts keepalive heartbeats, and runs fn.
func SSEHandler(fn SSEHandlerFunc, opts ...SSEOption) http.HandlerFunc {
	cfg := &SSEConfig{
		HeartbeatInterval: 15 * time.Second,
		Logger:            zap.NewNop(),
	}
	for _, opt := range opts {
		opt(cfg)
	}

	return func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "Streaming unsupported: ResponseWriter does not implement http.Flusher", http.StatusInternalServerError)
			cfg.Logger.Error("sse: client connection does not support http.Flusher")
			return
		}

		// Set standard SSE headers
		h := w.Header()
		h.Set("Content-Type", "text/event-stream")
		h.Set("Cache-Control", "no-cache, no-transform")
		h.Set("Connection", "keep-alive")
		// Disable buffering in Nginx and Envoy reverse proxies
		h.Set("X-Accel-Buffering", "no")

		for k, v := range cfg.Headers {
			h.Set(k, v)
		}

		// Flush headers immediately
		flusher.Flush()

		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()

		writer := &sseWriter{
			w:       w,
			flusher: flusher,
			ctx:     ctx,
			logger:  cfg.Logger,
		}
		defer writer.Close()

		if cfg.OnConnect != nil {
			cfg.OnConnect(r)
		}

		// Start heartbeat ticker if interval > 0
		var (
			heartbeatDone chan struct{}
			heartbeatWG   sync.WaitGroup
		)
		if cfg.HeartbeatInterval > 0 {
			heartbeatDone = make(chan struct{})
			heartbeatWG.Add(1)
			go func() {
				defer heartbeatWG.Done()
				ticker := time.NewTicker(cfg.HeartbeatInterval)
				defer ticker.Stop()

				for {
					select {
					case <-ticker.C:
						if err := writer.Comment("ping"); err != nil {
							cancel()
							return
						}
					case <-ctx.Done():
						return
					case <-heartbeatDone:
						return
					}
				}
			}()
		}

		err := fn(ctx, writer)
		if heartbeatDone != nil {
			close(heartbeatDone)
			heartbeatWG.Wait()
		}

		if cfg.OnDisconnect != nil {
			cfg.OnDisconnect(r, err)
		}
	}
}
