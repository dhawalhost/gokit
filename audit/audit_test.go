package audit_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"go.uber.org/zap"

	"github.com/dhawalhost/gokit/audit"
	"github.com/dhawalhost/gokit/middleware"
)

func TestEventSanitization(t *testing.T) {
	evt := &audit.Event{
		ID:     "evt-123",
		Action: "user.password_reset",
		Actor: audit.Actor{
			ID:   "usr-1",
			Type: "user",
		},
		Changes: []audit.FieldChange{
			{Field: "email", OldValue: "old@example.com", NewValue: "new@example.com"},
			{Field: "password_hash", OldValue: "$2a$10$xyz", NewValue: "$2a$10$abc"},
			{Field: "api_key", OldValue: "old-key", NewValue: "new-key"},
		},
		Metadata: map[string]any{
			"normal_key": "safe_value",
			"auth_token": "bearer-secret-token",
			"secret_key": "supersecret",
		},
	}

	sanitized := evt.Sanitize()

	// Verify original is untouched
	if evt.Changes[1].NewValue != "$2a$10$abc" {
		t.Error("original event was mutated")
	}

	// Verify sensitive changes masked
	if sanitized.Changes[0].NewValue != "new@example.com" {
		t.Errorf("expected email to remain unchanged, got %v", sanitized.Changes[0].NewValue)
	}
	if sanitized.Changes[1].NewValue != "********" || sanitized.Changes[1].OldValue != "********" {
		t.Errorf("expected password_hash to be masked, got %v", sanitized.Changes[1].NewValue)
	}
	if sanitized.Changes[2].NewValue != "********" {
		t.Errorf("expected api_key to be masked, got %v", sanitized.Changes[2].NewValue)
	}

	// Verify sensitive metadata masked
	if sanitized.Metadata["normal_key"] != "safe_value" {
		t.Errorf("expected normal_key safe_value, got %v", sanitized.Metadata["normal_key"])
	}
	if sanitized.Metadata["auth_token"] != "********" {
		t.Errorf("expected auth_token to be masked, got %v", sanitized.Metadata["auth_token"])
	}
	if sanitized.Metadata["secret_key"] != "********" {
		t.Errorf("expected secret_key to be masked, got %v", sanitized.Metadata["secret_key"])
	}
}

func TestMemorySink(t *testing.T) {
	sink := audit.NewMemorySink()
	defer sink.Close()

	ctx := context.Background()
	e1 := &audit.Event{ID: "1", Action: "act.1"}
	e2 := &audit.Event{ID: "2", Action: "act.2"}

	if err := sink.Write(ctx, e1); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := sink.Write(ctx, e2); err != nil {
		t.Fatalf("Write: %v", err)
	}

	events := sink.Events()
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].ID != "1" || events[1].ID != "2" {
		t.Errorf("unexpected event IDs: %v, %v", events[0].ID, events[1].ID)
	}

	sink.Clear()
	if len(sink.Events()) != 0 {
		t.Errorf("expected 0 events after Clear, got %d", len(sink.Events()))
	}

	_ = sink.Close()
	if err := sink.Write(ctx, e1); err != audit.ErrSinkClosed {
		t.Errorf("expected ErrSinkClosed, got %v", err)
	}
}

func TestLoggerSink(t *testing.T) {
	log := zap.NewNop()
	sink := audit.NewLoggerSink(log)
	defer sink.Close()

	evt := &audit.Event{
		ID:        "log-1",
		Timestamp: time.Now(),
		Action:    "auth.login",
		Actor:     audit.Actor{ID: "u1", Type: "user"},
		Status:    audit.StatusSuccess,
	}

	if err := sink.Write(context.Background(), evt); err != nil {
		t.Fatalf("LoggerSink.Write: %v", err)
	}
}

func TestMultiSink(t *testing.T) {
	s1 := audit.NewMemorySink()
	s2 := audit.NewMemorySink()
	multi := audit.NewMultiSink(s1, s2)
	defer multi.Close()

	evt := &audit.Event{ID: "multi-1", Action: "multi.action"}
	if err := multi.Write(context.Background(), evt); err != nil {
		t.Fatalf("MultiSink.Write: %v", err)
	}

	if len(s1.Events()) != 1 || len(s2.Events()) != 1 {
		t.Errorf("expected event in both sinks, got s1=%d s2=%d", len(s1.Events()), len(s2.Events()))
	}
}

