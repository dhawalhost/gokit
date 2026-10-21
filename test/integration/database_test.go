//go:build integration
// +build integration

package integration_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/dhawalhost/gokit/config"
	"github.com/dhawalhost/gokit/database"
	"gorm.io/gorm"
)

type TestUser struct {
	ID        uint   `gorm:"primarykey"`
	Email     string `gorm:"uniqueIndex"`
	Name      string
	CreatedAt time.Time
}

func getTestDSN(t *testing.T) string {
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("TEST_DATABASE_DSN not set, skipping integration test")
	}
	return dsn
}

func TestDatabaseConnection(t *testing.T) {
	ctx := context.Background()
	dsn := getTestDSN(t)

	cfg := config.DatabaseConfig{
		DSN:             dsn,
		MaxOpenConns:    10,
		MaxIdleConns:    2,
		ConnMaxLifetime: 5 * time.Minute,
	}

	db, err := database.New(ctx, cfg)
	if err != nil {
		t.Fatalf("failed to connect to database: %v", err)
	}
	defer db.Close()

	if err := db.Ping(ctx); err != nil {
		t.Errorf("failed to ping database: %v", err)
	}
}

func TestDatabaseCRUD(t *testing.T) {
	ctx := context.Background()
	dsn := getTestDSN(t)

	cfg := config.DatabaseConfig{DSN: dsn}
	db, err := database.New(ctx, cfg)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer db.Close()

	if err := db.GORM.AutoMigrate(&TestUser{}); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}

	defer db.GORM.Exec("DROP TABLE IF EXISTS test_users")

	t.Run("Create", func(t *testing.T) {
		user := TestUser{
			Email: "test@example.com",
			Name:  "Test User",
		}
		if err := db.GORM.Create(&user).Error; err != nil {
			t.Errorf("failed to create user: %v", err)
		}
		if user.ID == 0 {
			t.Error("expected user ID to be set")
		}
	})

	t.Run("Read", func(t *testing.T) {
		var user TestUser
		if err := db.GORM.Where("email = ?", "test@example.com").First(&user).Error; err != nil {
			t.Errorf("failed to read user: %v", err)
		}
		if user.Name != "Test User" {
			t.Errorf("expected name 'Test User', got %q", user.Name)
		}
	})

	t.Run("Update", func(t *testing.T) {
		var user TestUser
		db.GORM.Where("email = ?", "test@example.com").First(&user)
		user.Name = "Updated User"
		if err := db.GORM.Save(&user).Error; err != nil {
			t.Errorf("failed to update user: %v", err)
		}

		var updated TestUser
		db.GORM.Where("email = ?", "test@example.com").First(&updated)
		if updated.Name != "Updated User" {
			t.Errorf("expected updated name, got %q", updated.Name)
		}
	})

	t.Run("Delete", func(t *testing.T) {
		var user TestUser
		db.GORM.Where("email = ?", "test@example.com").First(&user)
		if err := db.GORM.Delete(&user).Error; err != nil {
			t.Errorf("failed to delete user: %v", err)
		}

		var deleted TestUser
		err := db.GORM.Where("email = ?", "test@example.com").First(&deleted).Error
		if err != gorm.ErrRecordNotFound {
			t.Error("expected record not found error")
		}
	})
}

func TestDatabaseHealthCheck(t *testing.T) {
	ctx := context.Background()
	dsn := getTestDSN(t)

	cfg := config.DatabaseConfig{DSN: dsn}
	db, err := database.New(ctx, cfg)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer db.Close()

	if err := db.HealthCheck(ctx); err != nil {
		t.Errorf("health check failed: %v", err)
	}
}

func TestDatabaseRawQueries(t *testing.T) {
	ctx := context.Background()
	dsn := getTestDSN(t)

	cfg := config.DatabaseConfig{DSN: dsn}
	db, err := database.New(ctx, cfg)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer db.Close()

	var result int
	query := "SELECT 1 + 1 as result"
	row := db.Pool.QueryRow(ctx, query)
	if err := row.Scan(&result); err != nil {
		t.Errorf("failed to execute raw query: %v", err)
	}
	if result != 2 {
		t.Errorf("expected 2, got %d", result)
	}
}
