package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/dhawalhost/gokit/audit"
	"github.com/dhawalhost/gokit/authz"
	"github.com/dhawalhost/gokit/crypto/kms"
	"github.com/dhawalhost/gokit/distlock"
	"github.com/dhawalhost/gokit/errors"
	"github.com/dhawalhost/gokit/idgen"
	"github.com/dhawalhost/gokit/logger"
	"github.com/dhawalhost/gokit/middleware"
	"github.com/dhawalhost/gokit/multitenancy"
	"github.com/dhawalhost/gokit/outbox"
	"github.com/dhawalhost/gokit/response"
	"github.com/dhawalhost/gokit/router"
	"github.com/dhawalhost/gokit/server"
	"go.uber.org/zap"
)

// Invoice represents a billing record.
type Invoice struct {
	ID                 string    `json:"id"`
	TenantID           string    `json:"tenant_id"`
	InvoiceNumber      string    `json:"invoice_number"`
	Amount             float64   `json:"amount"`
	Currency           string    `json:"currency"`
	EncryptedAccountNo string    `json:"encrypted_account_no"` // protected at rest via KMS envelope encryption
	MaskedAccountNo    string    `json:"masked_account_no"`
	CreatedAt          time.Time `json:"created_at"`
}

type CreateInvoiceRequest struct {
	InvoiceNumber  string  `json:"invoice_number"`
	Amount         float64 `json:"amount"`
	Currency       string  `json:"currency"`
	BankAccountNum string  `json:"bank_account_num"`
}

type memoryInvoiceStore struct {
	mu       sync.RWMutex
	invoices map[string][]Invoice // tenantID -> invoices
}

func newMemoryInvoiceStore() *memoryInvoiceStore {
	return &memoryInvoiceStore{
		invoices: make(map[string][]Invoice),
	}
}

func (s *memoryInvoiceStore) Save(inv Invoice) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.invoices[inv.TenantID] = append(s.invoices[inv.TenantID], inv)
}

func (s *memoryInvoiceStore) List(tenantID string) []Invoice {
	s.mu.RLock()
	defer s.mu.RUnlock()
	list := s.invoices[tenantID]
	res := make([]Invoice, len(list))
	copy(res, list)
	return res
}