func TestAsyncSink_FlushAndClose(t *testing.T) {
	mem := audit.NewMemorySink()
	async := audit.NewAsyncSink(mem, audit.AsyncSinkConfig{
		BufferSize:    100,
		Workers:       2,
		BatchSize:     10,
		FlushInterval: 50 * time.Millisecond,
	})

	ctx := context.Background()
	total := 25
	for i := 0; i < total; i++ {
		if err := async.Write(ctx, &audit.Event{ID: "async-test"}); err != nil {
			t.Fatalf("Write %d: %v", i, err)
		}
	}

	// Close flushes all queued items gracefully
	if err := async.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	events := mem.Events()
	if len(events) != total {
		t.Fatalf("expected %d events flushed, got %d", total, len(events))
	}
}

func TestAsyncSink_DropOnFull(t *testing.T) {
	mem := audit.NewMemorySink()
	var droppedCount int
	var mu sync.Mutex

	async := audit.NewAsyncSink(mem, audit.AsyncSinkConfig{
		BufferSize:    2,
		Workers:       1,
		BatchSize:     10,
		FlushInterval: 10 * time.Second, // don't flush automatically during test
		DropOnFull:    true,
		OnDrop: func(_ *audit.Event) {
			mu.Lock()
			droppedCount++
			mu.Unlock()
		},
	})

	ctx := context.Background()
	for i := 0; i < 10; i++ {
		_ = async.Write(ctx, &audit.Event{ID: "overflow"})
	}

	mu.Lock()
	dropped := droppedCount
	mu.Unlock()

	if dropped == 0 {
		t.Error("expected at least some dropped events when queue is saturated with DropOnFull=true")
	}

	_ = async.Close()
}

func TestRecorder_EnrichmentAndBuilder(t *testing.T) {
	mem := audit.NewMemorySink()
	rec := audit.NewRecorder(mem)
	defer rec.Close()

	// 1. Context with TenantID, RequestID, Actor, and custom Metadata
	req := httptest.NewRequest(http.MethodPost, "/api/test", nil)
	req.Header.Set("X-Tenant-ID", "tenant-alpha")
	req.Header.Set("X-Request-ID", "req-xyz-123")

	ctx := req.Context()
	// Simulate middleware extraction
	ctx = context.WithValue(ctx, struct{}{}, nil) // dummy
	tenantHandler := middleware.TenantID()
	reqIDHandler := middleware.RequestID()

	// Wrap in handlers to populate context
	h := reqIDHandler(tenantHandler(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		reqCtx := r.Context()
		reqCtx = audit.WithActor(reqCtx, audit.Actor{
			ID:    "user-99",
			Type:  "user",
			Email: "admin@corp.com",
			Role:  "admin",
		})
		reqCtx = audit.WithMetadata(reqCtx, "ip", "10.0.0.1")

		// Record using fluent builder
		err := rec.NewEvent("billing.plan.upgrade").
			WithResource("subscription", "sub-555", "Enterprise Plan").
			WithStatus(audit.StatusSuccess).
			WithChange("plan", "pro", "enterprise").
			WithMetadata("custom_field", "value").
			Record(reqCtx)
		if err != nil {
			t.Fatalf("Record: %v", err)
		}
	})))

	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	events := mem.Events()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	e := events[0]
	if e.ID == "" {
		t.Error("expected ID to be generated")
	}
	if e.Timestamp.IsZero() {
		t.Error("expected non-zero Timestamp")
	}
	if e.TenantID != "tenant-alpha" {
		t.Errorf("expected TenantID tenant-alpha, got %q", e.TenantID)
	}
	if e.Actor.ID != "user-99" || e.Actor.Email != "admin@corp.com" {
		t.Errorf("unexpected actor: %+v", e.Actor)
	}
	if e.Resource.Type != "subscription" || e.Resource.ID != "sub-555" {
		t.Errorf("unexpected resource: %+v", e.Resource)
	}
	if len(e.Changes) != 1 || e.Changes[0].Field != "plan" {
		t.Errorf("unexpected changes: %+v", e.Changes)
	}
	if e.Metadata["request_id"] != "req-xyz-123" {
		t.Errorf("expected request_id metadata to be enriched, got %v", e.Metadata["request_id"])
	}
	if e.Metadata["ip"] != "10.0.0.1" || e.Metadata["custom_field"] != "value" {
		t.Errorf("unexpected metadata: %+v", e.Metadata)
	}
}

