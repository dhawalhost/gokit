package idgen_test

import (
	"strings"
	"testing"
	"time"

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

func TestShardIDGenerator_TableDriven(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "NewShardIDGenerator rejects shard ID above maximum",
			run: func(t *testing.T) {
				_, err := idgen.NewShardIDGenerator(8192) // max is 8191 (13 bits)
				if err == nil {
					t.Fatal("expected error for shardID exceeding 8191")
				}
			},
		},
		{
			name: "NextID generates valid 64-bit ID with correct embedded shard ID",
			run: func(t *testing.T) {
				shardID := uint16(480)
				gen, err := idgen.NewShardIDGenerator(shardID)
				if err != nil {
					t.Fatalf("failed to create ShardIDGenerator: %v", err)
				}

				id, err := gen.NextID()
				if err != nil {
					t.Fatalf("NextID error: %v", err)
				}

				if id <= 0 {
					t.Fatalf("expected positive int64 ID, got %d", id)
				}

				extractedShard := idgen.ExtractShardID(id)
				if extractedShard != shardID {
					t.Fatalf("expected extracted shard %d, got %d", shardID, extractedShard)
				}
			},
		},
		{
			name: "ExtractTime recovers timestamp close to generation time",
			run: func(t *testing.T) {
				gen, err := idgen.NewShardIDGenerator(42)
				if err != nil {
					t.Fatalf("failed to create ShardIDGenerator: %v", err)
				}

				id, err := gen.NextID()
				if err != nil {
					t.Fatalf("NextID error: %v", err)
				}

				extractedTime := idgen.ExtractTime(id)
				if extractedTime.IsZero() {
					t.Fatal("expected non-zero extracted time")
				}
			},
		},
		{
			name: "consecutive IDs are strictly increasing and unique",
			run: func(t *testing.T) {
				gen, err := idgen.NewShardIDGenerator(10)
				if err != nil {
					t.Fatalf("failed to create ShardIDGenerator: %v", err)
				}

				seen := make(map[int64]struct{}, 200)
				var lastID int64
				for i := 0; i < 200; i++ {
					id, err := gen.NextID()
					if err != nil {
						t.Fatalf("NextID error at step %d: %v", i, err)
					}
					if id <= lastID {
						t.Fatalf("expected monotonic increasing ID, last %d, current %d", lastID, id)
					}
					if _, exists := seen[id]; exists {
						t.Fatalf("duplicate ID generated: %d", id)
					}
					seen[id] = struct{}{}
					lastID = id
				}
			},
		},
		{
			name: "clock moving backwards returns error",
			run: func(t *testing.T) {
				gen, err := idgen.NewShardIDGenerator(1)
				if err != nil {
					t.Fatalf("NewShardIDGenerator: %v", err)
				}
				// Set lastTime far in the future
				gen.SetLastTimeForTest(time.Now().UnixMilli() + 100000)

				_, err = gen.NextID()
				if err == nil {
					t.Fatal("expected error when clock moved backwards")
				}
			},
		},
		{
			name: "sequence rollover within same millisecond awaits next tick",
			run: func(t *testing.T) {
				gen, err := idgen.NewShardIDGenerator(1)
				if err != nil {
					t.Fatalf("NewShardIDGenerator: %v", err)
				}
				// Set sequence to maxSequence and lastTime to current millisecond
				gen.SetLastTimeForTest(time.Now().UnixMilli())
				gen.SetSequenceForTest(4095) // maxSequence

				id, err := gen.NextID()
				if err != nil {
					t.Fatalf("NextID error on rollover: %v", err)
				}
				if id <= 0 {
					t.Fatalf("expected valid ID after rollover, got %d", id)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, tc.run)
	}
}
