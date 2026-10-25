package workflow_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/dhawalhost/gokit/workflow"
)

const (
	stateDraft          workflow.State = "DRAFT"
	statePendingPayment workflow.State = "PENDING_PAYMENT"
	statePaid           workflow.State = "PAID"
	stateShipped        workflow.State = "SHIPPED"
	stateCancelled      workflow.State = "CANCELLED"

	eventSubmit  workflow.Event = "SUBMIT"
	eventPay     workflow.Event = "PAY"
	eventShip    workflow.Event = "SHIP"
	eventCancel  workflow.Event = "CANCEL"
	eventUnknown workflow.Event = "UNKNOWN"
)

func buildOrderWorkflow(t *testing.T) *workflow.Definition {
	t.Helper()

	def, err := workflow.NewBuilder("order-workflow", stateDraft).
		AddTransition(eventSubmit, []workflow.State{stateDraft}, statePendingPayment).
		AddTransition(eventPay, []workflow.State{statePendingPayment}, statePaid,
			workflow.WithGuard(func(ctx context.Context, e *workflow.EventContext) bool {
				amount, ok := e.Payload.(int)
				return ok && amount > 0
			}),
		).
		AddTransition(eventShip, []workflow.State{statePaid}, stateShipped).
		AddTransition(eventCancel, []workflow.State{stateDraft, statePendingPayment}, stateCancelled).
		Build()

	if err != nil {
		t.Fatalf("failed to build order workflow: %v", err)
	}
	return def
}

func TestDefinitionBuilder(t *testing.T) {
	t.Run("validation errors", func(t *testing.T) {
		_, err := workflow.NewBuilder("", stateDraft).Build()
		if !errors.Is(err, workflow.ErrInvalidDefinition) {
			t.Errorf("expected ErrInvalidDefinition for empty name, got: %v", err)
		}

		_, err = workflow.NewBuilder("test", "").Build()
		if !errors.Is(err, workflow.ErrInvalidDefinition) {
			t.Errorf("expected ErrInvalidDefinition for empty initial state, got: %v", err)
		}

		_, err = workflow.NewBuilder("test", stateDraft).
			AddTransition("", []workflow.State{stateDraft}, statePaid).
			Build()
		if !errors.Is(err, workflow.ErrInvalidDefinition) {
			t.Errorf("expected ErrInvalidDefinition for empty event, got: %v", err)
		}

		_, err = workflow.NewBuilder("test", stateDraft).
			AddTransition(eventPay, []workflow.State{stateDraft}, "").
			Build()
		if !errors.Is(err, workflow.ErrInvalidDefinition) {
			t.Errorf("expected ErrInvalidDefinition for empty target state, got: %v", err)
		}

		_, err = workflow.NewBuilder("test", stateDraft).
			AddTransition(eventPay, nil, statePaid).
			Build()
		if !errors.Is(err, workflow.ErrInvalidDefinition) {
			t.Errorf("expected ErrInvalidDefinition for empty from states, got: %v", err)
		}

		_, err = workflow.NewBuilder("test", stateDraft).
			AddTransition(eventPay, []workflow.State{stateDraft}, statePaid).
			AddTransition(eventPay, []workflow.State{stateDraft}, stateCancelled).
			Build()
		if !errors.Is(err, workflow.ErrInvalidDefinition) {
			t.Errorf("expected ErrInvalidDefinition for duplicate transition, got: %v", err)
		}
	})

	t.Run("successful inspection", func(t *testing.T) {
		def := buildOrderWorkflow(t)

		states := def.States()
		if len(states) != 5 {
			t.Errorf("expected 5 states, got %d", len(states))
		}

		tr, ok := def.FindTransition(stateDraft, eventSubmit)
		if !ok || tr.To != statePendingPayment {
			t.Errorf("expected valid transition from DRAFT to PENDING_PAYMENT, got %v", tr)
		}

		_, ok = def.FindTransition(stateDraft, eventShip)
		if ok {
			t.Errorf("expected no transition from DRAFT to SHIP")
		}

		permitted := def.PermittedEvents(stateDraft)
		if len(permitted) != 2 {
			t.Errorf("expected 2 permitted events from DRAFT (SUBMIT, CANCEL), got %d", len(permitted))
		}

		noPermitted := def.PermittedEvents(stateCancelled)
		if len(noPermitted) != 0 {
			t.Errorf("expected 0 permitted events from CANCELLED, got %d", len(noPermitted))
		}
	})
}

