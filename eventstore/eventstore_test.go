package eventstore_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/dhawalhost/gokit/eventstore"
)

// --- Domain Models for Testing ---

type AccountOpenedEvent struct {
	Owner   string  `json:"owner"`
	Balance float64 `json:"balance"`
}

type MoneyDepositedEvent struct {
	Amount float64 `json:"amount"`
}

type MoneyWithdrawnEvent struct {
	Amount float64 `json:"amount"`
}

type BankAccount struct {
	eventstore.BaseAggregate
	Owner   string  `json:"owner"`
	Balance float64 `json:"balance"`
}

func NewBankAccount() *BankAccount {
	return &BankAccount{}
}

func (a *BankAccount) AggregateType() string {
	return "BankAccount"
}

func (a *BankAccount) Open(id, owner string, initialBalance float64) {
	a.SetID(id)
	evt, _ := eventstore.NewEvent(id, "AccountOpened", AccountOpenedEvent{
		Owner:   owner,
		Balance: initialBalance,
	}, eventstore.WithAggregateType(a.AggregateType()))
	a.TrackChange(a, evt)
}

func (a *BankAccount) Deposit(amount float64) error {
	if amount <= 0 {
		return errors.New("deposit amount must be positive")
	}
	evt, _ := eventstore.NewEvent(a.AggregateID(), "MoneyDeposited", MoneyDepositedEvent{
		Amount: amount,
	}, eventstore.WithAggregateType(a.AggregateType()))
	a.TrackChange(a, evt)
	return nil
}

func (a *BankAccount) Withdraw(amount float64) error {
	if amount <= 0 {
		return errors.New("withdrawal amount must be positive")
	}
	if a.Balance < amount {
		return errors.New("insufficient funds")
	}
	evt, _ := eventstore.NewEvent(a.AggregateID(), "MoneyWithdrawn", MoneyWithdrawnEvent{
		Amount: amount,
	}, eventstore.WithAggregateType(a.AggregateType()))
	a.TrackChange(a, evt)
	return nil
}

func (a *BankAccount) ApplyChange(evt eventstore.Event) {
	switch evt.Type {
	case "AccountOpened":
		var data AccountOpenedEvent
		_ = evt.UnmarshalData(&data)
		a.SetID(evt.StreamID)
		a.Owner = data.Owner
		a.Balance = data.Balance
	case "MoneyDeposited":
		var data MoneyDepositedEvent
		_ = evt.UnmarshalData(&data)
		a.Balance += data.Amount
	case "MoneyWithdrawn":
		var data MoneyWithdrawnEvent
		_ = evt.UnmarshalData(&data)
		a.Balance -= data.Amount
	}
}

// Snapshot support
type BankAccountSnapshot struct {
	ID      string  `json:"id"`
	Owner   string  `json:"owner"`
	Balance float64 `json:"balance"`
}

func (a *BankAccount) CreateSnapshot() ([]byte, error) {
	return json.Marshal(BankAccountSnapshot{
		ID:      a.AggregateID(),
		Owner:   a.Owner,
		Balance: a.Balance,
	})
}

func (a *BankAccount) ApplySnapshot(state []byte) error {
	var snap BankAccountSnapshot
	if err := json.Unmarshal(state, &snap); err != nil {
		return err
	}
	a.SetID(snap.ID)
	a.Owner = snap.Owner
	a.Balance = snap.Balance
	return nil
}

// --- Tests ---

