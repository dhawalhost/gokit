package workflow

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/dhawalhost/gokit/idgen"
)

// Engine manages workflow definitions, instance lifecycles, and state transitions.
type Engine struct {
	store       Store
	mu          sync.RWMutex
	definitions map[string]*Definition
	idGen       func() string
}

// EngineOption configures the Engine.
type EngineOption func(*Engine)

// WithIDGenerator sets a custom instance ID generator function.
func WithIDGenerator(gen func() string) EngineOption {
	return func(e *Engine) {
		if gen != nil {
			e.idGen = gen
		}
	}
}

// NewEngine initializes an Engine with the provided store and options.
// If store is nil, an in-memory store is used by default.
func NewEngine(store Store, opts ...EngineOption) *Engine {
	if store == nil {
		store = NewMemoryStore()
	}

	e := &Engine{
		store:       store,
		definitions: make(map[string]*Definition),
		idGen:       idgen.NewUUIDv7,
	}

	for _, opt := range opts {
		opt(e)
	}

	return e
}

// RegisterDefinition adds or updates a compiled workflow definition in the engine registry.
func (e *Engine) RegisterDefinition(def *Definition) error {
	if def == nil || def.Name == "" {
		return fmt.Errorf("%w: definition cannot be nil or unnamed", ErrInvalidDefinition)
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	e.definitions[def.Name] = def
	return nil
}

// GetDefinition retrieves a workflow definition by name.
func (e *Engine) GetDefinition(name string) (*Definition, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	def, exists := e.definitions[name]
	if !exists {
		return nil, ErrDefinitionNotFound
	}
	return def, nil
}

// InstanceOption configures workflow instance creation.
type InstanceOption func(*instanceConfig)

type instanceConfig struct {
	id           string
	metadata     map[string]any
	initialState State
}

// WithInstanceID sets an explicit unique ID for the new instance.
func WithInstanceID(id string) InstanceOption {
	return func(cfg *instanceConfig) {
		cfg.id = id
	}
}

// WithMetadata sets initial key-value metadata for the instance.
func WithMetadata(meta map[string]any) InstanceOption {
	return func(cfg *instanceConfig) {
		cfg.metadata = meta
	}
}

// WithInitialState overrides the default initial state configured in the definition.
func WithInitialState(state State) InstanceOption {
	return func(cfg *instanceConfig) {
		cfg.initialState = state
	}
}

// CreateInstance instantiates and persists a new workflow instance.
func (e *Engine) CreateInstance(ctx context.Context, defName string, opts ...InstanceOption) (*Instance, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	def, err := e.GetDefinition(defName)
	if err != nil {
		return nil, err
	}

	cfg := &instanceConfig{
		initialState: def.InitialState,
	}
	for _, opt := range opts {
		opt(cfg)
	}

	if cfg.id == "" {
		cfg.id = e.idGen()
	}

	now := time.Now().UTC()
	inst := &Instance{
		ID:             cfg.id,
		DefinitionName: defName,
		CurrentState:   cfg.initialState,
		Version:        1,
		Metadata:       cfg.metadata,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	if err := e.store.Save(ctx, inst); err != nil {
		return nil, err
	}

	return inst, nil
}

// GetInstance fetches a workflow instance by ID from the underlying store.
func (e *Engine) GetInstance(ctx context.Context, id string) (*Instance, error) {
	return e.store.Get(ctx, id)
}

// Fire executes a state transition triggered by the given event.
// It executes guards, exit hooks, the transition action, and entry hooks in strict sequence.
func (e *Engine) Fire(ctx context.Context, instanceID string, event Event, payload any) (*Instance, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	inst, err := e.store.Get(ctx, instanceID)
	if err != nil {
		return nil, err
	}

	def, err := e.GetDefinition(inst.DefinitionName)
	if err != nil {
		return nil, err
	}

	t, found := def.FindTransition(inst.CurrentState, event)
	if !found {
		return nil, fmt.Errorf("%w: no transition from state %q with event %q", ErrInvalidTransition, inst.CurrentState, event)
	}

	fromState := inst.CurrentState
	toState := t.To
	now := time.Now().UTC()

	evtCtx := &EventContext{
		InstanceID: inst.ID,
		FromState:  fromState,
		ToState:    toState,
		Event:      event,
		Payload:    payload,
		Metadata:   inst.Metadata,
		Version:    inst.Version,
		Timestamp:  now,
	}

	if t.Guard != nil {
		allowed := e.safeGuard(ctx, t.Guard, evtCtx)
		if !allowed {
			return nil, ErrGuardFailed
		}
	}

	start := time.Now()

	// 1. Execute Exit Hooks for current state
	if hooks, exists := def.exitHooks[fromState]; exists {
		for _, hook := range hooks {
			if hookErr := e.safeAction(ctx, hook, evtCtx); hookErr != nil {
				return nil, hookErr
			}
		}
	}

	// 2. Execute Transition Action
	if t.Action != nil {
		if actionErr := e.safeAction(ctx, t.Action, evtCtx); actionErr != nil {
			return nil, actionErr
		}
	}

	// 3. Execute Enter Hooks for destination state
	if hooks, exists := def.enterHooks[toState]; exists {
		for _, hook := range hooks {
			if hookErr := e.safeAction(ctx, hook, evtCtx); hookErr != nil {
				return nil, hookErr
			}
		}
	}

	// 4. Update instance state and audit trail
	inst.CurrentState = toState
	inst.Version++
	inst.UpdatedAt = time.Now().UTC()
	inst.History = append(inst.History, HistoryEntry{
		FromState: fromState,
		ToState:   toState,
		Event:     event,
		Timestamp: now,
		Duration:  time.Since(start),
	})

	if err := e.store.Save(ctx, inst); err != nil {
		return nil, err
	}

	return inst, nil
}

// CanFire evaluates whether the given event can currently be executed on the instance.
// Note: If the transition has a guard that requires a specific runtime payload, this evaluates with nil payload.
func (e *Engine) CanFire(ctx context.Context, instanceID string, event Event) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}

	inst, err := e.store.Get(ctx, instanceID)
	if err != nil {
		return false, err
	}

	def, err := e.GetDefinition(inst.DefinitionName)
	if err != nil {
		return false, err
	}

	t, found := def.FindTransition(inst.CurrentState, event)
	if !found {
		return false, nil
	}

	if t.Guard != nil {
		evtCtx := &EventContext{
			InstanceID: inst.ID,
			FromState:  inst.CurrentState,
			ToState:    t.To,
			Event:      event,
			Payload:    nil,
			Metadata:   inst.Metadata,
			Version:    inst.Version,
			Timestamp:  time.Now().UTC(),
		}
		return e.safeGuard(ctx, t.Guard, evtCtx), nil
	}

	return true, nil
}

// PermittedEvents returns the list of candidate events defined for the instance's current state.
func (e *Engine) PermittedEvents(ctx context.Context, instanceID string) ([]Event, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	inst, err := e.store.Get(ctx, instanceID)
	if err != nil {
		return nil, err
	}

	def, err := e.GetDefinition(inst.DefinitionName)
	if err != nil {
		return nil, err
	}

	return def.PermittedEvents(inst.CurrentState), nil
}

func (e *Engine) safeGuard(ctx context.Context, guard Guard, evtCtx *EventContext) (allowed bool) {
	defer func() {
		if r := recover(); r != nil {
			allowed = false
		}
	}()
	return guard(ctx, evtCtx)
}

func (e *Engine) safeAction(ctx context.Context, action Action, evtCtx *EventContext) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("workflow: action panicked: %v", r)
		}
	}()
	return action(ctx, evtCtx)
}
