package idgen_test

import (
	"strings"
	"testing"

	"github.com/dhawalhost/gokit/idgen"
)

func TestNewUUID(t *testing.T) {
	id := idgen.NewUUID()
	if len(id) == 0 {
		t.Fatal("expected non-empty UUID")
	}
	// UUID v4 has 36 characters with dashes
	if len(id) != 36 {
		t.Errorf("expected 36 chars, got %d", len(id))
	}
	if !idgen.IsValidUUID(id) {
		t.Errorf("NewUUID returned invalid UUID: %q", id)
	}
}

func TestNewUUIDv7(t *testing.T) {
	id := idgen.NewUUIDv7()
	if !idgen.IsValidUUID(id) {
		t.Errorf("NewUUIDv7 returned invalid UUID: %q", id)
	}
}

func TestUUIDsAreUnique(t *testing.T) {
	ids := make(map[string]struct{})
	for i := 0; i < 100; i++ {
		id := idgen.NewUUID()
		if _, exists := ids[id]; exists {
			t.Fatal("duplicate UUID generated")
		}
		ids[id] = struct{}{}
	}
}

func TestIsValidUUID(t *testing.T) {
	if idgen.IsValidUUID("not-a-uuid") {
		t.Error("expected false for invalid UUID")
	}
	if !idgen.IsValidUUID("550e8400-e29b-41d4-a716-446655440000") {
		t.Error("expected true for valid UUID")
	}
}

func TestMustParseUUID(t *testing.T) {
	raw := "550e8400-e29b-41d4-a716-446655440000"
	parsed := idgen.MustParseUUID(raw)
	if parsed.String() != raw {
		t.Errorf("expected %q, got %q", raw, parsed.String())
	}
}

func TestMustParseUUIDPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for invalid UUID")
		}
	}()
	idgen.MustParseUUID("not-valid")
}

func TestNewULID(t *testing.T) {
	id := idgen.NewULID()
	if len(id) == 0 {
		t.Fatal("expected non-empty ULID")
	}
	// ULID is 26 characters
	if len(id) != 26 {
		t.Errorf("expected 26 chars, got %d", len(id))
	}
}

func TestULIDsAreUnique(t *testing.T) {
	ids := make(map[string]struct{})
	for i := 0; i < 100; i++ {
		id := idgen.NewULID()
		if _, exists := ids[id]; exists {
			t.Fatal("duplicate ULID generated")
		}
		ids[id] = struct{}{}
	}
}

func TestNewNanoID(t *testing.T) {
	id, err := idgen.NewNanoID()
	if err != nil {
		t.Fatalf("NewNanoID: %v", err)
	}
	if len(id) == 0 {
		t.Fatal("expected non-empty NanoID")
	}
}

func TestMustNanoID(t *testing.T) {
	id := idgen.MustNanoID()
	if len(id) == 0 {
		t.Fatal("expected non-empty NanoID")
	}
}

func TestNewNanoIDSize(t *testing.T) {
	id, err := idgen.NewNanoIDSize(10)
	if err != nil {
		t.Fatalf("NewNanoIDSize: %v", err)
	}
	if len(id) != 10 {
		t.Errorf("expected 10 chars, got %d", len(id))
	}
}

func TestNewNanoIDSizeError(t *testing.T) {
	_, err := idgen.NewNanoIDSize(-1)
	if err == nil {
		t.Fatal("expected error for negative NanoID size")
	}
}

func TestNanoIDsAreUnique(t *testing.T) {
	ids := make(map[string]struct{})
	for i := 0; i < 100; i++ {
		id, _ := idgen.NewNanoID()
		if _, exists := ids[id]; exists {
			t.Fatal("duplicate NanoID generated")
		}
		ids[id] = struct{}{}
	}
}

func TestULIDMonotonicity(t *testing.T) {
	id1 := idgen.NewULID()
	id2 := idgen.NewULID()
	// ULIDs are monotonically increasing in the same millisecond due to the monotonic random.
	// String comparison works because ULID uses Crockford's base32.
	if strings.Compare(id1, id2) > 0 {
		t.Errorf("expected id2 >= id1, got id1=%s id2=%s", id1, id2)
	}
}
