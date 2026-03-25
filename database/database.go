// Package database provides PostgreSQL database access via GORM and pgxpool.
package database

import (
	"context"
	"fmt"

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
	sqlDB, err := db.GORM.DB()
	if err != nil {
		return fmt.Errorf("database: get sql.DB: %w", err)
	}
	if err := sqlDB.Close(); err != nil {
		return fmt.Errorf("database: close sql.DB: %w", err)
	}
	db.Pool.Close()
	return nil
}

// HealthCheck pings both the GORM sql.DB and the pgxpool.
func (db *DB) HealthCheck(ctx context.Context) error {
	return db.Ping(ctx)
}
