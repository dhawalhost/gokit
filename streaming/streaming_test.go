package streaming_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"

	"github.com/dhawalhost/gokit/streaming"
)

// nonFlusherResponseWriter simulates an HTTP writer that does not support http.Flusher.
type nonFlusherResponseWriter struct {
	http.ResponseWriter
}

func TestSSE_FlusherMissing(t *testing.T) {
	handler := streaming.SSEHandler(func(ctx context.Context, w streaming.EventWriter) error {
		return nil
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/events", nil)

	// Wrap recorder to strip Flusher interface
	handler(&nonFlusherResponseWriter{ResponseWriter: rec}, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "http.Flusher") {
		t.Errorf("expected error message to mention Flusher, got %s", rec.Body.String())
	}
}

func TestSSE_HeadersAndCustomHeaders(t *testing.T) {
	var connected bool
	handler := streaming.SSEHandler(
		func(ctx context.Context, w streaming.EventWriter) error {
			return w.Send(streaming.Event{Data: "hello"})
		},
		streaming.WithSSEHeartbeat(0),
		streaming.WithSSEHeaders(map[string]string{
			"Access-Control-Allow-Origin": "*",
			"X-Custom-Stream":             "test-val",
		}),
		streaming.WithSSELogger(zap.NewNop()),
		streaming.WithSSEOnConnect(func(r *http.Request) {
			connected = true
		}),
	)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/events", nil)

	handler(rec, req)

	if !connected {
		t.Errorf("expected OnConnect hook to be called")
	}
	if rec.Header().Get("Content-Type") != "text/event-stream" {
		t.Errorf("expected Content-Type text/event-stream, got %s", rec.Header().Get("Content-Type"))
	}
	if rec.Header().Get("Cache-Control") != "no-cache, no-transform" {
		t.Errorf("expected Cache-Control no-cache, no-transform, got %s", rec.Header().Get("Cache-Control"))
	}
	if rec.Header().Get("X-Accel-Buffering") != "no" {
		t.Errorf("expected X-Accel-Buffering no, got %s", rec.Header().Get("X-Accel-Buffering"))
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("expected CORS header, got %s", rec.Header().Get("Access-Control-Allow-Origin"))
	}
	if rec.Header().Get("X-Custom-Stream") != "test-val" {
		t.Errorf("expected custom header, got %s", rec.Header().Get("X-Custom-Stream"))
	}
	if !strings.Contains(rec.Body.String(), "data: hello\n\n") {
		t.Errorf("expected payload in body, got %q", rec.Body.String())
	}
}

func TestSSE_EventFormatting(t *testing.T) {
	tests := []struct {
		name     string
		event    streaming.Event
		contains []string
	}{
		{
			name: "full event with retry and id",
			event: streaming.Event{
				ID:      "evt-123",
				Event:   "delta",
				Data:    "token-chunk",
				Retry:   5000 * time.Millisecond,
				Comment: "keepalive-ping",
			},
			contains: []string{
				": keepalive-ping\n",
				"id: evt-123\n",
				"event: delta\n",
				"retry: 5000\n",
				"data: token-chunk\n\n",
			},
		},
		{
			name: "multiline data splits correctly",
			event: streaming.Event{
				Event: "code",
				Data:  "package main\n\nfunc main() {\n}\n",
			},
			contains: []string{
				"event: code\n",
				"data: package main\n",
				"data: \n",
				"data: func main() {\n",
				"data: }\n",
				"data: \n\n",
			},
		},
		{
			name: "bytes data",
			event: streaming.Event{
				Data: []byte("raw-bytes\nsecond-line"),
			},
			contains: []string{
				"data: raw-bytes\n",
				"data: second-line\n\n",
			},
		},
		{
			name: "struct JSON data",
			event: streaming.Event{
				Event: "metric",
				Data: map[string]int{
					"tokens": 42,
				},
			},
			contains: []string{
				"event: metric\n",
				`data: {"tokens":42}` + "\n\n",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/events", nil)

			handler := streaming.SSEHandler(func(ctx context.Context, w streaming.EventWriter) error {
				return w.Send(tc.event)
			}, streaming.WithSSEHeartbeat(0))

			handler(rec, req)

			body := rec.Body.String()
			for _, expected := range tc.contains {
				if !strings.Contains(body, expected) {
					t.Errorf("expected body to contain %q, but got %q", expected, body)
				}
			}
		})
	}
}

func TestSSE_SendJSONAndComment(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/events", nil)

	handler := streaming.SSEHandler(func(ctx context.Context, w streaming.EventWriter) error {
		if err := w.Comment("initial-handshake"); err != nil {
			return err
		}
		if err := w.SendJSON("status", map[string]string{"state": "ready"}); err != nil {
			return err
		}
		w.Flush()
		if w.Context() == nil {
			return errors.New("expected non-nil context")
		}
		return nil
	}, streaming.WithSSEHeartbeat(0))

	handler(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, ": initial-handshake\n\n") {
		t.Errorf("expected comment, got %q", body)
	}
	if !strings.Contains(body, "event: status\n") || !strings.Contains(body, `data: {"state":"ready"}`+"\n\n") {
		t.Errorf("expected json status event, got %q", body)
	}
}

func TestSSE_HeartbeatTicker(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/events", nil)

	ctx, cancel := context.WithCancel(context.Background())
	req = req.WithContext(ctx)

	handler := streaming.SSEHandler(func(c context.Context, w streaming.EventWriter) error {
		// Wait for at least one heartbeat
		time.Sleep(35 * time.Millisecond)
		cancel()
		return nil
	}, streaming.WithSSEHeartbeat(10*time.Millisecond))

	handler(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, ": ping\n\n") {
		t.Errorf("expected heartbeat ping in output, got %q", body)
	}
}

func TestSSE_ClientDisconnectHook(t *testing.T) {
	var disconnectErr error
	var disconnected bool

	handler := streaming.SSEHandler(
		func(ctx context.Context, w streaming.EventWriter) error {
			<-ctx.Done()
			return ctx.Err()
		},
		streaming.WithSSEHeartbeat(0),
		streaming.WithSSEOnDisconnect(func(r *http.Request, err error) {
			disconnected = true
			disconnectErr = err
		}),
	)

	rec := httptest.NewRecorder()
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/events", nil).WithContext(ctx)

	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	handler(rec, req)

	if !disconnected {
		t.Fatalf("expected OnDisconnect hook to run")
	}
	if !errors.Is(disconnectErr, context.Canceled) {
		t.Errorf("expected disconnect error to be context.Canceled, got %v", disconnectErr)
	}
}

func TestSSE_JSONMarshalError(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/events", nil)

	var sendErr error
	handler := streaming.SSEHandler(func(ctx context.Context, w streaming.EventWriter) error {
		// Functions cannot be JSON-marshaled
		sendErr = w.Send(streaming.Event{Data: func() {}})
		return sendErr
	}, streaming.WithSSEHeartbeat(0))

	handler(rec, req)

	if sendErr == nil || !strings.Contains(sendErr.Error(), "marshal event data") {
		t.Fatalf("expected marshal error, got %v", sendErr)
	}
}

// ---------------------------------------------------------------------
// WebSocket Hub Tests
// ---------------------------------------------------------------------

func TestWebSocket_UpgradeAndConnect(t *testing.T) {
	var connected atomic.Bool
	var disconnected atomic.Bool

	hub := streaming.NewHub(
		streaming.WithHubLogger(zap.NewNop()),
		streaming.WithOnConnect(func(c *streaming.Client) {
			connected.Store(true)
		}),
		streaming.WithOnDisconnect(func(c *streaming.Client, err error) {
			disconnected.Store(true)
		}),
	)

	server := httptest.NewServer(hub.HTTPHandler(streaming.WithInitialTopics("general")))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	wsConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("failed to dial websocket: %v", err)
	}

	time.Sleep(20 * time.Millisecond)

	if !connected.Load() {
		t.Errorf("expected OnConnect to be triggered")
	}
	if hub.ClientCount() != 1 {
		t.Errorf("expected ClientCount 1, got %d", hub.ClientCount())
	}
	if hub.TopicSubscriberCount("general") != 1 {
		t.Errorf("expected 1 subscriber for topic 'general', got %d", hub.TopicSubscriberCount("general"))
	}

	_ = wsConn.Close()
	time.Sleep(30 * time.Millisecond)

	if !disconnected.Load() {
		t.Errorf("expected OnDisconnect to be triggered")
	}
	if hub.ClientCount() != 0 {
		t.Errorf("expected ClientCount 0, got %d", hub.ClientCount())
	}
	if hub.TopicSubscriberCount("general") != 0 {
		t.Errorf("expected 0 subscribers after disconnect, got %d", hub.TopicSubscriberCount("general"))
	}
}

func TestWebSocket_Auth(t *testing.T) {
	hub := streaming.NewHub(
		streaming.WithAuthFunc(func(r *http.Request) (string, string, map[string]string, error) {
			token := r.URL.Query().Get("token")
			if token != "secret-token" {
				return "", "", nil, errors.New("invalid token")
			}
			return "user-99", "tenant-alpha", map[string]string{"role": "admin"}, nil
		}),
	)

	server := httptest.NewServer(hub.HTTPHandler())
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	// 1. Unauthorized attempt
	_, resp, err := websocket.DefaultDialer.Dial(wsURL+"?token=bad", nil)
	if err == nil {
		t.Fatalf("expected dial to fail with invalid token")
	}
	if resp != nil && resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected status 401 Unauthorized, got %d", resp.StatusCode)
	}

	// 2. Authorized attempt
	conn, _, err := websocket.DefaultDialer.Dial(wsURL+"?token=secret-token", nil)
	if err != nil {
		t.Fatalf("expected dial to succeed with valid token, got %v", err)
	}
	defer conn.Close()

	time.Sleep(20 * time.Millisecond)
	if hub.ClientCount() != 1 {
		t.Fatalf("expected 1 client connected")
	}

	topics := hub.Topics()
	if len(topics) != 0 {
		t.Errorf("expected 0 topics, got %v", topics)
	}

	// Fetch through Broadcast or iteration
	hub.BroadcastAll("find-client")
	// Test sending Message struct directly to Broadcast
	_ = hub.Broadcast("audit", streaming.NewTextMessage("", "audit-event"))

	// Retrieve authed client
	hubMuClients := hub.ClientCount()
	if hubMuClients != 1 {
		t.Fatalf("expected 1 client")
	}

	// Trigger upgrade with known client ID to test client methods
	clientCh := make(chan *streaming.Client, 1)
	hub2 := streaming.NewHub(
		streaming.WithAuthFunc(func(r *http.Request) (string, string, map[string]string, error) {
			return "user-42", "tenant-omega", map[string]string{"env": "prod"}, nil
		}),
		streaming.WithOnConnect(func(c *streaming.Client) {
			clientCh <- c
		}),
	)
	s2 := httptest.NewServer(hub2.HTTPHandler(streaming.WithClientID("known-id")))
	defer s2.Close()

	u2 := "ws" + strings.TrimPrefix(s2.URL, "http")
	c2Conn, _, err := websocket.DefaultDialer.Dial(u2, nil)
	if err != nil {
		t.Fatalf("dial s2: %v", err)
	}
	defer c2Conn.Close()

	var capturedClient *streaming.Client
	select {
	case capturedClient = <-clientCh:
	case <-time.After(1 * time.Second):
		t.Fatalf("timed out waiting for OnConnect")
	}

	if capturedClient.UserID() != "user-42" {
		t.Errorf("expected UserID user-42, got %s", capturedClient.UserID())
	}
	if capturedClient.TenantID() != "tenant-omega" {
		t.Errorf("expected TenantID tenant-omega, got %s", capturedClient.TenantID())
	}

	// Test empty topic edge cases
	capturedClient.Subscribe("")
	capturedClient.Unsubscribe("")

	// Subscribe to a topic and verify Topics()
	capturedClient.Subscribe("alerts")
	if len(capturedClient.Topics()) != 1 || capturedClient.Topics()[0] != "alerts" {
		t.Errorf("expected 1 topic 'alerts', got %v", capturedClient.Topics())
	}

	// Test client send after closing
	capturedClient.Close()
	if capturedClient.Send(streaming.NewTextMessage("alerts", "post-close")) {
		t.Errorf("expected Send to return false after Close()")
	}
}

