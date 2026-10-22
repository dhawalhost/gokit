# streaming

`streaming` provides production-ready real-time communication primitives for modern Go microservices and distributed applications:
1. **Server-Sent Events (SSE)**: RFC-compliant push streaming for LLM/AI token streaming (ChatGPT-style interfaces), live dashboards, metrics, and progress tickers.
2. **WebSocket Hub**: Full-duplex room/topic pub/sub messaging hub with heartbeat keepalives, backpressure buffering, and authentication hooks.

---

## Features

### Server-Sent Events (SSE)
- **RFC 8895 Compliance**: Formats standard `id`, `event`, `data`, `retry`, and `: comment` frames.
- **AI/LLM Stream Ready**: Seamlessly sends structured event streams or chunks with multi-line preservation and JSON encoding.
- **Reverse Proxy Optimized**: Automatically sets `Content-Type: text/event-stream`, `Cache-Control: no-cache, no-transform`, and `X-Accel-Buffering: no` to prevent Nginx, Envoy, or Kubernetes ingress controllers from buffering.
- **Keep-Alive Heartbeats**: Configurable ping ticker (: comment) to keep idle connections open through cloud load balancers (ALBs) during long inference periods.
- **Cancellation & Disconnect Hooks**: Derives a context from the incoming request to stop background generation immediately when the user disconnects or aborts the stream.

### WebSocket Pub/Sub Hub
- **Topic / Room Pub/Sub**: Multiplex hundreds of topics (`chat:room-1`, `user:usr-123`, `metrics:cpu`) over single client connections.
- **Backpressure & Slow Client Policies**:
  - `SlowClientDrop`: Drops frames when a client's send buffer overflows without disconnecting.
  - `SlowClientDisconnect`: Disconnects slow/lagging clients to protect server stability and memory.
- **Handshake Authentication**: `AuthFunc` hook to extract JWT tokens or session cookies during HTTP Upgrade, associating user ID, tenant ID, and custom metadata.
- **Lifecycle & Heartbeats**: Background read/write pumps managing RFC-6455 Ping/Pong frames, read deadlines, and graceful shutdown frames.

---

## Architecture Overview

```
                      ┌───────────────────────────────────────┐
                      │              HTTP Client              │
                      └───────┬───────────────────────▲───────┘
                              │ SSE GET /events       │ Chunked Stream
                              ▼                       │ (Flush)
                      ┌───────────────────────────────┴───────┐
                      │             SSEHandler                │
                      │  - Disables reverse proxy buffering   │
                      │  - Heartbeat (: ping) ticker          │
                      │  - Disconnect context propagation     │
                      └───────────────────────────────────────┘

                      ┌───────────────────────────────────────┐
                      │            WebSocket Client           │
                      └───────▲───────────────────────┬───────┘
                 Read │ Write │ RFC-6455 Frames       │ Inbound Messages
                 Pump │ Pump  ▼                       ▼
                      ┌───────────────────────────────────────┐
                      │                Hub                    │
                      │  - Topic index (topics -> clients)    │
                      │  - Fast RWMutex broadcasting          │
                      │  - SlowClient backpressure policies   │
                      └───────────────────────────────────────┘
```

---

## Installation

```bash
go get github.com/dhawalhost/gokit/streaming
```

---

## Quickstart

### 1. AI/LLM Token Streaming with SSE

```go
package main

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/dhawalhost/gokit/streaming"
	"github.com/go-chi/chi/v5"
)

func main() {
	r := chi.NewRouter()

	r.Get("/v1/chat/completions", streaming.SSEHandler(
		func(ctx context.Context, w streaming.EventWriter) error {
			tokens := []string{"Hello", " world,", " this", " is", " streaming", " AI!"}

			for i, token := range tokens {
				select {
				case <-ctx.Done():
					// User hit "Stop Generating" or disconnected
					return ctx.Err()
				default:
				}

				// Send structured JSON or raw event
				err := w.Send(streaming.Event{
					ID:    fmt.Sprintf("tok-%d", i),
					Event: "token",
					Data: map[string]string{
						"delta": token,
					},
				})
				if err != nil {
					return err
				}

				time.Sleep(50 * time.Millisecond) // Simulate LLM latency
			}

			// Signal completion
			return w.Send(streaming.Event{
				Event: "done",
				Data:  "[DONE]",
			})
		},
		streaming.WithSSEHeartbeat(15*time.Second),
		streaming.WithSSEHeaders(map[string]string{
			"Access-Control-Allow-Origin": "*",
		}),
	))

	http.ListenAndServe(":8080", r)
}
```

