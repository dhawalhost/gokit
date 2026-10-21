package idgen_test

import (
	"strings"
	"testing"

	"github.com/dhawalhost/gokit/idgen"
)

func TestUUID_TableDriven(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "NewUUID generates valid 36-char UUIDv4",
			run: func(t *testing.T) {
				id := idgen.NewUUID()
				if len(id) != 36 {
					t.Errorf("expected 36 chars, got %d", len(id))
				}
				if !idgen.IsValidUUID(id) {
					t.Errorf("NewUUID returned invalid UUID: %q", id)
				}
			},
		},
		{
			name: "NewUUIDv7 generates valid UUID",
			run: func(t *testing.T) {
				id := idgen.NewUUIDv7()
				if !idgen.IsValidUUID(id) {
					t.Errorf("NewUUIDv7 returned invalid UUID: %q", id)
				}
			},
		},
		{
			name: "consecutive UUIDs are unique",
			run: func(t *testing.T) {
				ids := make(map[string]struct{})
				for i := 0; i < 100; i++ {
					id := idgen.NewUUID()
					if _, exists := ids[id]; exists {
						t.Fatal("duplicate UUID generated")
					}
					ids[id] = struct{}{}
				}
			},
		},
		{
			name: "IsValidUUID correctly identifies valid and invalid strings",
			run: func(t *testing.T) {
				if idgen.IsValidUUID("not-a-uuid") {
					t.Error("expected false for invalid UUID")
				}
				if !idgen.IsValidUUID("550e8400-e29b-41d4-a716-446655440000") {
					t.Error("expected true for valid UUID")
				}
			},
		},
		{
			name: "MustParseUUID parses valid UUID string",
			run: func(t *testing.T) {
				raw := "550e8400-e29b-41d4-a716-446655440000"
				parsed := idgen.MustParseUUID(raw)
				if parsed.String() != raw {
					t.Errorf("expected %q, got %q", raw, parsed.String())
				}
			},
		},
		{
			name: "MustParseUUID panics on invalid input",
			run: func(t *testing.T) {
				defer func() {
					if r := recover(); r == nil {
						t.Fatal("expected panic for invalid UUID")
					}
				}()
				idgen.MustParseUUID("not-valid")
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, tc.run)
	}
}

func TestULID_TableDriven(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "NewULID generates 26-char identifier",
			run: func(t *testing.T) {
				id := idgen.NewULID()
				if len(id) != 26 {
					t.Errorf("expected 26 chars, got %d", len(id))
				}
			},
		},
		{
			name: "consecutive ULIDs are unique",
			run: func(t *testing.T) {
				ids := make(map[string]struct{})
				for i := 0; i < 100; i++ {
					id := idgen.NewULID()
					if _, exists := ids[id]; exists {
						t.Fatal("duplicate ULID generated")
					}
					ids[id] = struct{}{}
				}
			},
		},
		{
			name: "monotonicity guarantees non-decreasing order in same millisecond",
			run: func(t *testing.T) {
				id1 := idgen.NewULID()
				id2 := idgen.NewULID()
				if strings.Compare(id1, id2) > 0 {
					t.Errorf("expected id2 >= id1, got id1=%s id2=%s", id1, id2)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, tc.run)
	}
}

func TestNanoID_TableDriven(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "NewNanoID generates non-empty ID",
			run: func(t *testing.T) {
				id, err := idgen.NewNanoID()
				if err != nil {
					t.Fatalf("NewNanoID: %v", err)
				}
				if len(id) == 0 {
					t.Fatal("expected non-empty NanoID")
				}
			},
		},
		{
			name: "MustNanoID generates non-empty ID without error",
			run: func(t *testing.T) {
				id := idgen.MustNanoID()
				if len(id) == 0 {
					t.Fatal("expected non-empty NanoID")
				}
			},
		},
		{
			name: "NewNanoIDSize generates exact requested length",
			run: func(t *testing.T) {
				id, err := idgen.NewNanoIDSize(10)
				if err != nil {
					t.Fatalf("NewNanoIDSize: %v", err)
				}
				if len(id) != 10 {
					t.Errorf("expected 10 chars, got %d", len(id))
				}
			},
		},
		{
			name: "NewNanoIDSize returns error on invalid size",
			run: func(t *testing.T) {
				_, err := idgen.NewNanoIDSize(-1)
				if err == nil {
					t.Fatal("expected error for negative NanoID size")
				}
			},
		},
		{
			name: "consecutive NanoIDs are unique",
			run: func(t *testing.T) {
				ids := make(map[string]struct{})
				for i := 0; i < 100; i++ {
					id, _ := idgen.NewNanoID()
					if _, exists := ids[id]; exists {
						t.Fatal("duplicate NanoID generated")
					}
					ids[id] = struct{}{}
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, tc.run)
	}
}
