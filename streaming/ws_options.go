package streaming

import (
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

// SlowClientPolicy dictates what the Hub does when a client's outbound channel is full.
type SlowClientPolicy int

const (
	// SlowClientDrop drops the outgoing message if the client buffer is full, preserving the connection.
	SlowClientDrop SlowClientPolicy = iota
	// SlowClientDisconnect drops the message and closes the connection for the slow client.
	SlowClientDisconnect
)

// AuthFunc extracts authentication identity and metadata from the HTTP upgrade request.
// If it returns an error, the upgrade request is rejected.
type AuthFunc func(r *http.Request) (userID, tenantID string, metadata map[string]string, err error)

// HubConfig holds configuration settings for the WebSocket Hub.
type HubConfig struct {
	WriteWait        time.Duration
	PongWait         time.Duration
	PingPeriod       time.Duration
	MaxMessageSize   int64
	ClientBufferSize int
	SlowClientPolicy SlowClientPolicy
	Upgrader         websocket.Upgrader
	AuthFunc         AuthFunc
	OnConnect        func(client *Client)
	OnDisconnect     func(client *Client, err error)
	OnMessage        func(client *Client, msg Message)
	Logger           *zap.Logger
}

// DefaultHubConfig returns production-ready default Hub configuration settings.
func DefaultHubConfig() HubConfig {
	pongWait := 60 * time.Second
	return HubConfig{
		WriteWait:        10 * time.Second,
		PongWait:         pongWait,
		PingPeriod:       (pongWait * 9) / 10,
		MaxMessageSize:   512 * 1024, // 512 KB
		ClientBufferSize: 256,
		SlowClientPolicy: SlowClientDrop,
		Upgrader: websocket.Upgrader{
			ReadBufferSize:  4096,
			WriteBufferSize: 4096,
			CheckOrigin: func(r *http.Request) bool {
				return true // Permissive default; configurable via WithCheckOrigin
			},
		},
		Logger: zap.NewNop(),
	}
}

// HubOption configures a Hub.
type HubOption func(*HubConfig)

// WithWriteWait sets the deadline for writing a message to a client.
func WithWriteWait(d time.Duration) HubOption {
	return func(c *HubConfig) {
		if d > 0 {
			c.WriteWait = d
		}
	}
}

// WithPongWait sets the timeout for receiving a pong response from a client.
func WithPongWait(d time.Duration) HubOption {
	return func(c *HubConfig) {
		if d > 0 {
			c.PongWait = d
			c.PingPeriod = (d * 9) / 10
		}
	}
}

// WithPingPeriod explicitly sets the interval for sending ping frames to clients.
func WithPingPeriod(d time.Duration) HubOption {
	return func(c *HubConfig) {
		if d > 0 {
			c.PingPeriod = d
		}
	}
}

// WithMaxMessageSize sets the maximum allowed size (in bytes) for inbound messages.
func WithMaxMessageSize(size int64) HubOption {
	return func(c *HubConfig) {
		if size > 0 {
			c.MaxMessageSize = size
		}
	}
}

// WithClientBufferSize sets the outbound message channel buffer capacity per client.
func WithClientBufferSize(size int) HubOption {
	return func(c *HubConfig) {
		if size > 0 {
			c.ClientBufferSize = size
		}
	}
}

// WithSlowClientPolicy sets the action to take when a client's outbound channel is full.
func WithSlowClientPolicy(p SlowClientPolicy) HubOption {
	return func(c *HubConfig) {
		c.SlowClientPolicy = p
	}
}

// WithCheckOrigin sets a custom origin verification function for the WebSocket upgrader.
func WithCheckOrigin(fn func(r *http.Request) bool) HubOption {
	return func(c *HubConfig) {
		if fn != nil {
			c.Upgrader.CheckOrigin = fn
		}
	}
}

// WithAuthFunc registers an authentication extractor for the WebSocket upgrade handshake.
func WithAuthFunc(fn AuthFunc) HubOption {
	return func(c *HubConfig) {
		c.AuthFunc = fn
	}
}

// WithOnConnect registers a callback invoked when a new client connects and registers.
func WithOnConnect(fn func(client *Client)) HubOption {
	return func(c *HubConfig) {
		c.OnConnect = fn
	}
}

// WithOnDisconnect registers a callback invoked when a client disconnects.
func WithOnDisconnect(fn func(client *Client, err error)) HubOption {
	return func(c *HubConfig) {
		c.OnDisconnect = fn
	}
}

// WithOnMessage registers a callback invoked when an inbound message is received from a client.
func WithOnMessage(fn func(client *Client, msg Message)) HubOption {
	return func(c *HubConfig) {
		c.OnMessage = fn
	}
}

// WithHubLogger sets a custom zap.Logger for the Hub.
func WithHubLogger(l *zap.Logger) HubOption {
	return func(c *HubConfig) {
		if l != nil {
			c.Logger = l
		}
	}
}
