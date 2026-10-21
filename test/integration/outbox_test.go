//go:build integration
// +build integration

package integration_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/dhawalhost/gokit/config"
	"github.com/dhawalhost/gokit/database"
	"github.com/dhawalhost/gokit/outbox"
	"gorm.io/gorm"
)

type recordedPublish struct {
	topic   string
	key     string
	payload []byte
}

type safePublisher struct {
	mu        sync.Mutex
	publishes []recordedPublish
	count     int64
}

func (p *safePublisher) Publish(_ context.Context, topic, key string, payload []byte, _ map[string]string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.publishes = append(p.publishes, recordedPublish{
		topic:   topic,
		key:     key,
		payload: payload,
	})
	atomic.AddInt64(&p.count, 1)
	return nil
}

func TestOutbox_LivePostgresConcurrentDispatch(t *testing.T) {
	ctx := context.Background()
	dsn := getTestDSN(t)

	db, err := database.New(ctx, config.DatabaseConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("database.New: %v", err)
	}
	defer db.Close()

	store := outbox.NewGORMStore(db.GORM)
	if err := store.AutoMigrate(); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}

	// Clean out any existing messages from previous runs
	_ = db.GORM.Exec("DELETE FROM outbox_messages").Error

	// 1. Transactionally insert 20 outbox messages
	const totalMessages = 20
	err = db.GORM.Transaction(func(tx *gorm.DB) error {
		for i := 0; i < totalMessages; i++ {
			_, err := outbox.Enqueue(ctx, tx, "orders.placed", "ord-key", []byte(`{"order": true}`), map[string]string{
				"correlation_id": "test-corr",
			})
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Transaction enqueue: %v", err)
	}

	// 2. Concurrently process messages across 4 parallel relay workers using SKIP LOCKED
	pub := &safePublisher{}
	const workers = 4
	var wg sync.WaitGroup

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			relay := outbox.NewRelay(store, pub, outbox.RelayConfig{
				BatchSize: 5,
			})
			// Process until queue exhausted
			for {
				count, err := relay.ProcessBatch(ctx)
				if err != nil || count == 0 {
					break
				}
			}
		}()
	}

	wg.Wait()

	// 3. Verify exactly totalMessages were published (no duplicates, no misses)
	processed := atomic.LoadInt64(&pub.count)
	if processed != int64(totalMessages) {
		t.Fatalf("expected exactly %d messages processed across workers, got %d", totalMessages, processed)
	}

	// 4. Verify no pending messages remain in the database
	pending, err := store.FetchPending(ctx, 100)
	if err != nil {
		t.Fatalf("FetchPending: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("expected 0 pending messages remaining, got %d", len(pending))
	}
}
