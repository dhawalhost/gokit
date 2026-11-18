package eventstore

import "strconv"

type expectedVersionKind uint8

const (
	kindAny expectedVersionKind = iota
	kindNoStream
	kindExact
)

// ExpectedVersion defines optimistic concurrency constraints when appending events to a stream.
type ExpectedVersion struct {
	kind    expectedVersionKind
	version uint64
}

var (
	// ExpectedVersionAny disables optimistic concurrency checks. The events will be appended to the current tail.
	ExpectedVersionAny = ExpectedVersion{kind: kindAny}

	// ExpectedVersionNoStream requires that the stream does not exist yet. It will fail if the stream exists.
	ExpectedVersionNoStream = ExpectedVersion{kind: kindNoStream}
)

// ExpectedVersionExact creates an ExpectedVersion constraint requiring the stream to be at exactly version v.
func ExpectedVersionExact(v uint64) ExpectedVersion {
	return ExpectedVersion{kind: kindExact, version: v}
}

// Matches checks if the actual stream version satisfies this ExpectedVersion requirement.
func (ev ExpectedVersion) Matches(streamExists bool, actualVersion uint64) bool {
	switch ev.kind {
	case kindAny:
		return true
	case kindNoStream:
		return !streamExists
	case kindExact:
		if !streamExists {
			return false
		}
		return ev.version == actualVersion
	default:
		return false
	}
}

// String returns a human-readable representation of ExpectedVersion.
func (ev ExpectedVersion) String() string {
	switch ev.kind {
	case kindAny:
		return "ANY"
	case kindNoStream:
		return "NO_STREAM"
	case kindExact:
		return strconv.FormatUint(ev.version, 10)
	default:
		return "UNKNOWN"
	}
}
