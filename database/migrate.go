package database

import (
	"context"
	"fmt"

	migrate "github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres" // postgres driver
	_ "github.com/golang-migrate/migrate/v4/source/file"       // file source
)

// RunMigrations applies all pending up-migrations from migrationsPath.
func RunMigrations(_ context.Context, dsn, migrationsPath string) error {
	m, err := migrate.New("file://"+migrationsPath, dsn)
	if err != nil {
		return fmt.Errorf("database: migrate.New: %w", err)
	}
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("database: migrate up: %w", err)
	}
	return nil
}

// RollbackMigration rolls back the most recent migration.
func RollbackMigration(_ context.Context, dsn, migrationsPath string) error {
	m, err := migrate.New("file://"+migrationsPath, dsn)
	if err != nil {
		return fmt.Errorf("database: migrate.New: %w", err)
	}
	if err := m.Steps(-1); err != nil {
		return fmt.Errorf("database: migrate rollback: %w", err)
	}
	return nil
}
