package workflow

import (
	"fmt"
)

// Transition represents a valid path from one or more source states to a destination state triggered by an event.
type Transition struct {
	Event  Event
	From   []State
	To     State
	Guard  Guard
	Action Action
}

// TransitionOption configures a Transition.
type TransitionOption func(*Transition)

// WithGuard attaches a pre-condition guard to the transition.
func WithGuard(g Guard) TransitionOption {
	return func(t *Transition) {
		t.Guard = g
	}
}

// WithAction attaches a transition execution action.
func WithAction(a Action) TransitionOption {
	return func(t *Transition) {
		t.Action = a
	}
}

// Definition defines an immutable workflow specification.
type Definition struct {
	Name         string
	InitialState State
	transitions  map[State]map[Event]*Transition
	all          []*Transition
	enterHooks   map[State][]Action
	exitHooks    map[State][]Action
	states       map[State]struct{}
}

// FindTransition looks up a transition for the given state and event.
func (d *Definition) FindTransition(from State, event Event) (*Transition, bool) {
	if eventMap, ok := d.transitions[from]; ok {
		t, found := eventMap[event]
		return t, found
	}
	return nil, false
}

// PermittedEvents returns all events valid from the given state.
func (d *Definition) PermittedEvents(from State) []Event {
	eventMap, ok := d.transitions[from]
	if !ok {
		return nil
	}
	events := make([]Event, 0, len(eventMap))
	for ev := range eventMap {
		events = append(events, ev)
	}
	return events
}

// States returns all states present in the workflow definition.
func (d *Definition) States() []State {
	states := make([]State, 0, len(d.states))
	for st := range d.states {
		states = append(states, st)
	}
	return states
}

// Builder constructs and validates a Definition.
type Builder struct {
	name         string
	initialState State
	transitions  []*Transition
	enterHooks   map[State][]Action
	exitHooks    map[State][]Action
}

// NewBuilder initializes a new workflow builder with a name and initial state.
func NewBuilder(name string, initial State) *Builder {
	return &Builder{
		name:         name,
		initialState: initial,
		enterHooks:   make(map[State][]Action),
		exitHooks:    make(map[State][]Action),
	}
}

// AddTransition declares a valid transition from one or more source states to a destination state on event.
func (b *Builder) AddTransition(event Event, from []State, to State, opts ...TransitionOption) *Builder {
	t := &Transition{
		Event: event,
		From:  from,
		To:    to,
	}
	for _, opt := range opts {
		opt(t)
	}
	b.transitions = append(b.transitions, t)
	return b
}

// OnEnter registers an action executed whenever the machine enters the given state.
func (b *Builder) OnEnter(state State, action Action) *Builder {
	if action != nil {
		b.enterHooks[state] = append(b.enterHooks[state], action)
	}
	return b
}

// OnExit registers an action executed whenever the machine departs from the given state.
func (b *Builder) OnExit(state State, action Action) *Builder {
	if action != nil {
		b.exitHooks[state] = append(b.exitHooks[state], action)
	}
	return b
}

// Build validates and returns the compiled Definition.
func (b *Builder) Build() (*Definition, error) {
	if b.name == "" {
		return nil, fmt.Errorf("%w: workflow name cannot be empty", ErrInvalidDefinition)
	}
	if b.initialState == "" {
		return nil, fmt.Errorf("%w: initial state cannot be empty", ErrInvalidDefinition)
	}

	transitionsMap := make(map[State]map[Event]*Transition)
	allStates := make(map[State]struct{})
	allStates[b.initialState] = struct{}{}

	for _, t := range b.transitions {
		if t.Event == "" {
			return nil, fmt.Errorf("%w: transition event cannot be empty", ErrInvalidDefinition)
		}
		if t.To == "" {
			return nil, fmt.Errorf("%w: transition target state cannot be empty", ErrInvalidDefinition)
		}
		if len(t.From) == 0 {
			return nil, fmt.Errorf("%w: transition source states cannot be empty for event %q", ErrInvalidDefinition, t.Event)
		}

		allStates[t.To] = struct{}{}

		for _, from := range t.From {
			allStates[from] = struct{}{}

			if _, ok := transitionsMap[from]; !ok {
				transitionsMap[from] = make(map[Event]*Transition)
			}

			if _, exists := transitionsMap[from][t.Event]; exists {
				return nil, fmt.Errorf("%w: duplicate transition for state %q and event %q", ErrInvalidDefinition, from, t.Event)
			}

			transitionsMap[from][t.Event] = t
		}
	}

	return &Definition{
		Name:         b.name,
		InitialState: b.initialState,
		transitions:  transitionsMap,
		all:          b.transitions,
		enterHooks:   b.enterHooks,
		exitHooks:    b.exitHooks,
		states:       allStates,
	}, nil
}