func TestMemoryStore(t *testing.T) {
	ctx := context.Background()
	store := workflow.NewMemoryStore()

	t.Run("nil or invalid inputs", func(t *testing.T) {
		if err := store.Save(ctx, nil); !errors.Is(err, workflow.ErrInstanceNotFound) {
			t.Errorf("expected ErrInstanceNotFound for nil instance, got: %v", err)
		}

		if err := store.Save(ctx, &workflow.Instance{}); !errors.Is(err, workflow.ErrInstanceNotFound) {
			t.Errorf("expected ErrInstanceNotFound for empty ID, got: %v", err)
		}

		if _, err := store.Get(ctx, ""); !errors.Is(err, workflow.ErrInstanceNotFound) {
			t.Errorf("expected ErrInstanceNotFound for empty get ID, got: %v", err)
		}

		if err := store.Delete(ctx, ""); !errors.Is(err, workflow.ErrInstanceNotFound) {
			t.Errorf("expected ErrInstanceNotFound for empty delete ID, got: %v", err)
		}

		if _, err := store.Get(ctx, "non-existent"); !errors.Is(err, workflow.ErrInstanceNotFound) {
			t.Errorf("expected ErrInstanceNotFound for missing ID, got: %v", err)
		}

		if err := store.Delete(ctx, "non-existent"); !errors.Is(err, workflow.ErrInstanceNotFound) {
			t.Errorf("expected ErrInstanceNotFound for deleting missing ID, got: %v", err)
		}
	})

	t.Run("cancelled context", func(t *testing.T) {
		cancCtx, cancel := context.WithCancel(ctx)
		cancel()

		if err := store.Save(cancCtx, &workflow.Instance{ID: "test", Version: 1}); !errors.Is(err, context.Canceled) {
			t.Errorf("expected context.Canceled on Save, got: %v", err)
		}

		if _, err := store.Get(cancCtx, "test"); !errors.Is(err, context.Canceled) {
			t.Errorf("expected context.Canceled on Get, got: %v", err)
		}

		if err := store.Delete(cancCtx, "test"); !errors.Is(err, context.Canceled) {
			t.Errorf("expected context.Canceled on Delete, got: %v", err)
		}
	})

	t.Run("optimistic locking and clone isolation", func(t *testing.T) {
		inst := &workflow.Instance{
			ID:             "order-1",
			DefinitionName: "order-workflow",
			CurrentState:   stateDraft,
			Version:        1,
			Metadata:       map[string]any{"customer": "Alice"},
			CreatedAt:      time.Now(),
			UpdatedAt:      time.Now(),
		}

		// Initial save
		if err := store.Save(ctx, inst); err != nil {
			t.Fatalf("failed to save initial instance: %v", err)
		}

		// Inserting new instance with version > 1 fails
		invalidNew := &workflow.Instance{ID: "order-2", Version: 2}
		if err := store.Save(ctx, invalidNew); !errors.Is(err, workflow.ErrOptimisticLockConflict) {
			t.Errorf("expected ErrOptimisticLockConflict for new instance with version 2, got: %v", err)
		}

		// Updating with wrong version fails
		staleInst := inst.Clone()
		staleInst.Version = 3
		if err := store.Save(ctx, staleInst); !errors.Is(err, workflow.ErrOptimisticLockConflict) {
			t.Errorf("expected ErrOptimisticLockConflict for mismatched version, got: %v", err)
		}

		// Correct update
		inst.Version = 2
		inst.CurrentState = statePendingPayment
		if err := store.Save(ctx, inst); err != nil {
			t.Fatalf("expected successful update, got: %v", err)
		}

		// Mutation isolation: mutate local inst
		inst.Metadata["customer"] = "Bob"
		retrieved, err := store.Get(ctx, "order-1")
		if err != nil {
			t.Fatalf("failed to get instance: %v", err)
		}
		if retrieved.Metadata["customer"] != "Alice" {
			t.Errorf("expected cloned isolation, stored value mutated to: %v", retrieved.Metadata["customer"])
		}

		// Delete
		if err := store.Delete(ctx, "order-1"); err != nil {
			t.Fatalf("expected successful delete, got: %v", err)
		}
		if _, err := store.Get(ctx, "order-1"); !errors.Is(err, workflow.ErrInstanceNotFound) {
			t.Errorf("expected ErrInstanceNotFound after delete, got: %v", err)
		}
	})
}