func TestEvent_Creation_And_Unmarshal(t *testing.T) {
	t.Run("valid event with payload", func(t *testing.T) {
		now := time.Now().UTC().Truncate(time.Second)
		evt, err := eventstore.NewEvent("acc-123", "AccountOpened", AccountOpenedEvent{
			Owner:   "Alice",
			Balance: 500,
		},
			eventstore.WithEventID("custom-id-1"),
			eventstore.WithAggregateType("BankAccount"),
			eventstore.WithMetadata("user_id", "u-99"),
			eventstore.WithMetadataMap(map[string]any{"source": "mobile"}),
			eventstore.WithTimestamp(now),
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if evt.ID != "custom-id-1" {
			t.Errorf("expected ID custom-id-1, got %s", evt.ID)
		}
		if evt.StreamID != "acc-123" {
			t.Errorf("expected StreamID acc-123, got %s", evt.StreamID)
		}
		if evt.AggregateType != "BankAccount" {
			t.Errorf("expected AggregateType BankAccount, got %s", evt.AggregateType)
		}
		if evt.Metadata["user_id"] != "u-99" || evt.Metadata["source"] != "mobile" {
			t.Errorf("metadata missing: %+v", evt.Metadata)
		}
		if !evt.Timestamp.Equal(now) {
			t.Errorf("expected timestamp %v, got %v", now, evt.Timestamp)
		}

		var payload AccountOpenedEvent
		if err := evt.UnmarshalData(&payload); err != nil {
			t.Fatalf("unmarshal failed: %v", err)
		}
		if payload.Owner != "Alice" || payload.Balance != 500 {
			t.Errorf("unmarshaled payload mismatch: %+v", payload)
		}
	})

	t.Run("event with byte slice payload", func(t *testing.T) {
		raw := []byte(`{"raw": true}`)
		evt, err := eventstore.NewEvent("stream-1", "RawEvent", raw)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if string(evt.Data) != string(raw) {
			t.Errorf("expected %s, got %s", string(raw), string(evt.Data))
		}
	})

	t.Run("empty stream ID error", func(t *testing.T) {
		_, err := eventstore.NewEvent("", "TestEvent", nil)
		if !errors.Is(err, eventstore.ErrEmptyStreamID) {
			t.Errorf("expected ErrEmptyStreamID, got %v", err)
		}
	})

	t.Run("empty event type error", func(t *testing.T) {
		_, err := eventstore.NewEvent("stream-1", "", nil)
		if !errors.Is(err, eventstore.ErrNilEvent) {
			t.Errorf("expected ErrNilEvent, got %v", err)
		}
	})

	t.Run("unmarshal empty data", func(t *testing.T) {
		evt := eventstore.Event{}
		var out map[string]any
		if err := evt.UnmarshalData(&out); err != nil {
			t.Errorf("expected nil error for empty data, got %v", err)
		}
	})
}

func TestExpectedVersion_Matches(t *testing.T) {
	tests := []struct {
		name         string
		ev           eventstore.ExpectedVersion
		streamExists bool
		actualVer    uint64
		wantMatch    bool
	}{
		{"any version on non-existent stream", eventstore.ExpectedVersionAny, false, 0, true},
		{"any version on existing stream", eventstore.ExpectedVersionAny, true, 5, true},
		{"no stream on non-existent stream", eventstore.ExpectedVersionNoStream, false, 0, true},
		{"no stream on existing stream", eventstore.ExpectedVersionNoStream, true, 1, false},
		{"exact match", eventstore.ExpectedVersionExact(3), true, 3, true},
		{"exact mismatch", eventstore.ExpectedVersionExact(3), true, 4, false},
		{"exact on non-existent stream", eventstore.ExpectedVersionExact(1), false, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.ev.Matches(tt.streamExists, tt.actualVer)
			if got != tt.wantMatch {
				t.Errorf("Matches() = %v, want %v", got, tt.wantMatch)
			}
		})
	}
}

