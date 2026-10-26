package eventstore

// AggregateRoot defines the contract for domain entities whose state is derived
// by applying an ordered sequence of domain events.
type AggregateRoot interface {
	// AggregateID returns the unique domain identifier of this aggregate instance.
	AggregateID() string

	// AggregateType returns the logical type name of this aggregate (e.g. "Order", "BankAccount").
	AggregateType() string

	// Version returns the current persisted version of this aggregate (0 for new aggregates).
	Version() uint64

	// SetVersion updates the internal version counter of the aggregate root upon loading or saving.
	SetVersion(v uint64)

	// ApplyChange applies the event to mutate the internal state of the aggregate.
	// This must be deterministic and side-effect free.
	ApplyChange(event Event)

	// UncommittedEvents returns events generated during business logic execution that have not yet been saved.
	UncommittedEvents() []Event

	// ClearUncommittedEvents clears the pending uncommitted events slice after a successful save.
	ClearUncommittedEvents()
}

// BaseAggregate provides a standard, embeddable implementation of common AggregateRoot logic.
type BaseAggregate struct {
	id          string
	version     uint64
	uncommitted []Event
}

// SetID initializes or updates the aggregate's unique identifier.
func (b *BaseAggregate) SetID(id string) {
	b.id = id
}

// AggregateID returns the unique identifier.
func (b *BaseAggregate) AggregateID() string {
	return b.id
}

// Version returns the current version.
func (b *BaseAggregate) Version() uint64 {
	return b.version
}

// SetVersion sets the version of the aggregate.
func (b *BaseAggregate) SetVersion(v uint64) {
	b.version = v
}

// UncommittedEvents returns all uncommitted domain events.
func (b *BaseAggregate) UncommittedEvents() []Event {
	if len(b.uncommitted) == 0 {
		return nil
	}
	cp := make([]Event, len(b.uncommitted))
	copy(cp, b.uncommitted)
	return cp
}

// ClearUncommittedEvents resets the uncommitted events slice.
func (b *BaseAggregate) ClearUncommittedEvents() {
	b.uncommitted = nil
}

// TrackChange applies the event to the aggregate via ApplyChange and records it into the uncommitted events list.
func (b *BaseAggregate) TrackChange(agg AggregateRoot, event Event) {
	agg.ApplyChange(event)
	b.uncommitted = append(b.uncommitted, event)
}