func TestWebSocket_PubSubAndBroadcast(t *testing.T) {
	hub := streaming.NewHub()
	server := httptest.NewServer(hub.HTTPHandler())
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	conn1, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial client 1: %v", err)
	}
	defer conn1.Close()

	conn2, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial client 2: %v", err)
	}
	defer conn2.Close()

	time.Sleep(20 * time.Millisecond)

	// Direct introspection
	if hub.ClientCount() != 2 {
		t.Fatalf("expected 2 clients, got %d", hub.ClientCount())
	}

	// Test topics directly using Client API
	serverHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("id")
		_, err := hub.Upgrade(w, r,
			streaming.WithClientID(id),
			streaming.WithClientMetadata("agent", "test"),
		)
		if err != nil {
			t.Errorf("upgrade failed: %v", err)
		}
	})

	server2 := httptest.NewServer(serverHandler)
	defer server2.Close()
	wsURL2 := "ws" + strings.TrimPrefix(server2.URL, "http")

	c1, _, err := websocket.DefaultDialer.Dial(wsURL2+"?id=client-1", nil)
	if err != nil {
		t.Fatalf("dial c1: %v", err)
	}
	defer c1.Close()

	c2, _, err := websocket.DefaultDialer.Dial(wsURL2+"?id=client-2", nil)
	if err != nil {
		t.Fatalf("dial c2: %v", err)
	}
	defer c2.Close()

	time.Sleep(20 * time.Millisecond)

	client1, ok := hub.GetClient("client-1")
	if !ok {
		t.Fatalf("expected client-1 to exist")
	}
	client2, ok := hub.GetClient("client-2")
	if !ok {
		t.Fatalf("expected client-2 to exist")
	}

	if client1.ID() != "client-1" || client1.Metadata()["agent"] != "test" {
		t.Errorf("unexpected client1 metadata: %v", client1.Metadata())
	}

	// Subscribe client 1 to "sports" and "news"
	client1.Subscribe("sports")
	client1.Subscribe("news")
	if !client1.IsSubscribed("sports") {
		t.Errorf("expected client1 to be subscribed to sports")
	}

	// Subscribe client 2 to "news"
	client2.Subscribe("news")

	if hub.TopicSubscriberCount("sports") != 1 {
		t.Errorf("expected 1 subscriber for sports, got %d", hub.TopicSubscriberCount("sports"))
	}
	if hub.TopicSubscriberCount("news") != 2 {
		t.Errorf("expected 2 subscribers for news, got %d", hub.TopicSubscriberCount("news"))
	}

	// Broadcast to "sports" -> only client 1 should receive
	err = hub.Broadcast("sports", "goal scored!")
	if err != nil {
		t.Fatalf("broadcast sports: %v", err)
	}

	_ = c1.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	msgType, payload, err := c1.ReadMessage()
	if err != nil {
		t.Fatalf("c1 failed to read sports message: %v", err)
	}
	if msgType != websocket.TextMessage || string(payload) != "goal scored!" {
		t.Errorf("unexpected payload: %s", string(payload))
	}

	// Broadcast to "news" with JSON payload -> both client 1 and 2 receive
	type NewsItem struct {
		Headline string `json:"headline"`
	}
	err = hub.Broadcast("news", NewsItem{Headline: "Market Rally"})
	if err != nil {
		t.Fatalf("broadcast news: %v", err)
	}

	_ = c1.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	_, p1, err := c1.ReadMessage()
	if err != nil {
		t.Fatalf("c1 read news: %v", err)
	}
	var n1 NewsItem
	if err := json.Unmarshal(p1, &n1); err != nil || n1.Headline != "Market Rally" {
		t.Errorf("c1 unexpected news: %s", string(p1))
	}

	_ = c2.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	_, p2, err := c2.ReadMessage()
	if err != nil {
		t.Fatalf("c2 read news: %v", err)
	}
	var n2 NewsItem
	if err := json.Unmarshal(p2, &n2); err != nil || n2.Headline != "Market Rally" {
		t.Errorf("c2 unexpected news: %s", string(p2))
	}

	// Direct SendTo client 2
	err = hub.SendTo("client-2", []byte("direct-whisper"))
	if err != nil {
		t.Fatalf("send to client 2: %v", err)
	}
	_ = c2.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	_, pWhisper, err := c2.ReadMessage()
	if err != nil || string(pWhisper) != "direct-whisper" {
		t.Errorf("c2 failed to receive whisper: %v, payload: %s", err, string(pWhisper))
	}

	// SendTo non-existent client
	err = hub.SendTo("non-existent-client", "hello")
	if !errors.Is(err, streaming.ErrClientNotFound) {
		t.Errorf("expected ErrClientNotFound, got %v", err)
	}

	// Unsubscribe client 1 from sports
	client1.Unsubscribe("sports")
	if client1.IsSubscribed("sports") {
		t.Errorf("expected client1 to no longer be subscribed to sports")
	}
	if hub.TopicSubscriberCount("sports") != 0 {
		t.Errorf("expected 0 subscribers for sports after unsubscribe, got %d", hub.TopicSubscriberCount("sports"))
	}

	// Broadcast to empty topic
	err = hub.Broadcast("sports", "should not error even with 0 subscribers")
	if err != nil {
		t.Errorf("expected nil error on empty topic, got %v", err)
	}

	// Close a specific client
	err = hub.CloseClient("client-1")
	if err != nil {
		t.Errorf("expected nil error closing client-1, got %v", err)
	}
	err = hub.CloseClient("unknown")
	if !errors.Is(err, streaming.ErrClientNotFound) {
		t.Errorf("expected ErrClientNotFound closing unknown client, got %v", err)
	}
}