func TestEngineLifecycle(t *testing.T) {
	ctx := context.Background()
	engine := workflow.NewEngine(nil, workflow.WithIDGenerator(func() string {
		return "fixed-id-1"
	}))

	t.Run("definition registry", func(t *testing.T) {
		if err := engine.RegisterDefinition(nil); !errors.Is(err, workflow.ErrInvalidDefinition) {
			t.Errorf("expected ErrInvalidDefinition for nil def, got: %v", err)
		}

		if _, err := engine.GetDefinition("missing"); !errors.Is(err, workflow.ErrDefinitionNotFound) {
			t.Errorf("expected ErrDefinitionNotFound, got: %v", err)
		}

		def := buildOrderWorkflow(t)
		if err := engine.RegisterDefinition(def); err != nil {
			t.Fatalf("failed to register definition: %v", err)
		}

		gotDef, err := engine.GetDefinition("order-workflow")
		if err != nil || gotDef.Name != "order-workflow" {
			t.Fatalf("failed to retrieve registered definition: %v", err)
		}
	})

	t.Run("create instance", func(t *testing.T) {
		// Non-existent definition
		_, err := engine.CreateInstance(ctx, "missing")
		if !errors.Is(err, workflow.ErrDefinitionNotFound) {
			t.Errorf("expected ErrDefinitionNotFound, got: %v", err)
		}

		// Successful creation with options
		meta := map[string]any{"user_id": "u-123"}
		inst, err := engine.CreateInstance(ctx, "order-workflow",
			workflow.WithInstanceID("custom-id"),
			workflow.WithMetadata(meta),
			workflow.WithInitialState(stateDraft),
		)
		if err != nil {
			t.Fatalf("failed to create instance: %v", err)
		}

		if inst.ID != "custom-id" || inst.CurrentState != stateDraft || inst.Version != 1 {
			t.Errorf("unexpected instance state: %+v", inst)
		}

		// Verify GetInstance
		fetched, err := engine.GetInstance(ctx, "custom-id")
		if err != nil || fetched.ID != "custom-id" {
			t.Fatalf("failed to get instance: %v", err)
		}

		// Default ID generator check
		inst2, err := engine.CreateInstance(ctx, "order-workflow")
		if err != nil {
			t.Fatalf("failed to create instance with default idgen: %v", err)
		}
		if inst2.ID != "fixed-id-1" {
			t.Errorf("expected ID 'fixed-id-1', got: %s", inst2.ID)
		}
	})

	t.Run("transition transitions and guards", func(t *testing.T) {
		inst, err := engine.CreateInstance(ctx, "order-workflow", workflow.WithInstanceID("order-fsm"))
		if err != nil {
			t.Fatalf("failed to create instance: %v", err)
		}

		// Invalid transition
		_, err = engine.Fire(ctx, inst.ID, eventShip, nil)
		if !errors.Is(err, workflow.ErrInvalidTransition) {
			t.Errorf("expected ErrInvalidTransition firing SHIP on DRAFT, got: %v", err)
		}

		// Permitted events check
		events, err := engine.PermittedEvents(ctx, inst.ID)
		if err != nil || len(events) != 2 {
			t.Errorf("expected 2 permitted events, got %v (%v)", events, err)
		}

		// CanFire check
		canSubmit, err := engine.CanFire(ctx, inst.ID, eventSubmit)
		if err != nil || !canSubmit {
			t.Errorf("expected CanFire(SUBMIT) to be true, got %v (%v)", canSubmit, err)
		}
		canShip, err := engine.CanFire(ctx, inst.ID, eventShip)
		if err != nil || canShip {
			t.Errorf("expected CanFire(SHIP) to be false, got %v (%v)", canShip, err)
		}

		// Fire SUBMIT
		inst, err = engine.Fire(ctx, inst.ID, eventSubmit, nil)
		if err != nil {
			t.Fatalf("failed to fire SUBMIT: %v", err)
		}
		if inst.CurrentState != statePendingPayment || inst.Version != 2 {
			t.Errorf("expected state PENDING_PAYMENT and version 2, got %s / %d", inst.CurrentState, inst.Version)
		}

		// Fire PAY with invalid payload (amount <= 0)
		_, err = engine.Fire(ctx, inst.ID, eventPay, 0)
		if !errors.Is(err, workflow.ErrGuardFailed) {
			t.Errorf("expected ErrGuardFailed for payment 0, got: %v", err)
		}

		// Fire PAY with valid payload
		inst, err = engine.Fire(ctx, inst.ID, eventPay, 100)
		if err != nil {
			t.Fatalf("failed to fire PAY: %v", err)
		}
		if inst.CurrentState != statePaid || inst.Version != 3 {
			t.Errorf("expected state PAID and version 3, got %s / %d", inst.CurrentState, inst.Version)
		}

		// Fire SHIP
		inst, err = engine.Fire(ctx, inst.ID, eventShip, nil)
		if err != nil {
			t.Fatalf("failed to fire SHIP: %v", err)
		}
		if inst.CurrentState != stateShipped || inst.Version != 4 {
			t.Errorf("expected state SHIPPED and version 4, got %s / %d", inst.CurrentState, inst.Version)
		}

		// Verify audit history
		if len(inst.History) != 3 {
			t.Fatalf("expected 3 history entries, got %d", len(inst.History))
		}
		if inst.History[0].Event != eventSubmit || inst.History[1].Event != eventPay || inst.History[2].Event != eventShip {
			t.Errorf("unexpected history trail: %+v", inst.History)
		}
	})
}

