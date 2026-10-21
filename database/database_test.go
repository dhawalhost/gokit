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