func TestWebSocket_InboundClientMessage(t *testing.T) {
	var receivedMsg streaming.Message
	var receivedClient *streaming.Client
	var wg sync.WaitGroup
	wg.Add(1)

	hub := streaming.NewHub(
		streaming.WithOnMessage(func(client *streaming.Client, msg streaming.Message) {
			receivedClient = client
			receivedMsg = msg
			wg.Done()
		}),
	)

	server := httptest.NewServer(hub.HTTPHandler())
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close()

	time.Sleep(20 * time.Millisecond)

	err = conn.WriteMessage(websocket.TextMessage, []byte("inbound-ping"))
	if err != nil {
		t.Fatalf("write inbound message: %v", err)
	}

	wg.Wait()

	if string(receivedMsg.Payload) != "inbound-ping" {
		t.Errorf("expected inbound-ping, got %s", string(receivedMsg.Payload))
	}
	if receivedClient == nil {
		t.Errorf("expected non-nil client")
	}
}

func TestWebSocket_SlowClient_Drop(t *testing.T) {
	hub := streaming.NewHub(
		streaming.WithClientBufferSize(2),
		streaming.WithSlowClientPolicy(streaming.SlowClientDrop),
	)

	server := httptest.NewServer(hub.HTTPHandler())
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close()

	time.Sleep(20 * time.Millisecond)

	// Send more than buffer size without client reading
	for i := 0; i < 50; i++ {
		_ = hub.BroadcastAll(fmt.Sprintf("msg-%d", i))
	}

	// Ensure connection remains alive under SlowClientDrop policy
	time.Sleep(20 * time.Millisecond)
	if hub.ClientCount() != 1 {
		t.Errorf("expected client to remain connected under SlowClientDrop, got %d", hub.ClientCount())
	}
}

