package audit_test

import (
	"context"
	"errors"
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

func TestEventSanitization_TableDriven(t *testing.T) {
	tests := []struct {
		name       string
		fieldName  string
		val        any
		wantMasked bool
	}{
		{"password field is masked", "password", "my-secret-pass", true},
		{"password_hash field is masked", "password_hash", "$2a$10$xyz", true},
		{"api_key field is masked", "api_key", "sk_live_12345", true},
		{"auth_token field is masked", "auth_token", "jwt.token.string", true},
		{"credit_card field is masked", "credit_card", "4111222233334444", true},
		{"ssn field is masked", "ssn", "000-11-2222", true},
		{"secret_key field is masked", "secret_key", "topsecret", true},
		{"email field is not masked", "email", "alice@example.com", false},
		{"username field is not masked", "username", "alice", false},
		{"status field is not masked", "status", "active", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			evt := &audit.Event{
				ID:     "evt-sanitization",
				Action: "test.sanitize",
				Changes: []audit.FieldChange{
					{Field: tc.fieldName, OldValue: tc.val, NewValue: tc.val},
				},
				Metadata: map[string]any{
					tc.fieldName: tc.val,
				},
			}

			sanitized := evt.Sanitize()
			expectedVal := tc.val
			if tc.wantMasked {
				expectedVal = "********"
			}

			if sanitized.Changes[0].NewValue != expectedVal {
				t.Errorf("Changes[%q].NewValue = %v, want %v", tc.fieldName, sanitized.Changes[0].NewValue, expectedVal)
			}
			if sanitized.Metadata[tc.fieldName] != expectedVal {
				t.Errorf("Metadata[%q] = %v, want %v", tc.fieldName, sanitized.Metadata[tc.fieldName], expectedVal)
			}
		})
	}
}

func TestMemorySink_TableDriven(t *testing.T) {
	sink := audit.NewMemorySink()
	defer sink.Close()
	ctx := context.Background()

	tests := []struct {
		name      string
		action    func(t *testing.T)
		wantCount int
	}{
		{
			name: "write first event",
			action: func(t *testing.T) {
				if err := sink.Write(ctx, &audit.Event{ID: "1", Action: "act.1"}); err != nil {
					t.Fatalf("Write: %v", err)
				}
			},
			wantCount: 1,
		},
		{
			name: "write second event",
			action: func(t *testing.T) {
				if err := sink.Write(ctx, &audit.Event{ID: "2", Action: "act.2"}); err != nil {
					t.Fatalf("Write: %v", err)
				}
			},
			wantCount: 2,
		},
		{
			name: "clear removes all events",
			action: func(_ *testing.T) {
				sink.Clear()
			},
			wantCount: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.action(t)
			events := sink.Events()
			if len(events) != tc.wantCount {
				t.Errorf("len(Events()) = %d, want %d", len(events), tc.wantCount)
			}
		})
	}
}

