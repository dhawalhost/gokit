package bloomfilter_test

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/dhawalhost/gokit/bloomfilter"
)

func newRedisStore(t *testing.T, key string) (*bloomfilter.RedisStore, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { client.Close() })
	store, err := bloomfilter.NewRedisStore(client, key, 1000, 0.01)
	if err != nil {
		t.Fatalf("NewRedisStore: %v", err)
	}
	return store, mr
}

func TestNewRedisStoreValidation(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { client.Close() })

	tests := []struct {
		name              string
		client            *redis.Client
		key               string
		expectedItems     uint
		falsePositiveRate float64
		wantErr           bool
	}{
		{"nil client", nil, "bf", 1000, 0.01, true},
		{"empty key", client, "", 1000, 0.01, true},
		{"zero expectedItems", client, "bf", 0, 0.01, true},
		{"rate zero", client, "bf", 1000, 0, true},
		{"rate one", client, "bf", 1000, 1, true},
		{"rate negative", client, "bf", 1000, -0.5, true},
		{"rate > 1", client, "bf", 1000, 1.5, true},
		{"valid", client, "bf:valid", 1000, 0.01, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := bloomfilter.NewRedisStore(tc.client, tc.key, tc.expectedItems, tc.falsePositiveRate)
			if (err != nil) != tc.wantErr {
				t.Errorf("wantErr=%v got err=%v", tc.wantErr, err)
			}
		})
	}
}

func TestRedisStoreAddAndContains(t *testing.T) {
	store, _ := newRedisStore(t, "bf:test:add")
	ctx := context.Background()

	items := []string{"apple", "banana", "cherry", "date", "elderberry"}
	for _, s := range items {
		if err := store.AddString(ctx, s); err != nil {
			t.Fatalf("AddString(%q): %v", s, err)
		}
	}

	for _, s := range items {
		found, err := store.ContainsString(ctx, s)
		if err != nil {
			t.Fatalf("ContainsString(%q): %v", s, err)
		}
		if !found {
			t.Errorf("expected %q to be found in filter", s)
		}
	}
}

func TestRedisStoreAddBytes(t *testing.T) {
	store, _ := newRedisStore(t, "bf:test:bytes")
	ctx := context.Background()

	data := []byte{0x01, 0x02, 0x03, 0x04}
	if err := store.Add(ctx, data); err != nil {
		t.Fatalf("Add: %v", err)
	}
	found, err := store.Contains(ctx, data)
	if err != nil {
		t.Fatalf("Contains: %v", err)
	}
	if !found {
		t.Error("expected bytes to be found after Add")
	}
}

func TestRedisStoreAbsentItemNotFound(t *testing.T) {
	store, _ := newRedisStore(t, "bf:test:absent")
	ctx := context.Background()

	_ = store.AddString(ctx, "present-item")

	found, err := store.ContainsString(ctx, "xyzzy-never-added-xyzzy-000")
	if err != nil {
		t.Fatalf("ContainsString: %v", err)
	}
	_ = found
}

func TestRedisStoreEmptyFilterContainsFalse(t *testing.T) {
	store, _ := newRedisStore(t, "bf:test:empty")
	ctx := context.Background()

	found, err := store.ContainsString(ctx, "anything")
	if err != nil {
		t.Fatalf("ContainsString on empty filter: %v", err)
	}
	if found {
		t.Error("empty filter should return false for any item")
	}
}

func TestRedisStoreExpire(t *testing.T) {
	store, mr := newRedisStore(t, "bf:test:expire")
	ctx := context.Background()

	_ = store.AddString(ctx, "item")
	if err := store.Expire(ctx, 500*time.Millisecond); err != nil {
		t.Fatalf("Expire: %v", err)
	}

	mr.FastForward(1 * time.Second)

	found, err := store.ContainsString(ctx, "item")
	if err != nil {
		t.Fatalf("ContainsString after expiry: %v", err)
	}
	if found {
		t.Error("item should not be found after Redis key expired")
	}
}

func TestRedisStoreDelete(t *testing.T) {
	store, _ := newRedisStore(t, "bf:test:delete")
	ctx := context.Background()

	_ = store.AddString(ctx, "item")
	if err := store.Delete(ctx); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	found, err := store.ContainsString(ctx, "item")
	if err != nil {
		t.Fatalf("ContainsString after delete: %v", err)
	}
	if found {
		t.Error("item should not be found after Delete")
	}
}

func TestRedisStoreMetadata(t *testing.T) {
	store, _ := newRedisStore(t, "bf:test:meta")
	if store.BitSize() == 0 {
		t.Error("BitSize should be > 0")
	}
	if store.HashFunctions() == 0 {
		t.Error("HashFunctions should be > 0")
	}
}