func TestMemoryStore_Append_And_Read(t *testing.T) {
	ctx := context.Background()
	store := eventstore.NewMemoryStore()

	// Append to new stream with ExpectedVersionNoStream
	e1, _ := eventstore.NewEvent("order-1", "OrderCreated", map[string]string{"item": "laptop"})
	e2, _ := eventstore.NewEvent("order-1", "OrderPaid", map[string]float64{"amount": 1200})
	err := store.Append(ctx, "order-1", eventstore.ExpectedVersionNoStream, e1, e2)
	if err != nil {
		t.Fatalf("Append failed: %v", err)
	}

	// Verify stream version
	ver, err := store.GetStreamVersion(ctx, "order-1")
	if err != nil || ver != 2 {
		t.Fatalf("expected stream version 2, got %d, err: %v", ver, err)
	}

	// Read stream forward
	events, err := store.ReadStream(ctx, "order-1", 1, 0)
	if err != nil {
		t.Fatalf("ReadStream failed: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].Version != 1 || events[1].Version != 2 {
		t.Errorf("event versions incorrect: v1=%d, v2=%d", events[0].Version, events[1].Version)
	}
	if events[0].GlobalPosition != 1 || events[1].GlobalPosition != 2 {
		t.Errorf("global positions incorrect: gp1=%d, gp2=%d", events[0].GlobalPosition, events[1].GlobalPosition)
	}

	// Read stream slice with count
	eventsSlice, err := store.ReadStream(ctx, "order-1", 1, 1)
	if err != nil || len(eventsSlice) != 1 {
		t.Fatalf("expected 1 event slice, got %d, err: %v", len(eventsSlice), err)
	}

	// Read stream backward
	revEvents, err := store.ReadStreamBackward(ctx, "order-1", 2, 0)
	if err != nil || len(revEvents) != 2 {
		t.Fatalf("expected 2 reverse events, got %d, err: %v", len(revEvents), err)
	}
	if revEvents[0].Version != 2 || revEvents[1].Version != 1 {
		t.Errorf("reverse order mismatch: first=%d, second=%d", revEvents[0].Version, revEvents[1].Version)
	}

	// Read global stream
	globalEvents, err := store.ReadGlobal(ctx, 1, 0)
	if err != nil || len(globalEvents) != 2 {
		t.Fatalf("expected 2 global events, got %d, err: %v", len(globalEvents), err)
	}

	// Streams list
	streams, err := store.Streams(ctx)
	if err != nil || len(streams) != 1 || streams[0] != "order-1" {
		t.Errorf("unexpected streams: %v", streams)
	}

	// Global position
	gPos, err := store.GlobalPosition(ctx)
	if err != nil || gPos != 2 {
		t.Errorf("expected global pos 2, got %d", gPos)
	}
}

func TestMemoryStore_ConcurrencyConflict(t *testing.T) {
	ctx := context.Background()
	store := eventstore.NewMemoryStore()

	e1, _ := eventstore.NewEvent("doc-1", "DocCreated", nil)
	_ = store.Append(ctx, "doc-1", eventstore.ExpectedVersionNoStream, e1)

	// Concurrently append with ExpectedVersionExact(1)
	const workers = 10
	var wg sync.WaitGroup
	var successCount int
	var conflictCount int
	var mu sync.Mutex

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			ev, _ := eventstore.NewEvent("doc-1", "DocEdited", map[string]int{"w": workerID})
			err := store.Append(ctx, "doc-1", eventstore.ExpectedVersionExact(1), ev)
			mu.Lock()
			if err == nil {
				successCount++
			} else if errors.Is(err, eventstore.ErrConcurrencyConflict) {
				conflictCount++
			}
			mu.Unlock()
		}(i)
	}
	wg.Wait()

	if successCount != 1 {
		t.Errorf("expected exactly 1 successful append, got %d", successCount)
	}
	if conflictCount != workers-1 {
		t.Errorf("expected %d conflicts, got %d", workers-1, conflictCount)
	}
}