func TestLoggerSink(t *testing.T) {
	log := zap.NewNop()
	sink := audit.NewLoggerSink(log)
	defer sink.Close()

	tests := []struct {
		name  string
		event *audit.Event
	}{
		{
			name: "valid success login event",
			event: &audit.Event{
				ID:        "log-1",
				Timestamp: time.Now(),
				Action:    "auth.login",
				Actor:     audit.Actor{ID: "u1", Type: "user"},
				Status:    audit.StatusSuccess,
			},
		},
		{
			name: "denied permission event",
			event: &audit.Event{
				ID:        "log-2",
				Timestamp: time.Now(),
				Action:    "billing.refund",
				Actor:     audit.Actor{ID: "u2", Type: "user"},
				Status:    audit.StatusDenied,
				Reason:    "missing permission",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := sink.Write(context.Background(), tc.event); err != nil {
				t.Fatalf("LoggerSink.Write: %v", err)
			}
		})
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
		FlushInterval: 10 * time.Second,
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

	req := httptest.NewRequest(http.MethodPost, "/api/test", nil)
	req.Header.Set("X-Tenant-ID", "tenant-alpha")
	req.Header.Set("X-Request-ID", "req-xyz-123")

	tenantHandler := middleware.TenantID()
	reqIDHandler := middleware.RequestID()

	h := reqIDHandler(tenantHandler(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		reqCtx := r.Context()
		reqCtx = audit.WithActor(reqCtx, audit.Actor{
			ID:    "user-99",
			Type:  "user",
			Email: "admin@corp.com",
			Role:  "admin",
		})
		reqCtx = audit.WithMetadata(reqCtx, "ip", "10.0.0.1")

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

func TestAuditMiddleware_TableDriven(t *testing.T) {
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

	tests := []struct {
		name        string
		method      string
		path        string
		headers     map[string]string
		wantAudited bool
		wantStatus  audit.Status
	}{
		{
			name:        "GET request is not audited by mutation middleware",
			method:      http.MethodGet,
			path:        "/success",
			headers:     nil,
			wantAudited: false,
		},
		{
			name:   "POST /success records StatusSuccess with IP and User-Agent",
			method: http.MethodPost,
			path:   "/success",
			headers: map[string]string{
				"User-Agent":      "AuditTestClient/2.0",
				"X-Forwarded-For": "203.0.113.195",
			},
			wantAudited: true,
			wantStatus:  audit.StatusSuccess,
		},
		{
			name:        "POST /denied records StatusDenied",
			method:      http.MethodPost,
			path:        "/denied",
			headers:     nil,
			wantAudited: true,
			wantStatus:  audit.StatusDenied,
		},
		{
			name:        "POST /error records StatusFailure",
			method:      http.MethodPost,
			path:        "/error",
			headers:     nil,
			wantAudited: true,
			wantStatus:  audit.StatusFailure,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mem.Clear()

			req := httptest.NewRequest(tc.method, tc.path, nil)
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}
			w := httptest.NewRecorder()
			auditHandler.ServeHTTP(w, req)

			events := mem.Events()
			if !tc.wantAudited {
				if len(events) != 0 {
					t.Errorf("expected no audit event, got %d", len(events))
				}
				return
			}

			if len(events) != 1 {
				t.Fatalf("expected 1 audit event, got %d", len(events))
			}
			if events[0].Status != tc.wantStatus {
				t.Errorf("event.Status = %v, want %v", events[0].Status, tc.wantStatus)
			}
			if ua, ok := tc.headers["User-Agent"]; ok {
				if events[0].Metadata["user_agent"] != ua {
					t.Errorf("event.Metadata[user_agent] = %v, want %v", events[0].Metadata["user_agent"], ua)
				}
			}
			if ip, ok := tc.headers["X-Forwarded-For"]; ok {
				if events[0].Metadata["client_ip"] != ip {
					t.Errorf("event.Metadata[client_ip] = %v, want %v", events[0].Metadata["client_ip"], ip)
				}
			}
		})
	}
}

func TestEventBuilder_TableDriven(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "EventBuilder WithActor, WithActorUser, WithTenant, and WithReason",
			run: func(t *testing.T) {
				mem := audit.NewMemorySink()
				recorder := audit.NewRecorder(mem)
				ctx := context.Background()

				// 1. Using WithActor
				err := recorder.NewEvent("user.login").
					WithActor(audit.Actor{
						ID:   "act-1",
						Type: "service",
						Name: "worker-svc",
					}).
					WithTenant("tenant-corp").
					WithStatus(audit.StatusSuccess).
					WithReason("mfa verified").
					WithChange("status", "pending", "active").
					Record(ctx)

				if err != nil {
					t.Fatalf("Record error: %v", err)
				}

				events := mem.Events()
				if len(events) != 1 {
					t.Fatalf("expected 1 event, got %d", len(events))
				}
				evt1 := events[0]
				if evt1.Actor.ID != "act-1" || evt1.TenantID != "tenant-corp" || evt1.Reason != "mfa verified" {
					t.Errorf("unexpected event1 properties: %+v", evt1)
				}

				// 2. Using WithActorUser
				err = recorder.NewEvent("user.update").
					WithActorUser("usr-100", "alice@example.com", "Alice", "admin").
					WithStatus(audit.StatusFailure).
					WithReason("permission denied").
					Record(ctx)

				if err != nil {
					t.Fatalf("Record error: %v", err)
				}

				events = mem.Events()
				if len(events) != 2 {
					t.Fatalf("expected 2 events, got %d", len(events))
				}
				evt2 := events[1]
				if evt2.Actor.ID != "usr-100" || evt2.Actor.Email != "alice@example.com" || evt2.Actor.Role != "admin" {
					t.Errorf("unexpected event2 properties: %+v", evt2)
				}

				// Close
				_ = recorder.Close()
			},
		},
		{
			name: "Recorder with nil sink Close returns nil",
			run: func(t *testing.T) {
				rec := audit.NewRecorder(nil)
				if err := rec.Close(); err != nil {
					t.Errorf("expected nil error on Close with nil sink, got %v", err)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, tc.run)
	}
}

type failingSink struct{}

func (failingSink) Write(context.Context, *audit.Event) error {
	return errors.New("failing sink write error")
}

func (failingSink) Close() error {
	return errors.New("failing sink close error")
}

func TestSink_EdgeCases_TableDriven(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "MultiSink error on Write and Close",
			run: func(t *testing.T) {
				ms := audit.NewMultiSink(failingSink{})
				ctx := context.Background()

				if err := ms.Write(ctx, &audit.Event{ID: "test"}); err == nil {
					t.Error("expected MultiSink.Write to fail with failing sink")
				}
				if err := ms.Close(); err == nil {
					t.Error("expected MultiSink.Close to fail with failing sink")
				}
			},
		},
		{
			name: "MemorySink error after Close",
			run: func(t *testing.T) {
				mem := audit.NewMemorySink()
				if err := mem.Close(); err != nil {
					t.Fatalf("Close failed: %v", err)
				}
				if err := mem.Write(context.Background(), &audit.Event{ID: "closed"}); err != audit.ErrSinkClosed {
					t.Errorf("expected ErrSinkClosed, got %v", err)
				}
			},
		},
		{
			name: "AsyncSink defaults, double close, and write after close",
			run: func(t *testing.T) {
				mem := audit.NewMemorySink()
				// Tests zero config defaults: BufferSize, Workers, BatchSize, FlushInterval
				async := audit.NewAsyncSink(mem, audit.AsyncSinkConfig{})

				if err := async.Close(); err != nil {
					t.Fatalf("first Close failed: %v", err)
				}
				// Second Close is safe and idempotent (returns nil)
				if err := async.Close(); err != nil {
					t.Errorf("expected nil on second Close, got %v", err)
				}

				// Write after Close should return ErrSinkClosed
				if err := async.Write(context.Background(), &audit.Event{ID: "after-close"}); err != audit.ErrSinkClosed {
					t.Errorf("expected ErrSinkClosed on Write after Close, got %v", err)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, tc.run)
	}
}