func TestEngineHooksAndPanicRecovery(t *testing.T) {
	ctx := context.Background()

	t.Run("lifecycle hooks and actions order", func(t *testing.T) {
		var executionOrder []string

		def, err := workflow.NewBuilder("hook-workflow", stateDraft).
			OnExit(stateDraft, func(ctx context.Context, e *workflow.EventContext) error {
				executionOrder = append(executionOrder, "exit_draft")
				return nil
			}).
			OnEnter(statePaid, func(ctx context.Context, e *workflow.EventContext) error {
				executionOrder = append(executionOrder, "enter_paid")
				return nil
			}).
			AddTransition(eventPay, []workflow.State{stateDraft}, statePaid,
				workflow.WithAction(func(ctx context.Context, e *workflow.EventContext) error {
					executionOrder = append(executionOrder, "action_pay")
					return nil
				}),
			).
			Build()
		if err != nil {
			t.Fatalf("failed to build workflow: %v", err)
		}

		engine := workflow.NewEngine(nil)
		_ = engine.RegisterDefinition(def)

		inst, err := engine.CreateInstance(ctx, "hook-workflow", workflow.WithInstanceID("hook-1"))
		if err != nil {
			t.Fatalf("failed to create instance: %v", err)
		}

		_, err = engine.Fire(ctx, inst.ID, eventPay, nil)
		if err != nil {
			t.Fatalf("fire failed: %v", err)
		}

		expected := []string{"exit_draft", "action_pay", "enter_paid"}
		if len(executionOrder) != 3 {
			t.Fatalf("expected 3 hooks executed, got %d", len(executionOrder))
		}
		for i, exp := range expected {
			if executionOrder[i] != exp {
				t.Errorf("index %d: expected %s, got %s", i, exp, executionOrder[i])
			}
		}
	})

	t.Run("hook error terminates transition", func(t *testing.T) {
		errExit := errors.New("exit error")
		def, _ := workflow.NewBuilder("err-exit", stateDraft).
			OnExit(stateDraft, func(ctx context.Context, e *workflow.EventContext) error {
				return errExit
			}).
			AddTransition(eventPay, []workflow.State{stateDraft}, statePaid).
			Build()

		engine := workflow.NewEngine(nil)
		_ = engine.RegisterDefinition(def)

		inst, _ := engine.CreateInstance(ctx, "err-exit")
		_, err := engine.Fire(ctx, inst.ID, eventPay, nil)
		if !errors.Is(err, errExit) {
			t.Errorf("expected errExit, got: %v", err)
		}

		// Ensure instance remains in original state
		fetched, _ := engine.GetInstance(ctx, inst.ID)
		if fetched.CurrentState != stateDraft {
			t.Errorf("expected state to remain DRAFT, got: %s", fetched.CurrentState)
		}
	})

	t.Run("action panic recovery", func(t *testing.T) {
		def, _ := workflow.NewBuilder("panic-workflow", stateDraft).
			AddTransition(eventPay, []workflow.State{stateDraft}, statePaid,
				workflow.WithAction(func(ctx context.Context, e *workflow.EventContext) error {
					panic("boom in action!")
				}),
			).
			Build()

		engine := workflow.NewEngine(nil)
		_ = engine.RegisterDefinition(def)

		inst, _ := engine.CreateInstance(ctx, "panic-workflow")
		_, err := engine.Fire(ctx, inst.ID, eventPay, nil)
		if err == nil {
			t.Fatalf("expected error from panicked action, got nil")
		}

		// Verify state untouched
		fetched, _ := engine.GetInstance(ctx, inst.ID)
		if fetched.CurrentState != stateDraft {
			t.Errorf("expected state DRAFT, got: %s", fetched.CurrentState)
		}
	})

	t.Run("guard panic recovery", func(t *testing.T) {
		def, _ := workflow.NewBuilder("guard-panic", stateDraft).
			AddTransition(eventPay, []workflow.State{stateDraft}, statePaid,
				workflow.WithGuard(func(ctx context.Context, e *workflow.EventContext) bool {
					panic("boom in guard!")
				}),
			).
			Build()

		engine := workflow.NewEngine(nil)
		_ = engine.RegisterDefinition(def)

		inst, _ := engine.CreateInstance(ctx, "guard-panic")
		_, err := engine.Fire(ctx, inst.ID, eventPay, nil)
		if !errors.Is(err, workflow.ErrGuardFailed) {
			t.Errorf("expected ErrGuardFailed from panicked guard, got: %v", err)
		}
	})

	t.Run("enter hook error terminates transition", func(t *testing.T) {
		errEnter := errors.New("enter error")
		def, _ := workflow.NewBuilder("err-enter", stateDraft).
			OnEnter(statePaid, func(ctx context.Context, e *workflow.EventContext) error {
				return errEnter
			}).
			AddTransition(eventPay, []workflow.State{stateDraft}, statePaid).
			Build()

		engine := workflow.NewEngine(nil)
		_ = engine.RegisterDefinition(def)

		inst, _ := engine.CreateInstance(ctx, "err-enter")
		_, err := engine.Fire(ctx, inst.ID, eventPay, nil)
		if !errors.Is(err, errEnter) {
			t.Errorf("expected errEnter, got: %v", err)
		}
	})
}