func TestWebSocket_SlowClient_Disconnect(t *testing.T) {
	hub := streaming.NewHub(
		streaming.WithClientBufferSize(1),
		streaming.WithSlowClientPolicy(streaming.SlowClientDisconnect),
	)

	server := httptest.NewServer(hub.HTTPHandler())
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close()

	time.Sleep(20 * time.Millisecond)

	// Flood messages to trigger overflow and disconnection
	for i := 0; i < 20; i++ {
		_ = hub.BroadcastAll(fmt.Sprintf("flood-%d", i))
	}

	time.Sleep(50 * time.Millisecond)
	if hub.ClientCount() != 0 {
		t.Errorf("expected client to be disconnected under SlowClientDisconnect, got %d", hub.ClientCount())
	}
}

func TestWebSocket_StopAndLifecycle(t *testing.T) {
	hub := streaming.NewHub()
	server := httptest.NewServer(hub.HTTPHandler())
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	time.Sleep(20 * time.Millisecond)

	// Test Start(ctx) cancellation
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan error, 1)
	go func() {
		stopped <- hub.Start(ctx)
	}()

	cancel()
	select {
	case err := <-stopped:
		if err != nil {
			t.Errorf("expected nil error on stop, got %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("hub.Start did not terminate on context cancel")
	}

	// Idempotent Stop
	if err := hub.Stop(); err != nil {
		t.Errorf("expected nil error on repeated stop, got %v", err)
	}

	// Post-stop operations return ErrHubClosed
	if err := hub.Broadcast("topic", "data"); !errors.Is(err, streaming.ErrHubClosed) {
		t.Errorf("expected ErrHubClosed on Broadcast, got %v", err)
	}
	if err := hub.BroadcastAll("data"); !errors.Is(err, streaming.ErrHubClosed) {
		t.Errorf("expected ErrHubClosed on BroadcastAll, got %v", err)
	}
	if err := hub.SendTo("any", "data"); !errors.Is(err, streaming.ErrHubClosed) {
		t.Errorf("expected ErrHubClosed on SendTo, got %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	_, err = hub.Upgrade(rec, req)
	if !errors.Is(err, streaming.ErrHubClosed) {
		t.Errorf("expected ErrHubClosed on Upgrade, got %v", err)
	}
}

func TestWebSocket_MessageConstructors(t *testing.T) {
	txt := streaming.NewTextMessage("room-1", "hello text")
	if txt.Type != streaming.MessageTypeText || txt.Topic != "room-1" || string(txt.Payload) != "hello text" {
		t.Errorf("unexpected text message: %+v", txt)
	}

	bin := streaming.NewBinaryMessage("room-2", []byte{0x01, 0x02})
	if bin.Type != streaming.MessageTypeBinary || bin.Topic != "room-2" || len(bin.Payload) != 2 {
		t.Errorf("unexpected binary message: %+v", bin)
	}

	jsonMsg, err := streaming.NewJSONMessage("room-3", map[string]int{"count": 10})
	if err != nil {
		t.Fatalf("failed to create JSON message: %v", err)
	}
	if jsonMsg.Type != streaming.MessageTypeText || !strings.Contains(string(jsonMsg.Payload), `"count":10`) {
		t.Errorf("unexpected json message: %s", string(jsonMsg.Payload))
	}

	// Error creating JSON message with invalid type
	_, err = streaming.NewJSONMessage("room-err", make(chan int))
	if err == nil {
		t.Errorf("expected JSON marshal error for channel")
	}
}

func TestWebSocket_Options(t *testing.T) {
	hub := streaming.NewHub(
		streaming.WithWriteWait(5*time.Second),
		streaming.WithPongWait(30*time.Second),
		streaming.WithPingPeriod(15*time.Second),
		streaming.WithMaxMessageSize(1024*1024),
		streaming.WithCheckOrigin(func(r *http.Request) bool {
			return r.Header.Get("Origin") == "https://trusted.example.com"
		}),
	)

	server := httptest.NewServer(hub.HTTPHandler())
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	// Disallowed origin
	header := http.Header{}
	header.Set("Origin", "https://evil.example.com")
	_, resp, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err == nil {
		t.Fatalf("expected dial to fail for rejected origin")
	}
	if resp != nil && resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden for bad origin, got %d", resp.StatusCode)
	}

	// Allowed origin
	header.Set("Origin", "https://trusted.example.com")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		t.Fatalf("expected dial to succeed for trusted origin, got %v", err)
	}
	defer conn.Close()
}