func TestRecorder_JWTClaimsFallback(t *testing.T) {
	mem := audit.NewMemorySink()
	rec := audit.NewRecorder(mem)
	defer rec.Close()

	// Setup context with JWT claims via middleware
	cfg := middleware.JWTConfig{
		SecretKey: []byte("super-secret-key-32-bytes-long!"),
	}
	claims := jwt.MapClaims{
		"sub":   "jwt-user-42",
		"email": "jwt@example.com",
		"exp":   time.Now().Add(time.Hour).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenStr, _ := token.SignedString(cfg.SecretKey)

	req := httptest.NewRequest(http.MethodPut, "/resource", nil)
	req.Header.Set("Authorization", "Bearer "+tokenStr)

	h := middleware.JWT(cfg)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		err := rec.NewEvent("item.update").
			WithResource("item", "it-1").
			Record(r.Context())
		if err != nil {
			t.Fatalf("Record: %v", err)
		}
	}))

	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	events := mem.Events()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Actor.ID != "jwt-user-42" || events[0].Actor.Email != "jwt@example.com" {
		t.Errorf("expected actor from JWT claims, got %+v", events[0].Actor)
	}
}

func TestAuditMiddleware(t *testing.T) {
	mem := audit.NewMemorySink()
	rec := audit.NewRecorder(mem)
	defer rec.Close()

	auditHandler := audit.ContextMiddleware()(audit.Middleware(rec)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/success":
			w.WriteHeader(http.StatusCreated)
		case "/denied":
			w.WriteHeader(http.StatusForbidden)
		case "/error":
			w.WriteHeader(http.StatusInternalServerError)
		default:
			w.WriteHeader(http.StatusOK)
		}
	})))

	// 1. GET requests should NOT be audited by automatic middleware
	reqGet := httptest.NewRequest(http.MethodGet, "/success", nil)
	wGet := httptest.NewRecorder()
	auditHandler.ServeHTTP(wGet, reqGet)
	if len(mem.Events()) != 0 {
		t.Errorf("expected GET not to be audited, got %d events", len(mem.Events()))
	}

	// 2. POST /success
	reqPost := httptest.NewRequest(http.MethodPost, "/success", nil)
	reqPost.Header.Set("User-Agent", "TestClient/1.0")
	reqPost.Header.Set("X-Forwarded-For", "203.0.113.195")
	wPost := httptest.NewRecorder()
	auditHandler.ServeHTTP(wPost, reqPost)

	events := mem.Events()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Status != audit.StatusSuccess {
		t.Errorf("expected StatusSuccess, got %v", events[0].Status)
	}
	if events[0].Metadata["user_agent"] != "TestClient/1.0" {
		t.Errorf("expected User-Agent in metadata, got %v", events[0].Metadata["user_agent"])
	}
	if events[0].Metadata["client_ip"] != "203.0.113.195" {
		t.Errorf("expected client_ip in metadata, got %v", events[0].Metadata["client_ip"])
	}

	// 3. POST /denied
	mem.Clear()
	reqDenied := httptest.NewRequest(http.MethodPost, "/denied", nil)
	wDenied := httptest.NewRecorder()
	auditHandler.ServeHTTP(wDenied, reqDenied)
	if events := mem.Events(); len(events) != 1 || events[0].Status != audit.StatusDenied {
		t.Errorf("expected StatusDenied, got %+v", events)
	}

	// 4. POST /error
	mem.Clear()
	reqError := httptest.NewRequest(http.MethodPost, "/error", nil)
	wError := httptest.NewRecorder()
	auditHandler.ServeHTTP(wError, reqError)
	if events := mem.Events(); len(events) != 1 || events[0].Status != audit.StatusFailure {
		t.Errorf("expected StatusFailure, got %+v", events)
	}
}