func TestMemoryStore_EdgeCases(t *testing.T) {
	ctx := context.Background()
	store := eventstore.NewMemoryStore()

	// Append empty stream ID
	ev, _ := eventstore.NewEvent("valid", "Type", nil)
	err := store.Append(ctx, "", eventstore.ExpectedVersionAny, ev)
	if !errors.Is(err, eventstore.ErrEmptyStreamID) {
		t.Errorf("expected ErrEmptyStreamID, got %v", err)
	}

	// Append nil event
	err = store.Append(ctx, "s1", eventstore.ExpectedVersionAny, eventstore.Event{})
	if !errors.Is(err, eventstore.ErrNilEvent) {
		t.Errorf("expected ErrNilEvent, got %v", err)
	}

	// Append to existing stream with ExpectedVersionNoStream
	_ = store.Append(ctx, "s1", eventstore.ExpectedVersionNoStream, ev)
	err = store.Append(ctx, "s1", eventstore.ExpectedVersionNoStream, ev)
	if !errors.Is(err, eventstore.ErrStreamExists) {
		t.Errorf("expected ErrStreamExists, got %v", err)
	}

	// Read non-existent stream
	_, err = store.ReadStream(ctx, "unknown", 1, 0)
	if !errors.Is(err, eventstore.ErrStreamNotFound) {
		t.Errorf("expected ErrStreamNotFound, got %v", err)
	}

	// Read stream beyond length
	events, err := store.ReadStream(ctx, "s1", 100, 0)
	if err != nil || len(events) != 0 {
		t.Errorf("expected empty events, got %v, err %v", events, err)
	}

	// Read stream backward non-existent
	_, err = store.ReadStreamBackward(ctx, "unknown", 1, 0)
	if !errors.Is(err, eventstore.ErrStreamNotFound) {
		t.Errorf("expected ErrStreamNotFound, got %v", err)
	}

	// Read global beyond length
	gEvents, err := store.ReadGlobal(ctx, 100, 0)
	if err != nil || len(gEvents) != 0 {
		t.Errorf("expected empty global events, got %v, err %v", gEvents, err)
	}

	// Cancelled context
	cancCtx, cancel := context.WithCancel(ctx)
	cancel()
	err = store.Append(cancCtx, "s1", eventstore.ExpectedVersionAny, ev)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}

	// Test live subscribe
	ch, unsub := store.Subscribe(10)
	defer unsub()

	_ = store.Append(ctx, "s1", eventstore.ExpectedVersionAny, ev)
	select {
	case received := <-ch:
		if received.StreamID != "s1" {
			t.Errorf("unexpected event stream: %s", received.StreamID)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for subscribed event")
	}
}

func TestSnapshotStore_Memory(t *testing.T) {
	ctx := context.Background()
	store := eventstore.NewMemorySnapshotStore()

	snap := eventstore.Snapshot{
		StreamID:      "acc-1",
		AggregateType: "BankAccount",
		Version:       10,
		State:         []byte(`{"balance": 1000}`),
	}

	if err := store.SaveSnapshot(ctx, snap); err != nil {
		t.Fatalf("SaveSnapshot failed: %v", err)
	}

	retrieved, err := store.GetSnapshot(ctx, "acc-1")
	if err != nil {
		t.Fatalf("GetSnapshot failed: %v", err)
	}
	if retrieved.Version != 10 || string(retrieved.State) != string(snap.State) {
		t.Errorf("retrieved snapshot mismatch: %+v", retrieved)
	}

	// Non-existent snapshot
	_, err = store.GetSnapshot(ctx, "unknown")
	if !errors.Is(err, eventstore.ErrSnapshotNotFound) {
		t.Errorf("expected ErrSnapshotNotFound, got %v", err)
	}

	// Delete snapshot
	if err := store.DeleteSnapshot(ctx, "acc-1"); err != nil {
		t.Fatalf("DeleteSnapshot failed: %v", err)
	}
	_, err = store.GetSnapshot(ctx, "acc-1")
	if !errors.Is(err, eventstore.ErrSnapshotNotFound) {
		t.Errorf("expected ErrSnapshotNotFound after delete, got %v", err)
	}

	// Empty stream ID validation
	if err := store.SaveSnapshot(ctx, eventstore.Snapshot{}); !errors.Is(err, eventstore.ErrEmptyStreamID) {
		t.Errorf("expected ErrEmptyStreamID on save, got %v", err)
	}
	if _, err := store.GetSnapshot(ctx, ""); !errors.Is(err, eventstore.ErrEmptyStreamID) {
		t.Errorf("expected ErrEmptyStreamID on get, got %v", err)
	}
	if err := store.DeleteSnapshot(ctx, ""); !errors.Is(err, eventstore.ErrEmptyStreamID) {
		t.Errorf("expected ErrEmptyStreamID on delete, got %v", err)
	}
}

