// Package database provides PostgreSQL database access via GORM and pgxpool.
package database

import (
	"context"
	"fmt"
	"io/fs"

	"github.com/dhawalhost/gokit/x/migration"
	"github.com/jackc/pgx/v5/pgxpool"
	"gorm.io/gorm"
)

// DB bundles a GORM database handle and a raw pgxpool connection pool.
type DB struct {
	// GORM is the ORM handle.
	GORM *gorm.DB
	// Pool is the raw pgx connection pool.
	Pool *pgxpool.Pool
}

// Close closes both the GORM underlying sql.DB and the pgxpool.
func (db *DB) Close() error {
	if db == nil {
		return nil
	}
	if db.GORM == nil && db.Pool == nil {
		return fmt.Errorf("database: uninitialized database handle")
	}
	if db.GORM != nil {
		sqlDB, err := db.GORM.DB()
		if err != nil {
			return fmt.Errorf("database: get sql.DB: %w", err)
		}
		if err := sqlDB.Close(); err != nil {
			return fmt.Errorf("database: close sql.DB: %w", err)
		}
	}
	if db.Pool != nil {
		db.Pool.Close()
	}
	return nil
}

// HealthCheck pings both the GORM sql.DB and the pgxpool.
func (db *DB) HealthCheck(ctx context.Context) error {
	return db.Ping(ctx)
}

// Migrate applies all pending schema migrations from the provided filesystem using gokit/migration.
// It leverages the underlying *sql.DB from GORM and seamlessly supports io/fs.FS and //go:embed.
// If dir is empty, it defaults to ".".
func (db *DB) Migrate(ctx context.Context, fsys fs.FS, dir string, opts ...migration.Option) error {
	if db == nil || db.GORM == nil {
		return fmt.Errorf("database: uninitialized database handle")
	}
	if dir == "" {
		dir = "."
	}
	sqlDB, err := db.GORM.DB()
	if err != nil {
		return fmt.Errorf("database: get sql.DB: %w", err)
	}
	migrations, err := migration.LoadFromFS(fsys, dir)
	if err != nil {
		return fmt.Errorf("database: load migrations: %w", err)
	}
	m, err := migration.NewSQL(sqlDB, migrations, opts...)
	if err != nil {
		return fmt.Errorf("database: create migrator: %w", err)
	}
	return m.Up(ctx)
}