func TestConcurrentOptimisticLocking(t *testing.T) {
	ctx := context.Background()
	engine := workflow.NewEngine(nil)

	def := buildOrderWorkflow(t)
	_ = engine.RegisterDefinition(def)

	inst, err := engine.CreateInstance(ctx, "order-workflow", workflow.WithInstanceID("concur-1"))
	if err != nil {
		t.Fatalf("failed to create instance: %v", err)
	}

	const workers = 10
	var wg sync.WaitGroup
	var successCount int64
	var conflictCount int64
	var mu sync.Mutex

	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			_, fireErr := engine.Fire(ctx, inst.ID, eventSubmit, nil)
			mu.Lock()
			defer mu.Unlock()
			if fireErr == nil {
				successCount++
			} else if errors.Is(fireErr, workflow.ErrOptimisticLockConflict) || errors.Is(fireErr, workflow.ErrInvalidTransition) {
				conflictCount++
			}
		}()
	}

	wg.Wait()

	if successCount != 1 {
		t.Errorf("expected exactly 1 successful transition, got %d", successCount)
	}
	if conflictCount != workers-1 {
		t.Errorf("expected %d conflicts/invalid transitions, got %d", workers-1, conflictCount)
	}
}

func TestInstanceCloneNil(t *testing.T) {
	var in *workflow.Instance
	if in.Clone() != nil {
		t.Errorf("expected nil clone for nil instance")
	}
}