func TestRepository_AggregateLifecycle(t *testing.T) {
	ctx := context.Background()
	store := eventstore.NewMemoryStore()
	repo := eventstore.NewRepository[*BankAccount](store, NewBankAccount)

	// 1. Create and Save BankAccount
	acc := NewBankAccount()
	acc.Open("acc-42", "Bob", 200)
	if err := acc.Deposit(100); err != nil {
		t.Fatalf("deposit failed: %v", err)
	}
	if len(acc.UncommittedEvents()) != 2 {
		t.Fatalf("expected 2 uncommitted events, got %d", len(acc.UncommittedEvents()))
	}

	if err := repo.Save(ctx, acc); err != nil {
		t.Fatalf("repo.Save failed: %v", err)
	}
	if len(acc.UncommittedEvents()) != 0 {
		t.Errorf("expected 0 uncommitted events after save, got %d", len(acc.UncommittedEvents()))
	}
	if acc.Version() != 2 {
		t.Errorf("expected version 2, got %d", acc.Version())
	}

	// 2. Load BankAccount and verify rehydrated state
	loaded, err := repo.Load(ctx, "acc-42")
	if err != nil {
		t.Fatalf("repo.Load failed: %v", err)
	}
	if loaded.Owner != "Bob" || loaded.Balance != 300 {
		t.Errorf("loaded account state mismatch: owner=%s, balance=%f", loaded.Owner, loaded.Balance)
	}
	if loaded.Version() != 2 {
		t.Errorf("expected loaded version 2, got %d", loaded.Version())
	}

	// 3. Mutate loaded account and Save again
	if err := loaded.Withdraw(50); err != nil {
		t.Fatalf("withdraw failed: %v", err)
	}
	if err := repo.Save(ctx, loaded); err != nil {
		t.Fatalf("repo.Save second time failed: %v", err)
	}
	if loaded.Version() != 3 {
		t.Errorf("expected version 3, got %d", loaded.Version())
	}

	// 4. Reload and verify new balance
	reloaded, err := repo.Load(ctx, "acc-42")
	if err != nil {
		t.Fatalf("repo.Load reload failed: %v", err)
	}
	if reloaded.Balance != 250 || reloaded.Version() != 3 {
		t.Errorf("reloaded state mismatch: balance=%f, version=%d", reloaded.Balance, reloaded.Version())
	}
}

func TestRepository_Snapshot_And_ReplayDelta(t *testing.T) {
	ctx := context.Background()
	store := eventstore.NewMemoryStore()
	snapStore := eventstore.NewMemorySnapshotStore()

	// Snapshot every 5 events
	repo := eventstore.NewRepository[*BankAccount](
		store,
		NewBankAccount,
		eventstore.WithRepositorySnapshotStore(snapStore, eventstore.EveryNEvents(5)),
	)

	acc := NewBankAccount()
	acc.Open("acc-snap", "Charlie", 100)
	for i := 0; i < 6; i++ {
		_ = acc.Deposit(10)
	}
	// Total 7 events: Open (1) + 6 Deposits (2..7)
	if err := repo.Save(ctx, acc); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Verify snapshot exists at version 5
	snap, err := snapStore.GetSnapshot(ctx, "acc-snap")
	if err != nil {
		t.Fatalf("expected snapshot to exist: %v", err)
	}
	if snap.Version != 7 {
		// Version 7 is >= 5, so snapshot saved with current version 7
		if snap.Version != 7 {
			t.Errorf("expected snapshot version 7, got %d", snap.Version)
		}
	}

	// Add 3 more deposits
	for i := 0; i < 3; i++ {
		_ = acc.Deposit(20)
	}
	if err := repo.Save(ctx, acc); err != nil {
		t.Fatalf("Save second batch failed: %v", err)
	}
	if acc.Version() != 10 {
		t.Fatalf("expected version 10, got %d", acc.Version())
	}

	// Rehydrate from snapshot + delta events
	rehydrated, err := repo.Load(ctx, "acc-snap")
	if err != nil {
		t.Fatalf("Load rehydrate failed: %v", err)
	}
	// Expected balance: 100 + (6 * 10) + (3 * 20) = 100 + 60 + 60 = 220
	if rehydrated.Balance != 220 {
		t.Errorf("expected balance 220, got %f", rehydrated.Balance)
	}
	if rehydrated.Version() != 10 {
		t.Errorf("expected version 10, got %d", rehydrated.Version())
	}
}

