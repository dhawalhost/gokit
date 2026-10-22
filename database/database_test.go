package database_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dhawalhost/gokit/config"
	"github.com/dhawalhost/gokit/database"
	"gorm.io/gorm"
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

func TestReadYourWrites(t *testing.T) {
	tests := []struct {
		name              string
		key               string
		window            time.Duration
		sleepBeforeCheck  time.Duration
		sendHeader        bool
		method            string
		wantPrimaryForced bool
	}{
		{
			name:              "pinned user forces primary within window",
			key:               "user_123",
			window:            200 * time.Millisecond,
			sleepBeforeCheck:  50 * time.Millisecond,
			sendHeader:        false,
			method:            http.MethodGet,
			wantPrimaryForced: true,
		},
		{
			name:              "expired window does not force primary",
			key:               "user_456",
			window:            50 * time.Millisecond,
			sleepBeforeCheck:  100 * time.Millisecond,
			sendHeader:        false,
			method:            http.MethodGet,
			wantPrimaryForced: false,
		},
		{
			name:              "header X-Force-Primary forces primary even if unpinned",
			key:               "user_789",
			window:            0,
			sleepBeforeCheck:  0,
			sendHeader:        true,
			method:            http.MethodGet,
			wantPrimaryForced: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tracker := database.NewReadYourWritesTracker()
			if tc.window > 0 {
				tracker.Pin(tc.key, tc.window)
			}

			if tc.sleepBeforeCheck > 0 {
				time.Sleep(tc.sleepBeforeCheck)
			}

			var capturedPrimary bool
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capturedPrimary = database.IsPrimaryForced(r.Context())
				w.WriteHeader(http.StatusOK)
			})

			keyExtractor := func(r *http.Request) string {
				return tc.key
			}

			middleware := database.ReadYourWritesMiddleware(tracker, keyExtractor, 200*time.Millisecond)
			server := middleware(handler)

			req := httptest.NewRequest(tc.method, "/profile", nil)
			if tc.sendHeader {
				req.Header.Set("X-Force-Primary", "true")
			}
			rec := httptest.NewRecorder()

			server.ServeHTTP(rec, req)

			if capturedPrimary != tc.wantPrimaryForced {
				t.Errorf("expected forced primary = %v, got %v", tc.wantPrimaryForced, capturedPrimary)
			}
		})
	}
}

func TestReplicaRouter_GORM_TableDriven(t *testing.T) {
	primaryGorm := &gorm.DB{Config: &gorm.Config{}, Statement: &gorm.Statement{}}
	replicaGorm := &gorm.DB{Config: &gorm.Config{}, Statement: &gorm.Statement{}}
	primary := &database.DB{GORM: primaryGorm}
	replica := &database.DB{GORM: replicaGorm}

	tests := []struct {
		name       string
		forceWrite bool
		ctx        context.Context
	}{
		{
			name:       "read routes via GORM with context",
			forceWrite: false,
			ctx:        context.Background(),
		},
		{
			name:       "read with WithPrimary forces primary",
			forceWrite: false,
			ctx:        database.WithPrimary(context.Background()),
		},
		{
			name:       "WriteGORM returns primary context-bound handle",
			forceWrite: true,
			ctx:        context.Background(),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			router, err := database.NewReplicaRouter(primary, replica)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tc.forceWrite {
				handle := router.WriteGORM(tc.ctx)
				if handle == nil {
					t.Fatal("expected non-nil WriteGORM handle")
				}
			} else {
				handle := router.GORM(tc.ctx)
				if handle == nil {
					t.Fatal("expected non-nil GORM handle")
				}
			}
		})
	}
}

func TestReplicaRouter_HealthCheckAndClose_TableDriven(t *testing.T) {
	tests := []struct {
		name      string
		operation func(t *testing.T, router *database.ReplicaRouter)
	}{
		{
			name: "HealthCheck reports primary ping failure on uninitialized DB",
			operation: func(t *testing.T, router *database.ReplicaRouter) {
				err := router.HealthCheck(context.Background())
				if err == nil {
					t.Error("expected error from HealthCheck on uninitialized primary DB")
				}
			},
		},
		{
			name: "Close reports error when underlying sql.DB is invalid",
			operation: func(t *testing.T, router *database.ReplicaRouter) {
				err := router.Close()
				if err == nil {
					t.Error("expected error from Close on uninitialized primary DB")
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			primary := &database.DB{}
			replica := &database.DB{}
			router, err := database.NewReplicaRouter(primary, replica)
			if err != nil {
				t.Fatalf("NewReplicaRouter error: %v", err)
			}
			tc.operation(t, router)
		})
	}
}

func TestDB_CloseAndHealthCheck_TableDriven(t *testing.T) {
	tests := []struct {
		name      string
		operation func(t *testing.T, db *database.DB)
	}{
		{
			name: "HealthCheck propagates error",
			operation: func(t *testing.T, db *database.DB) {
				if err := db.HealthCheck(context.Background()); err == nil {
					t.Error("expected health check error")
				}
			},
		},
		{
			name: "Close propagates error",
			operation: func(t *testing.T, db *database.DB) {
				if err := db.Close(); err == nil {
					t.Error("expected close error")
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := &database.DB{}
			tc.operation(t, db)
		})
	}
}