func TestEngineEdgeCases(t *testing.T) {
	ctx := context.Background()
	engine := workflow.NewEngine(nil)
	def := buildOrderWorkflow(t)
	_ = engine.RegisterDefinition(def)

	inst, err := engine.CreateInstance(ctx, "order-workflow")
	if err != nil {
		t.Fatalf("failed to create instance: %v", err)
	}

	cancCtx, cancel := context.WithCancel(ctx)
	cancel()

	t.Run("context cancelled checks", func(t *testing.T) {
		if _, err := engine.CreateInstance(cancCtx, "order-workflow"); !errors.Is(err, context.Canceled) {
			t.Errorf("expected Canceled on CreateInstance, got: %v", err)
		}
		if _, err := engine.Fire(cancCtx, inst.ID, eventSubmit, nil); !errors.Is(err, context.Canceled) {
			t.Errorf("expected Canceled on Fire, got: %v", err)
		}
		if _, err := engine.CanFire(cancCtx, inst.ID, eventSubmit); !errors.Is(err, context.Canceled) {
			t.Errorf("expected Canceled on CanFire, got: %v", err)
		}
		if _, err := engine.PermittedEvents(cancCtx, inst.ID); !errors.Is(err, context.Canceled) {
			t.Errorf("expected Canceled on PermittedEvents, got: %v", err)
		}
	})

	t.Run("missing instance or definition in queries", func(t *testing.T) {
		if _, err := engine.CanFire(ctx, "non-existent", eventSubmit); !errors.Is(err, workflow.ErrInstanceNotFound) {
			t.Errorf("expected ErrInstanceNotFound on CanFire, got: %v", err)
		}
		if _, err := engine.PermittedEvents(ctx, "non-existent"); !errors.Is(err, workflow.ErrInstanceNotFound) {
			t.Errorf("expected ErrInstanceNotFound on PermittedEvents, got: %v", err)
		}

		// Inject instance pointing to non-existent definition directly into store
		orphanInst := &workflow.Instance{
			ID:             "orphan-1",
			DefinitionName: "deleted-workflow",
			CurrentState:   stateDraft,
			Version:        1,
		}

		// Create store and engine to test missing definition on existing instance
		badStore := workflow.NewMemoryStore()
		_ = badStore.Save(ctx, orphanInst)
		badEngine := workflow.NewEngine(badStore)

		if _, err := badEngine.Fire(ctx, "orphan-1", eventSubmit, nil); !errors.Is(err, workflow.ErrDefinitionNotFound) {
			t.Errorf("expected ErrDefinitionNotFound on Fire, got: %v", err)
		}
		if _, err := badEngine.CanFire(ctx, "orphan-1", eventSubmit); !errors.Is(err, workflow.ErrDefinitionNotFound) {
			t.Errorf("expected ErrDefinitionNotFound on CanFire, got: %v", err)
		}
		if _, err := badEngine.PermittedEvents(ctx, "orphan-1"); !errors.Is(err, workflow.ErrDefinitionNotFound) {
			t.Errorf("expected ErrDefinitionNotFound on PermittedEvents, got: %v", err)
		}
	})

	t.Run("FindTransition unmapped state", func(t *testing.T) {
		if _, ok := def.FindTransition("UNMAPPED_STATE", eventSubmit); ok {
			t.Errorf("expected false for unmapped state")
		}
	})
}