---

### 2. Real-Time WebSocket Pub/Sub Hub

```go
package main

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/dhawalhost/gokit/streaming"
	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"
)

func main() {
	logger, _ := zap.NewProduction()

	// Initialize the Hub
	hub := streaming.NewHub(
		streaming.WithHubLogger(logger),
		streaming.WithSlowClientPolicy(streaming.SlowClientDrop),
		streaming.WithAuthFunc(func(r *http.Request) (string, string, map[string]string, error) {
			token := r.URL.Query().Get("token")
			if token == "" {
				return "", "", nil, fmt.Errorf("missing token")
			}
			return "user-123", "tenant-abc", map[string]string{"role": "member"}, nil
		}),
		streaming.WithOnConnect(func(c *streaming.Client) {
			logger.Info("client connected", zap.String("id", c.ID()), zap.String("user", c.UserID()))
			// Auto-subscribe to personal notifications and tenant topic
			c.Subscribe("user:" + c.UserID())
			c.Subscribe("tenant:" + c.TenantID())
		}),
		streaming.WithOnMessage(func(c *streaming.Client, msg streaming.Message) {
			logger.Info("received client message", zap.String("client", c.ID()), zap.ByteString("data", msg.Payload))
		}),
	)

	// Clean shutdown when service stops
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go hub.Start(ctx)

	r := chi.NewRouter()

	// WebSocket upgrade endpoint
	r.Get("/ws", hub.HTTPHandler())

	// HTTP endpoint triggering a topic broadcast
	r.Post("/broadcast", func(w http.ResponseWriter, r *http.Request) {
		topic := r.URL.Query().Get("topic")
		hub.Broadcast(topic, map[string]string{
			"alert": "Maintenance starting in 10 minutes",
		})
		w.WriteHeader(http.StatusOK)
	})

	http.ListenAndServe(":8080", r)
}
```

---

## Configuration Reference

### SSE Options

| Option | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `WithSSEHeartbeat(d)` | `time.Duration` | `15s` | Interval for emitting `: ping\n\n` comments (0 to disable). |
| `WithSSEHeaders(headers)` | `map[string]string` | `nil` | Additional HTTP response headers (e.g. CORS). |
| `WithSSELogger(logger)` | `*zap.Logger` | `zap.NewNop()` | Custom Zap logger for errors. |
| `WithSSEOnConnect(fn)` | `func(*http.Request)` | `nil` | Hook called when stream connects. |
| `WithSSEOnDisconnect(fn)` | `func(*http.Request, error)` | `nil` | Hook called when stream ends or client cancels. |

### WebSocket Hub Options

| Option | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `WithWriteWait(d)` | `time.Duration` | `10s` | Network write deadline per frame. |
| `WithPongWait(d)` | `time.Duration` | `60s` | Read timeout waiting for client Pong. |
| `WithPingPeriod(d)` | `time.Duration` | `54s` | Ping frame interval (must be `< PongWait`). |
| `WithMaxMessageSize(bytes)` | `int64` | `512 KB` | Maximum inbound message payload size. |
| `WithClientBufferSize(size)` | `int` | `256` | Outbound channel capacity per client. |
| `WithSlowClientPolicy(p)` | `SlowClientPolicy` | `SlowClientDrop` | `SlowClientDrop` or `SlowClientDisconnect`. |
| `WithCheckOrigin(fn)` | `func(*http.Request) bool` | Permissive | Origin validation for CORS handshakes. |
| `WithAuthFunc(fn)` | `AuthFunc` | `nil` | Handshake authentication and identity extractor. |
