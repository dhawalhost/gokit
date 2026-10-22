package webhook_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dhawalhost/gokit/webhook"
)

func TestVerifySignature(t *testing.T) {
	secret := []byte("super-secret-signing-key")
	payload := []byte(`{"event":"payment.completed","id":"evt-101"}`)
	nowUnix := strconv.FormatInt(time.Now().UTC().Unix(), 10)
	validSig := webhook.ComputeSignature(payload, nowUnix, secret)

	oldUnix := strconv.FormatInt(time.Now().UTC().Add(-10*time.Minute).Unix(), 10)
	oldSig := webhook.ComputeSignature(payload, oldUnix, secret)

	tests := []struct {
		name      string
		payload   []byte
		signature string
		timestamp string
		secret    []byte
		maxAge    time.Duration
		wantErr   bool
	}{
		{
			name:      "valid signature and fresh timestamp",
			payload:   payload,
			signature: validSig,
			timestamp: nowUnix,
			secret:    secret,
			maxAge:    5 * time.Minute,
			wantErr:   false,
		},
		{
			name:      "tampered payload produces mismatch",
			payload:   []byte(`{"event":"payment.tampered"}`),
			signature: validSig,
			timestamp: nowUnix,
			secret:    secret,
			maxAge:    5 * time.Minute,
			wantErr:   true,
		},
		{
			name:      "expired timestamp triggers replay attack error",
			payload:   payload,
			signature: oldSig,
			timestamp: oldUnix,
			secret:    secret,
			maxAge:    5 * time.Minute,
			wantErr:   true,
		},
		{
			name:      "incorrect secret produces mismatch",
			payload:   payload,
			signature: validSig,
			timestamp: nowUnix,
			secret:    []byte("wrong-key"),
			maxAge:    5 * time.Minute,
			wantErr:   true,
		},
		{
			name:      "missing signature returns error",
			payload:   payload,
			signature: "",
			timestamp: nowUnix,
			secret:    secret,
			maxAge:    5 * time.Minute,
			wantErr:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := webhook.VerifySignature(tc.payload, tc.signature, tc.timestamp, tc.secret, tc.maxAge)
			if tc.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestInboundMiddleware(t *testing.T) {
	secret := []byte("webhook-secret")
	middleware := webhook.InboundMiddleware(secret, 5*time.Minute)

	tests := []struct {
		name       string
		setHeaders func(req *http.Request, payload []byte)
		wantStatus int
	}{
		{
			name: "valid signature passes through to handler",
			setHeaders: func(req *http.Request, payload []byte) {
				ts := strconv.FormatInt(time.Now().UTC().Unix(), 10)
				sig := webhook.ComputeSignature(payload, ts, secret)
				req.Header.Set(webhook.DefaultHeaderSignature, sig)
				req.Header.Set(webhook.DefaultHeaderTimestamp, ts)
			},
			wantStatus: http.StatusOK,
		},
		{
			name: "missing signature returns unauthorized 401",
			setHeaders: func(req *http.Request, payload []byte) {
				ts := strconv.FormatInt(time.Now().UTC().Unix(), 10)
				req.Header.Set(webhook.DefaultHeaderTimestamp, ts)
			},
			wantStatus: http.StatusUnauthorized,
		},
		{
			name: "tampered signature returns unauthorized 401",
			setHeaders: func(req *http.Request, payload []byte) {
				ts := strconv.FormatInt(time.Now().UTC().Unix(), 10)
				req.Header.Set(webhook.DefaultHeaderSignature, "invalid-hex-sig")
				req.Header.Set(webhook.DefaultHeaderTimestamp, ts)
			},
			wantStatus: http.StatusUnauthorized,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			targetHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("handled"))
			})

			handler := middleware(targetHandler)
			payload := []byte(`{"event":"customer.created"}`)

			req := httptest.NewRequest(http.MethodPost, "/webhooks", bytes.NewReader(payload))
			tc.setHeaders(req, payload)

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Errorf("expected status %d, got %d", tc.wantStatus, rec.Code)
			}
		})
	}
}