func TestRepository_OptimisticLocking(t *testing.T) {
	ctx := context.Background()
	store := eventstore.NewMemoryStore()
	repo := eventstore.NewRepository[*BankAccount](store, NewBankAccount)

	acc := NewBankAccount()
	acc.Open("acc-concur", "Dave", 1000)
	_ = repo.Save(ctx, acc)

	// User 1 loads account
	user1Acc, _ := repo.Load(ctx, "acc-concur")
	// User 2 loads account concurrently
	user2Acc, _ := repo.Load(ctx, "acc-concur")

	// User 1 withdraws 200 and saves -> succeeds
	_ = user1Acc.Withdraw(200)
	if err := repo.Save(ctx, user1Acc); err != nil {
		t.Fatalf("user 1 save failed: %v", err)
	}

	// User 2 withdraws 100 and tries to save -> fails with concurrency conflict!
	_ = user2Acc.Withdraw(100)
	err := repo.Save(ctx, user2Acc)
	if !errors.Is(err, eventstore.ErrConcurrencyConflict) {
		t.Fatalf("expected ErrConcurrencyConflict for concurrent save, got: %v", err)
	}
}

func TestRepository_EventEnricher(t *testing.T) {
	ctx := context.Background()
	store := eventstore.NewMemoryStore()
	repo := eventstore.NewRepository[*BankAccount](
		store,
		NewBankAccount,
		eventstore.WithEventEnricher(func(ctx context.Context, e *eventstore.Event) {
			if e.Metadata == nil {
				e.Metadata = make(map[string]any)
			}
			e.Metadata["trace_id"] = "trace-abc-123"
		}),
	)

	acc := NewBankAccount()
	acc.Open("acc-enrich", "Eve", 500)
	_ = repo.Save(ctx, acc)

	events, _ := store.ReadStream(ctx, "acc-enrich", 1, 0)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Metadata["trace_id"] != "trace-abc-123" {
		t.Errorf("enricher metadata missing: %+v", events[0].Metadata)
	}
}

// TotalDepositProjection calculates the sum of all deposits across all bank accounts
type TotalDepositProjection struct {
	mu           sync.RWMutex
	TotalDeposit float64
	EventCount   int
}

func (p *TotalDepositProjection) Name() string {
	return "TotalDepositProjection"
}

func (p *TotalDepositProjection) Handle(ctx context.Context, evt eventstore.Event) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.EventCount++

	switch evt.Type {
	case "AccountOpened":
		var data AccountOpenedEvent
		_ = evt.UnmarshalData(&data)
		p.TotalDeposit += data.Balance
	case "MoneyDeposited":
		var data MoneyDepositedEvent
		_ = evt.UnmarshalData(&data)
		p.TotalDeposit += data.Amount
	}
	return nil
}

