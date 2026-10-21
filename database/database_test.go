package database_test

import (
	"context"
	"testing"

	"github.com/dhawalhost/gokit/config"
	"github.com/dhawalhost/gokit/database"
)

func TestNewFailsWithBadDSN(t *testing.T) {
	tests := []struct {
		name string
		dsn  string
	}{
		{"unreachable host", "postgresql://user:pass@localhost:5999/testdb?connect_timeout=1"},
		{"invalid dsn format", "not-a-valid-dsn"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := database.New(context.Background(), config.DatabaseConfig{DSN: tc.dsn})
			if err == nil {
				t.Fatalf("expected error for DSN %q", tc.dsn)
			}
		})
	}
}

func TestRunMigrationsBadDSN(t *testing.T) {
	tests := []struct {
		name           string
		dsn            string
		migrationsPath string
	}{
		{"bad dsn", "postgresql://user:pass@localhost:5999/testdb", "/tmp/migrations"},
		{"invalid dsn format", "invalid-dsn", "/tmp/migrations"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := database.RunMigrations(context.Background(), tc.dsn, tc.migrationsPath)
			if err == nil {
				t.Fatalf("expected error for DSN %q", tc.dsn)
			}
		})
	}
}

func TestRollbackMigrationBadDSN(t *testing.T) {
	tests := []struct {
		name           string
		dsn            string
		migrationsPath string
	}{
		{"bad dsn", "postgresql://user:pass@localhost:5999/testdb", "/tmp/migrations"},
		{"invalid dsn format", "invalid-dsn", "/tmp/migrations"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := database.RollbackMigration(context.Background(), tc.dsn, tc.migrationsPath)
			if err == nil {
				t.Fatalf("expected error for DSN %q", tc.dsn)
			}
		})
	}
}

func TestReplicaRouter(t *testing.T) {
	t.Run("nil primary returns error", func(t *testing.T) {
		_, err := database.NewReplicaRouter(nil)
		if err == nil {
			t.Fatalf("expected error for nil primary, got nil")
		}
	})

	t.Run("write always returns primary", func(t *testing.T) {
		primary := &database.DB{}
		router, err := database.NewReplicaRouter(primary)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if router.Write() != primary {
			t.Errorf("expected write to return primary")
		}
	})

	t.Run("read without replicas returns primary", func(t *testing.T) {
		primary := &database.DB{}
		router, err := database.NewReplicaRouter(primary)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if router.Read(context.Background()) != primary {
			t.Errorf("expected read without replicas to return primary")
		}
	})

	t.Run("read round-robins across replicas", func(t *testing.T) {
		primary := &database.DB{}
		r1 := &database.DB{}
		r2 := &database.DB{}

		router, err := database.NewReplicaRouter(primary, r1, r2)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		ctx := context.Background()
		first := router.Read(ctx)
		second := router.Read(ctx)
		third := router.Read(ctx)

		if first != r1 || second != r2 || third != r1 {
			t.Errorf("expected round robin r1 -> r2 -> r1, got %v -> %v -> %v", first, second, third)
		}
	})

	t.Run("WithPrimary context forces primary read", func(t *testing.T) {
		primary := &database.DB{}
		r1 := &database.DB{}

		router, err := database.NewReplicaRouter(primary, r1)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		ctx := database.WithPrimary(context.Background())
		if !database.IsPrimaryForced(ctx) {
			t.Errorf("expected IsPrimaryForced to be true")
		}

		got := router.Read(ctx)
		if got != primary {
			t.Errorf("expected primary read when WithPrimary is set, got replica")
		}
	})
}