func TestDispatcher(t *testing.T) {
	secret := []byte("outbound-secret")

	t.Run("successful delivery", func(t *testing.T) {
		var received atomic.Bool
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sig := r.Header.Get(webhook.DefaultHeaderSignature)
			ts := r.Header.Get(webhook.DefaultHeaderTimestamp)
			if sig != "" && ts != "" {
				received.Store(true)
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		dispatcher := webhook.NewDispatcher(webhook.DispatcherConfig{
			Timeout:    2 * time.Second,
			MaxRetries: 1,
		})

		payload := []byte(`{"action":"sync"}`)
		res, err := dispatcher.Dispatch(context.Background(), server.URL, payload, secret)
		if err != nil {
			t.Fatalf("Dispatch error: %v", err)
		}
		if !res.Success {
			t.Fatalf("expected success, got false")
		}
		if !received.Load() {
			t.Errorf("expected headers received by server")
		}
	})

	t.Run("retries on server error and fails gracefully", func(t *testing.T) {
		var attempts atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			attempts.Add(1)
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()

		dispatcher := webhook.NewDispatcher(webhook.DispatcherConfig{
			Timeout:    2 * time.Second,
			MaxRetries: 2,
			BaseDelay:  10 * time.Millisecond,
		})

		res, err := dispatcher.Dispatch(context.Background(), server.URL, []byte("data"), secret)
		if err == nil {
			t.Fatalf("expected error, got nil")
		}
		if res.Success {
			t.Fatalf("expected failure, got success")
		}
		if attempts.Load() != 3 { // 1 initial + 2 retries = 3
			t.Errorf("expected 3 attempts, got %d", attempts.Load())
		}
	})
}

func TestVerifySignature_FutureTimestamp_TableDriven(t *testing.T) {
	secret := []byte("secret")
	payload := []byte(`{"event":"test"}`)
	futureUnix := strconv.FormatInt(time.Now().UTC().Add(10*time.Minute).Unix(), 10)
	futureSig := webhook.ComputeSignature(payload, futureUnix, secret)

	tests := []struct {
		name    string
		maxAge  time.Duration
		wantErr bool
	}{
		{
			name:    "future timestamp beyond maxAge is rejected",
			maxAge:  2 * time.Minute,
			wantErr: true,
		},
		{
			name:    "future timestamp within maxAge is accepted",
			maxAge:  20 * time.Minute,
			wantErr: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := webhook.VerifySignature(payload, futureSig, futureUnix, secret, tc.maxAge)
			if (err != nil) != tc.wantErr {
				t.Fatalf("wantErr=%v, got=%v", tc.wantErr, err)
			}
		})
	}
}

func TestNewDispatcher_Defaults_TableDriven(t *testing.T) {
	tests := []struct {
		name string
		cfg  webhook.DispatcherConfig
	}{
		{
			name: "zero config populates safe defaults",
			cfg:  webhook.DispatcherConfig{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := webhook.NewDispatcher(tc.cfg)
			if d == nil {
				t.Fatal("expected non-nil Dispatcher")
			}
		})
	}
}

func TestInboundMiddleware_DefaultMaxAge_TableDriven(t *testing.T) {
	secret := []byte("secret-key")
	payload := []byte(`{"ping":true}`)
	nowUnix := strconv.FormatInt(time.Now().UTC().Unix(), 10)
	sig := webhook.ComputeSignature(payload, nowUnix, secret)

	tests := []struct {
		name       string
		maxAge     time.Duration
		wantStatus int
	}{
		{
			name:       "maxAge <= 0 defaults to DefaultMaxAge",
			maxAge:     0,
			wantStatus: http.StatusOK,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mw := webhook.InboundMiddleware(secret, tc.maxAge)
			handler := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(payload))
			req.Header.Set("X-Webhook-Signature", sig)
			req.Header.Set("X-Webhook-Timestamp", nowUnix)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)
			if rec.Code != tc.wantStatus {
				t.Fatalf("expected status %d, got %d", tc.wantStatus, rec.Code)
			}
		})
	}
}