func TestCQRS_Projection_And_SubscriptionManager(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	store := eventstore.NewMemoryStore()
	repo := eventstore.NewRepository[*BankAccount](store, NewBankAccount)
	proj := &TotalDepositProjection{}

	subMgr := eventstore.NewSubscriptionManager(
		store,
		eventstore.NewMemoryCheckpointStore(),
		eventstore.WithPollInterval(20*time.Millisecond),
	)
	subMgr.Register(proj)

	// Create initial accounts
	a1 := NewBankAccount()
	a1.Open("acc-1", "A1", 100)
	_ = a1.Deposit(50)
	_ = repo.Save(ctx, a1)

	a2 := NewBankAccount()
	a2.Open("acc-2", "A2", 200)
	_ = a2.Deposit(75)
	_ = repo.Save(ctx, a2)

	// Start subscription manager (catches up history and polls)
	if err := subMgr.Start(ctx); err != nil {
		t.Fatalf("subMgr.Start failed: %v", err)
	}
	defer subMgr.Stop()

	// Wait for catch-up
	time.Sleep(100 * time.Millisecond)

	proj.mu.RLock()
	// Expected total: 100 + 50 + 200 + 75 = 425
	if proj.TotalDeposit != 425 {
		t.Errorf("expected total deposits 425, got %f", proj.TotalDeposit)
	}
	proj.mu.RUnlock()

	// Create additional live event
	a3 := NewBankAccount()
	a3.Open("acc-3", "A3", 300)
	_ = repo.Save(ctx, a3)

	// Wait for background poll
	time.Sleep(100 * time.Millisecond)

	proj.mu.RLock()
	// Expected total: 425 + 300 = 725
	if proj.TotalDeposit != 725 {
		t.Errorf("expected total deposits 725, got %f", proj.TotalDeposit)
	}
	proj.mu.RUnlock()
}

func TestConcurrencyConflictError_Format(t *testing.T) {
	err := &eventstore.ConcurrencyConflictError{
		StreamID:        "acc-123",
		ExpectedVersion: eventstore.ExpectedVersionExact(2),
		ActualVersion:   3,
	}
	expectedStr := `eventstore: concurrency conflict on stream "acc-123": expected version 2, actual version 3`
	if err.Error() != expectedStr {
		t.Errorf("expected %q, got %q", expectedStr, err.Error())
	}
	if !errors.Is(err, eventstore.ErrConcurrencyConflict) {
		t.Errorf("expected unwrapped ErrConcurrencyConflict")
	}
}

func TestSnapshotStrategy_Coverage(t *testing.T) {
	acc := NewBankAccount()
	acc.SetVersion(10)

	always := eventstore.AlwaysSnapshot()
	if !always(acc, 1) {
		t.Errorf("AlwaysSnapshot should return true for uncommitted > 0")
	}
	if always(nil, 1) {
		t.Errorf("AlwaysSnapshot should return false for nil agg")
	}

	never := eventstore.NeverSnapshot()
	if never(acc, 1) {
		t.Errorf("NeverSnapshot should return false")
	}

	everyZero := eventstore.EveryNEvents(0)
	if everyZero(acc, 1) {
		t.Errorf("EveryNEvents(0) should return false")
	}
	if everyZero(nil, 1) {
		t.Errorf("EveryNEvents on nil agg should return false")
	}
	accZeroVer := NewBankAccount()
	if everyZero(accZeroVer, 1) {
		t.Errorf("EveryNEvents on version 0 should return false")
	}
}