func main() {
	// 1. Logger
	log, err := logger.New("info", true)
	if err != nil {
		fmt.Printf("failed to initialize logger: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = log.Sync() }()

	// 2. KMS Envelope Encryption
	// In production, master keys reside in AWS KMS, GCP KMS, or Vault.
	masterKey32Bytes := []byte("master-kek-32-bytes-long-secret!")
	keyProvider, err := kms.NewLocalKeyProvider(map[string][]byte{
		"primary-2026": masterKey32Bytes,
	}, "primary-2026")
	if err != nil {
		log.Fatal("failed to initialize KMS key provider", zap.Error(err))
	}
	cipher := kms.NewEnvelopeCipher(keyProvider)

	// 3. Fine-Grained Authorization (RBAC)
	roleMap := authz.NewRoleMap(map[string][]string{
		"admin":   {"*"},
		"finance": {"invoices:*"},
		"viewer":  {"invoices:read"},
	})
	enforcer := authz.NewEnforcer(authz.WithRoleMap(roleMap))

	// 4. Audit Logging (Async Sink with PII auto-masking)
	auditSink := audit.NewAsyncSink(audit.NewLoggerSink(log), audit.AsyncSinkConfig{
		BufferSize:    1024,
		BatchSize:     20,
		FlushInterval: 100 * time.Millisecond,
	})
	defer func() { _ = auditSink.Close() }()
	auditRecorder := audit.NewRecorder(auditSink)

	// 5. Distributed Locking (In-memory for demo; use NewRedisLocker in clustered production)
	locker := distlock.NewInMemoryLocker()

	// 6. Transactional Outbox & Background Relay
	outboxStore := outbox.NewMemoryStore()
	publisher := outbox.PublisherFunc(func(_ context.Context, topic, key string, payload []byte, headers map[string]string) error {
		log.Info("[OUTBOX DISPATCHER] Published message to broker",
			zap.String("topic", topic),
			zap.String("key", key),
			zap.String("payload", string(payload)),
			zap.Any("headers", headers),
		)
		return nil
	})
	relay := outbox.NewRelay(outboxStore, publisher, outbox.RelayConfig{
		BatchSize:    10,
		PollInterval: 250 * time.Millisecond,
	})
	relayCtx, cancelRelay := context.WithCancel(context.Background())
	defer cancelRelay()
	relay.Start(relayCtx)
	defer relay.Stop()

	// In-memory business database
	dbStore := newMemoryInvoiceStore()

	// 7. Router & Middleware Pipeline
	r := router.NewWithMiddleware(
		middleware.RequestID(),
		middleware.Logger(log),
		middleware.Recovery(log),
		multitenancy.RequireTenant(),    // Enforces X-Tenant-ID header
		audit.ContextMiddleware(),       // Injects client_ip and user_agent into audit context
		audit.Middleware(auditRecorder), // Audits mutating HTTP requests (POST, PUT, DELETE)
	)

	// Helper to extract actor from mock header or token
	injectMockSubject := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			role := r.Header.Get("X-User-Role")
			if role == "" {
				role = "finance"
			}
			userID := r.Header.Get("X-User-ID")
			if userID == "" {
				userID = "usr-operator-01"
			}
			sub := authz.Subject{
				ID:       userID,
				TenantID: r.Header.Get("X-Tenant-ID"),
				Roles:    []string{role},
			}
			ctx := authz.WithSubject(r.Context(), sub)
			ctx = audit.WithActor(ctx, audit.Actor{
				ID:   userID,
				Type: "user",
				Role: role,
			})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}

	r.Use(injectMockSubject)

	// 8. Endpoints

	// POST /api/v1/invoices
	r.With(authz.Require(enforcer, "invoices:create")).Post("/api/v1/invoices", func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		tenantID, _ := multitenancy.FromContext(ctx)

		var req CreateInvoiceRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			errors.WriteError(w, r, errors.BadRequest("INVALID_BODY", err.Error()))
			return
		}

		// A. Distributed Lock: Prevent duplicate concurrent invoice creation
		lockKey := fmt.Sprintf("tenant:%s:invoice:%s", tenantID, req.InvoiceNumber)
		lockErr := locker.WithLock(ctx, lockKey, 5*time.Second, func(lockCtx context.Context) error {
			// B. Envelope Encryption: Encrypt bank account number under per-record DEK
			aad := []byte(fmt.Sprintf("tenant:%s:inv:%s", tenantID, req.InvoiceNumber))
			encryptedAccountB64, err := cipher.EncryptString(lockCtx, "primary-2026", req.BankAccountNum, aad)
			if err != nil {
				return fmt.Errorf("envelope encryption: %w", err)
			}

			maskedAccount := "****"
			if len(req.BankAccountNum) >= 4 {
				maskedAccount = "****" + req.BankAccountNum[len(req.BankAccountNum)-4:]
			}

			inv := Invoice{
				ID:                 idgen.NewUUIDv7(),
				TenantID:           tenantID,
				InvoiceNumber:      req.InvoiceNumber,
				Amount:             req.Amount,
				Currency:           req.Currency,
				EncryptedAccountNo: encryptedAccountB64,
				MaskedAccountNo:    maskedAccount,
				CreatedAt:          time.Now().UTC(),
			}

			// C. Transactionally store entity & enqueue outbox event
			dbStore.Save(inv)

			eventPayload, _ := json.Marshal(inv)
			outboxMsg := &outbox.Message{
				Topic:   "invoices.issued",
				Key:     inv.ID,
				Payload: eventPayload,
				Headers: map[string]string{
					"tenant_id":  tenantID,
					"request_id": middleware.RequestIDFromContext(lockCtx),
				},
			}
			_ = outboxStore.Save(lockCtx, outboxMsg)

			// D. Record fine-grained compliance audit event
			_ = auditRecorder.NewEvent("invoice.created").
				WithResource("invoice", inv.ID, inv.InvoiceNumber).
				WithChange("amount", 0, inv.Amount).
				WithChange("currency", "", inv.Currency).
				WithMetadata("masked_account", maskedAccount).
				Record(lockCtx)

			response.Created(w, r, inv)
			return nil
		})

		if lockErr != nil {
			errors.WriteError(w, r, errors.Internal(lockErr.Error()))
		}
	})

	// GET /api/v1/invoices
	r.With(authz.Require(enforcer, "invoices:read")).Get("/api/v1/invoices", func(w http.ResponseWriter, r *http.Request) {
		tenantID, _ := multitenancy.FromContext(r.Context())
		invoices := dbStore.List(tenantID)
		response.Ok(w, r, invoices)
	})

	// 9. Server Lifecycle with Graceful Shutdown
	srv := server.New(
		server.WithAddr(":8080"),
		server.WithReadTimeout(5*time.Second),
		server.WithWriteTimeout(10*time.Second),
	)
	srv.Mount("/", r)

	log.Info("Starting Enterprise Microservice on :8080")
	if err := srv.Run(context.Background()); err != nil {
		log.Fatal("server run error", zap.Error(err))
	}
}
