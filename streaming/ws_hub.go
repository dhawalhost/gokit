package streaming

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

var (
	// ErrHubClosed is returned when an operation is attempted on a closed Hub.
	ErrHubClosed = errors.New("streaming: hub is closed")
	// ErrClientNotFound is returned when attempting to send to a non-existent client.
	ErrClientNotFound = errors.New("streaming: client not found")
)

// Hub manages WebSocket clients, topic subscriptions, and broadcast fan-out.
type Hub struct {
	cfg HubConfig

	mu      sync.RWMutex
	clients map[string]*Client            // clientID -> *Client
	topics  map[string]map[string]*Client // topic -> clientID -> *Client

	closed atomic.Bool
}

// NewHub constructs a new WebSocket Hub with the given options.
func NewHub(opts ...HubOption) *Hub {
	cfg := DefaultHubConfig()
	for _, opt := range opts {
		opt(&cfg)
	}

	return &Hub{
		cfg:     cfg,
		clients: make(map[string]*Client),
		topics:  make(map[string]map[string]*Client),
	}
}

// ClientConfig holds per-connection configuration during upgrade.
type ClientConfig struct {
	ID            string
	InitialTopics []string
	Metadata      map[string]string
}

// ClientOption configures client upgrade parameters.
type ClientOption func(*ClientConfig)

// WithClientID explicitly sets the client identifier instead of generating a random UUID.
func WithClientID(id string) ClientOption {
	return func(c *ClientConfig) {
		c.ID = id
	}
}

// WithInitialTopics subscribes the client to topics upon connection.
func WithInitialTopics(topics ...string) ClientOption {
	return func(c *ClientConfig) {
		c.InitialTopics = append(c.InitialTopics, topics...)
	}
}

// WithClientMetadata sets a metadata key-value pair for the client.
func WithClientMetadata(key, val string) ClientOption {
	return func(c *ClientConfig) {
		if c.Metadata == nil {
			c.Metadata = make(map[string]string)
		}
		c.Metadata[key] = val
	}
}

// Start runs the hub and blocks until the context is canceled, whereupon Stop is invoked.
func (h *Hub) Start(ctx context.Context) error {
	<-ctx.Done()
	return h.Stop()
}

// Stop closes all active client connections and marks the hub closed.
func (h *Hub) Stop() error {
	if !h.closed.CompareAndSwap(false, true) {
		return nil
	}

	h.mu.Lock()
	clients := make([]*Client, 0, len(h.clients))
	for _, c := range h.clients {
		clients = append(clients, c)
	}
	h.clients = make(map[string]*Client)
	h.topics = make(map[string]map[string]*Client)
	h.mu.Unlock()

	for _, c := range clients {
		c.Close()
	}

	h.cfg.Logger.Info("streaming: websocket hub stopped", zap.Int("closed_clients", len(clients)))
	return nil
}

// Upgrade handles an HTTP upgrade request, authenticates, registers the client,
// and starts the client read/write pumps.
func (h *Hub) Upgrade(w http.ResponseWriter, r *http.Request, opts ...ClientOption) (*Client, error) {
	if h.closed.Load() {
		http.Error(w, "Hub is closed", http.StatusServiceUnavailable)
		return nil, ErrHubClosed
	}

	var userID, tenantID string
	metadata := make(map[string]string)

	if h.cfg.AuthFunc != nil {
		u, t, m, err := h.cfg.AuthFunc(r)
		if err != nil {
			http.Error(w, fmt.Sprintf("Unauthorized: %v", err), http.StatusUnauthorized)
			return nil, fmt.Errorf("streaming: auth failed: %w", err)
		}
		userID = u
		tenantID = t
		for k, v := range m {
			metadata[k] = v
		}
	}

	clientCfg := ClientConfig{
		ID:       uuid.NewString(),
		Metadata: metadata,
	}
	for _, opt := range opts {
		opt(&clientCfg)
	}

	conn, err := h.cfg.Upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.cfg.Logger.Error("streaming: websocket upgrade failed", zap.Error(err))
		return nil, err
	}

	client := newClient(clientCfg.ID, userID, tenantID, clientCfg.Metadata, conn, h)

	h.mu.Lock()
	if h.closed.Load() {
		h.mu.Unlock()
		client.Close()
		return nil, ErrHubClosed
	}
	h.clients[client.ID()] = client
	h.mu.Unlock()

	for _, topic := range clientCfg.InitialTopics {
		client.Subscribe(topic)
	}

	go client.writePump()
	go client.readPump()

	if h.cfg.OnConnect != nil {
		h.cfg.OnConnect(client)
	}

	return client, nil
}

// HTTPHandler returns an http.HandlerFunc that upgrades requests to WebSocket connections.
func (h *Hub) HTTPHandler(opts ...ClientOption) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, _ = h.Upgrade(w, r, opts...)
	}
}