func TestRepository_EdgeCases(t *testing.T) {
	ctx := context.Background()
	store := eventstore.NewMemoryStore()

	// Nil factory
	badRepo := eventstore.NewRepository[*BankAccount](store, nil)
	_, err := badRepo.Load(ctx, "stream-1")
	if err == nil {
		t.Errorf("expected error for nil factory")
	}

	// Factory returns nil
	nilRetRepo := eventstore.NewRepository[*BankAccount](store, func() *BankAccount { return nil })
	_, err = nilRetRepo.Load(ctx, "stream-1")
	if !errors.Is(err, eventstore.ErrNilAggregate) {
		t.Errorf("expected ErrNilAggregate, got %v", err)
	}

	// Empty ID load
	repo := eventstore.NewRepository[*BankAccount](store, NewBankAccount)
	_, err = repo.Load(ctx, "")
	if !errors.Is(err, eventstore.ErrEmptyStreamID) {
		t.Errorf("expected ErrEmptyStreamID on load, got %v", err)
	}

	// Cancelled context on Load
	cancCtx, cancel := context.WithCancel(ctx)
	cancel()
	_, err = repo.Load(cancCtx, "stream-1")
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}

	// Save nil aggregate
	err = repo.Save(ctx, nil)
	if !errors.Is(err, eventstore.ErrNilAggregate) {
		t.Errorf("expected ErrNilAggregate on save, got %v", err)
	}

	// Save empty aggregate ID
	emptyAcc := NewBankAccount()
	err = repo.Save(ctx, emptyAcc)
	if !errors.Is(err, eventstore.ErrEmptyStreamID) {
		t.Errorf("expected ErrEmptyStreamID on save, got %v", err)
	}

	// Save with 0 uncommitted events
	emptyAcc.SetID("valid-id")
	err = repo.Save(ctx, emptyAcc)
	if err != nil {
		t.Errorf("expected nil error on 0 uncommitted events, got %v", err)
	}

	// Save cancelled context
	err = repo.Save(cancCtx, emptyAcc)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled on save, got %v", err)
	}
}

type FailProjection struct{}

func (f *FailProjection) Name() string { return "FailProjection" }
func (f *FailProjection) Handle(ctx context.Context, evt eventstore.Event) error {
	return errors.New("boom")
}

func TestSubscriptionManager_EdgeCases(t *testing.T) {
	ctx := context.Background()
	store := eventstore.NewMemoryStore()

	// WithBatchSize option
	sm := eventstore.NewSubscriptionManager(
		store,
		nil,
		eventstore.WithBatchSize(50),
		eventstore.WithPollInterval(10*time.Millisecond),
	)

	// Append an event
	ev, _ := eventstore.NewEvent("s1", "Ev1", nil)
	_ = store.Append(ctx, "s1", eventstore.ExpectedVersionNoStream, ev)

	// Test failing projection
	sm.Register(&FailProjection{})
	err := sm.CatchUp(ctx)
	if err == nil {
		t.Errorf("expected catchup error for failing projection")
	}

	// Test Start and double Stop
	cancCtx, cancel := context.WithCancel(ctx)
	cancel() // pre-cancel context
	_ = sm.Start(cancCtx)
	sm.Stop()
	sm.Stop() // idempotent

	// Start on closed manager
	err = sm.Start(ctx)
	if !errors.Is(err, eventstore.ErrSubscriptionClosed) {
		t.Errorf("expected ErrSubscriptionClosed, got %v", err)
	}
}

func TestMemoryStore_ContextCancellations(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	store := eventstore.NewMemoryStore()
	_, err := store.ReadStream(ctx, "s1", 1, 0)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled on ReadStream, got %v", err)
	}

	_, err = store.ReadStreamBackward(ctx, "s1", 1, 0)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled on ReadStreamBackward, got %v", err)
	}

	_, err = store.ReadGlobal(ctx, 1, 0)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled on ReadGlobal, got %v", err)
	}

	_, err = store.GetStreamVersion(ctx, "s1")
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled on GetStreamVersion, got %v", err)
	}

	_, err = store.Streams(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled on Streams, got %v", err)
	}

	_, err = store.GlobalPosition(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled on GlobalPosition, got %v", err)
	}

	cpStore := eventstore.NewMemoryCheckpointStore()
	_, err = cpStore.GetCheckpoint(ctx, "p1")
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled on GetCheckpoint, got %v", err)
	}

	err = cpStore.SaveCheckpoint(ctx, "p1", 10)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled on SaveCheckpoint, got %v", err)
	}

	snapStore := eventstore.NewMemorySnapshotStore()
	_, err = snapStore.GetSnapshot(ctx, "s1")
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled on GetSnapshot, got %v", err)
	}
	err = snapStore.SaveSnapshot(ctx, eventstore.Snapshot{StreamID: "s1"})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled on SaveSnapshot, got %v", err)
	}
	err = snapStore.DeleteSnapshot(ctx, "s1")
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled on DeleteSnapshot, got %v", err)
	}
}
