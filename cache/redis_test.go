package cache_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/dhawalhost/gokit/cache"
	"github.com/dhawalhost/gokit/config"
)

func newTestCache(t *testing.T) (*cache.RedisCache, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rc, err := cache.NewRedis(config.RedisConfig{Addr: mr.Addr()})
	if err != nil {
		t.Fatalf("NewRedis: %v", err)
	}
	return rc, mr
}

func downCache(t *testing.T) *cache.RedisCache {
	t.Helper()
	mr := miniredis.RunT(t)
	rc, err := cache.NewRedis(config.RedisConfig{Addr: mr.Addr()})
	if err != nil {
		t.Fatalf("NewRedis: %v", err)
	}
	mr.Close()
	return rc
}

func TestNewRedis(t *testing.T) {
	tests := []struct {
		name    string
		getAddr func(t *testing.T) string
		wantErr bool
	}{
		{
			"valid miniredis",
			func(t *testing.T) string { return miniredis.RunT(t).Addr() },
			false,
		},
		{
			"unreachable address",
			func(*testing.T) string { return "localhost:1" },
			true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := cache.NewRedis(config.RedisConfig{Addr: tc.getAddr(t)})
			if (err != nil) != tc.wantErr {
				t.Errorf("wantErr=%v got err=%v", tc.wantErr, err)
			}
		})
	}
}

func TestSetAndGet(t *testing.T) {
	rc, _ := newTestCache(t)
	ctx := context.Background()

	tests := []struct {
		name  string
		key   string
		value string
		ttl   time.Duration
	}{
		{"string value", "k1", "hello", time.Minute},
		{"empty value", "k2", "", time.Minute},
		{"long ttl", "k3", "world", 24 * time.Hour},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := rc.Set(ctx, tc.key, tc.value, tc.ttl); err != nil {
				t.Fatalf("Set: %v", err)
			}
			got, err := rc.Get(ctx, tc.key)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if got != tc.value {
				t.Errorf("expected %q, got %q", tc.value, got)
			}
		})
	}
}

func TestGetErrors(t *testing.T) {
	tests := []struct {
		name           string
		rc             func(t *testing.T) *cache.RedisCache
		key            string
		expectRedisNil bool
	}{
		{
			"missing key returns redis.Nil wrapped",
			func(t *testing.T) *cache.RedisCache { rc, _ := newTestCache(t); return rc },
			"no-such-key",
			true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.rc(t).Get(context.Background(), tc.key)
			if err == nil {
				t.Fatal("expected error")
			}
			if tc.expectRedisNil && !errors.Is(err, redis.Nil) {
				t.Errorf("expected wrapped redis.Nil, got %v", err)
			}
		})
	}
}

func TestSetErrorWhenDown(t *testing.T) {
	rc := downCache(t)
	err := rc.Set(context.Background(), "k", "v", time.Minute)
	if err == nil {
		t.Fatal("expected error when Redis is down")
	}
}

func TestGetErrorWhenDown(t *testing.T) {
	rc := downCache(t)
	_, err := rc.Get(context.Background(), "k")
	if err == nil {
		t.Fatal("expected error when Redis is down")
	}
}

func TestExists(t *testing.T) {
	rc, _ := newTestCache(t)
	ctx := context.Background()

	tests := []struct {
		name   string
		setup  func()
		key    string
		wantOk bool
	}{
		{"missing key", func() {}, "absent", false},
		{"present key", func() { _ = rc.Set(ctx, "present", "v", time.Minute) }, "present", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.setup()
			ok, err := rc.Exists(ctx, tc.key)
			if err != nil {
				t.Fatalf("Exists: %v", err)
			}
			if ok != tc.wantOk {
				t.Errorf("expected %v, got %v", tc.wantOk, ok)
			}
		})
	}
}

func TestExistsErrorWhenDown(t *testing.T) {
	rc := downCache(t)
	_, err := rc.Exists(context.Background(), "k")
	if err == nil {
		t.Fatal("expected error when Redis is down")
	}
}

func TestDelete(t *testing.T) {
	rc, _ := newTestCache(t)
	ctx := context.Background()

	tests := []struct {
		name    string
		setup   func(keys []string)
		keys    []string
		wantErr bool
	}{
		{
			"delete existing keys",
			func(keys []string) {
				for _, k := range keys {
					_ = rc.Set(ctx, k, "v", time.Minute)
				}
			},
			[]string{"d1", "d2"},
			false,
		},
		{
			"delete non-existent key is not an error",
			func(_ []string) {},
			[]string{"ghost"},
			false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.setup(tc.keys)
			err := rc.Delete(ctx, tc.keys...)
			if (err != nil) != tc.wantErr {
				t.Fatalf("wantErr=%v got %v", tc.wantErr, err)
			}
			if !tc.wantErr {
				for _, k := range tc.keys {
					ok, _ := rc.Exists(ctx, k)
					if ok {
						t.Errorf("key %q should have been deleted", k)
					}
				}
			}
		})
	}
}

func TestDeleteErrorWhenDown(t *testing.T) {
	rc := downCache(t)
	err := rc.Delete(context.Background(), "k")
	if err == nil {
		t.Fatal("expected error when Redis is down")
	}
}

func TestFlush(t *testing.T) {
	rc, _ := newTestCache(t)
	ctx := context.Background()

	for _, k := range []string{"a", "b", "c"} {
		_ = rc.Set(ctx, k, "1", time.Minute)
	}
	if err := rc.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	for _, k := range []string{"a", "b", "c"} {
		ok, _ := rc.Exists(ctx, k)
		if ok {
			t.Errorf("key %q should be gone after Flush", k)
		}
	}
}

func TestFlushErrorWhenDown(t *testing.T) {
	rc := downCache(t)
	err := rc.Flush(context.Background())
	if err == nil {
		t.Fatal("expected error when Redis is down")
	}
}

func TestHealthCheck(t *testing.T) {
	tests := []struct {
		name    string
		killSrv bool
		wantErr bool
	}{
		{"server up", false, false},
		{"server down", true, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mr := miniredis.RunT(t)
			rc, err := cache.NewRedis(config.RedisConfig{Addr: mr.Addr()})
			if err != nil {
				t.Fatalf("NewRedis: %v", err)
			}
			if tc.killSrv {
				mr.Close()
			}
			err = rc.HealthCheck(context.Background())
			if (err != nil) != tc.wantErr {
				t.Errorf("wantErr=%v got %v", tc.wantErr, err)
			}
		})
	}
}

func TestClose(t *testing.T) {
	rc, _ := newTestCache(t)
	if err := rc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestCloseDoubleClose(t *testing.T) {
	rc, _ := newTestCache(t)
	_ = rc.Close()
	err := rc.Close()
	if err == nil {
		t.Log("second close did not error — acceptable depending on go-redis version")
	}
}

func TestSetTTLExpiry(t *testing.T) {
	rc, mr := newTestCache(t)
	ctx := context.Background()

	_ = rc.Set(ctx, "ttlkey", "val", 1*time.Second)
	mr.FastForward(2 * time.Second)

	_, err := rc.Get(ctx, "ttlkey")
	if err == nil {
		t.Error("expected key to be expired after TTL")
	}
}