// Broadcast sends a message to all clients subscribed to the given topic.
func (h *Hub) Broadcast(topic string, data any) error {
	if h.closed.Load() {
		return ErrHubClosed
	}

	msg, err := toMessage(topic, data)
	if err != nil {
		return fmt.Errorf("streaming: format message: %w", err)
	}

	h.mu.RLock()
	subscribers, ok := h.topics[topic]
	if !ok || len(subscribers) == 0 {
		h.mu.RUnlock()
		return nil
	}

	// Copy targets to release lock quickly
	targets := make([]*Client, 0, len(subscribers))
	for _, c := range subscribers {
		targets = append(targets, c)
	}
	h.mu.RUnlock()

	for _, c := range targets {
		c.Send(msg)
	}
	return nil
}

// BroadcastAll sends a message to every connected client on the hub.
func (h *Hub) BroadcastAll(data any) error {
	if h.closed.Load() {
		return ErrHubClosed
	}

	msg, err := toMessage("", data)
	if err != nil {
		return fmt.Errorf("streaming: format message: %w", err)
	}

	h.mu.RLock()
	targets := make([]*Client, 0, len(h.clients))
	for _, c := range h.clients {
		targets = append(targets, c)
	}
	h.mu.RUnlock()

	for _, c := range targets {
		c.Send(msg)
	}
	return nil
}

// SendTo sends a message to a specific client by ID.
func (h *Hub) SendTo(clientID string, data any) error {
	if h.closed.Load() {
		return ErrHubClosed
	}

	msg, err := toMessage("", data)
	if err != nil {
		return fmt.Errorf("streaming: format message: %w", err)
	}

	h.mu.RLock()
	client, ok := h.clients[clientID]
	h.mu.RUnlock()

	if !ok {
		return ErrClientNotFound
	}

	if !client.Send(msg) {
		return errors.New("streaming: client send buffer full or closed")
	}
	return nil
}

// ClientCount returns the total number of connected clients.
func (h *Hub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// TopicSubscriberCount returns the number of clients subscribed to a given topic.
func (h *Hub) TopicSubscriberCount(topic string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	subscribers, ok := h.topics[topic]
	if !ok {
		return 0
	}
	return len(subscribers)
}

// Topics returns all currently active topics.
func (h *Hub) Topics() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	res := make([]string, 0, len(h.topics))
	for t := range h.topics {
		res = append(res, t)
	}
	return res
}

// GetClient retrieves a connected client by its ID.
func (h *Hub) GetClient(clientID string) (*Client, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	c, ok := h.clients[clientID]
	return c, ok
}

// CloseClient disconnects a specific client by ID.
func (h *Hub) CloseClient(clientID string) error {
	h.mu.RLock()
	c, ok := h.clients[clientID]
	h.mu.RUnlock()
	if !ok {
		return ErrClientNotFound
	}
	c.Close()
	return nil
}

// addSubscription indexes a client under a topic.
func (h *Hub) addSubscription(client *Client, topic string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	subscribers, ok := h.topics[topic]
	if !ok {
		subscribers = make(map[string]*Client)
		h.topics[topic] = subscribers
	}
	subscribers[client.ID()] = client
}

// removeSubscription removes a client from a topic index.
func (h *Hub) removeSubscription(client *Client, topic string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	subscribers, ok := h.topics[topic]
	if !ok {
		return
	}
	delete(subscribers, client.ID())
	if len(subscribers) == 0 {
		delete(h.topics, topic)
	}
}

// unregisterClient removes a disconnected client from the hub and clean up subscriptions.
func (h *Hub) unregisterClient(client *Client, err error) {
	h.mu.Lock()
	delete(h.clients, client.ID())
	for _, topic := range client.Topics() {
		if subs, ok := h.topics[topic]; ok {
			delete(subs, client.ID())
			if len(subs) == 0 {
				delete(h.topics, topic)
			}
		}
	}
	h.mu.Unlock()

	if h.cfg.OnDisconnect != nil {
		h.cfg.OnDisconnect(client, err)
	}
}

// handleInbound processes an incoming message from a client.
func (h *Hub) handleInbound(client *Client, msg Message) {
	if h.cfg.OnMessage != nil {
		h.cfg.OnMessage(client, msg)
	}
}

// toMessage normalizes data into a Message instance.
func toMessage(topic string, data any) (Message, error) {
	switch v := data.(type) {
	case Message:
		if topic != "" && v.Topic == "" {
			v.Topic = topic
		}
		return v, nil
	case []byte:
		return NewBinaryMessage(topic, v), nil
	case string:
		return NewTextMessage(topic, v), nil
	default:
		return NewJSONMessage(topic, v)
	}
}
