package streaming

import (
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Client represents an active WebSocket connection.
type Client struct {
	id       string
	userID   string
	tenantID string
	metadata map[string]string

	conn *websocket.Conn
	hub  *Hub

	send chan Message

	topicsMu sync.RWMutex
	topics   map[string]struct{}

	closeOnce sync.Once
	closed    chan struct{}
}

// newClient constructs a new Client instance.
func newClient(id, userID, tenantID string, metadata map[string]string, conn *websocket.Conn, hub *Hub) *Client {
	if metadata == nil {
		metadata = make(map[string]string)
	}
	return &Client{
		id:       id,
		userID:   userID,
		tenantID: tenantID,
		metadata: metadata,
		conn:     conn,
		hub:      hub,
		send:     make(chan Message, hub.cfg.ClientBufferSize),
		topics:   make(map[string]struct{}),
		closed:   make(chan struct{}),
	}
}

// ID returns the unique identifier for the client connection.
func (c *Client) ID() string {
	return c.id
}

// UserID returns the user ID associated with the client, if authenticated.
func (c *Client) UserID() string {
	return c.userID
}

// TenantID returns the tenant ID associated with the client, if applicable.
func (c *Client) TenantID() string {
	return c.tenantID
}

// Metadata returns a copy of the client's metadata map.
func (c *Client) Metadata() map[string]string {
	c.topicsMu.RLock()
	defer c.topicsMu.RUnlock()
	res := make(map[string]string, len(c.metadata))
	for k, v := range c.metadata {
		res[k] = v
	}
	return res
}

// Subscribe registers the client to a topic.
func (c *Client) Subscribe(topic string) {
	if topic == "" {
		return
	}
	c.topicsMu.Lock()
	c.topics[topic] = struct{}{}
	c.topicsMu.Unlock()

	c.hub.addSubscription(c, topic)
}

// Unsubscribe removes the client from a topic.
func (c *Client) Unsubscribe(topic string) {
	if topic == "" {
		return
	}
	c.topicsMu.Lock()
	delete(c.topics, topic)
	c.topicsMu.Unlock()

	c.hub.removeSubscription(c, topic)
}

// IsSubscribed reports whether the client is currently subscribed to topic.
func (c *Client) IsSubscribed(topic string) bool {
	c.topicsMu.RLock()
	defer c.topicsMu.RUnlock()
	_, ok := c.topics[topic]
	return ok
}

// Topics returns a slice of all topics the client is currently subscribed to.
func (c *Client) Topics() []string {
	c.topicsMu.RLock()
	defer c.topicsMu.RUnlock()
	res := make([]string, 0, len(c.topics))
	for t := range c.topics {
		res = append(res, t)
	}
	return res
}

// Send enqueues a message to be written to the client connection.
// It applies the configured SlowClientPolicy if the client's send buffer is full.
func (c *Client) Send(msg Message) bool {
	select {
	case <-c.closed:
		return false
	default:
	}

	select {
	case c.send <- msg:
		return true
	default:
		// Client buffer is full
		if c.hub.cfg.SlowClientPolicy == SlowClientDisconnect {
			c.Close()
		}
		return false
	}
}

// Close gracefully closes the client connection.
func (c *Client) Close() {
	c.closeOnce.Do(func() {
		close(c.closed)
		// Close send channel to notify writePump to flush and exit
		close(c.send)
		_ = c.conn.Close()
	})
}

// readPump pumps messages from the websocket connection to the hub.
func (c *Client) readPump() {
	var readErr error
	defer func() {
		c.hub.unregisterClient(c, readErr)
		c.Close()
	}()

	c.conn.SetReadLimit(c.hub.cfg.MaxMessageSize)
	_ = c.conn.SetReadDeadline(time.Now().Add(c.hub.cfg.PongWait))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(c.hub.cfg.PongWait))
	})

	for {
		messageType, payload, err := c.conn.ReadMessage()
		if err != nil {
			readErr = err
			break
		}

		msg := Message{
			Type:      messageType,
			Payload:   payload,
			Timestamp: time.Now().UTC(),
		}
		c.hub.handleInbound(c, msg)
	}
}

// writePump pumps messages from the hub to the websocket connection.
func (c *Client) writePump() {
	ticker := time.NewTicker(c.hub.cfg.PingPeriod)
	defer func() {
		ticker.Stop()
		_ = c.conn.Close()
	}()

	for {
		select {
		case msg, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(c.hub.cfg.WriteWait))
			if !ok {
				// The hub closed the channel
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			if err := c.conn.WriteMessage(msg.Type, msg.Payload); err != nil {
				return
			}

		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(c.hub.cfg.WriteWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}

		case <-c.closed:
			return
		}
	}
}
