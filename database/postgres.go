package database

import (
	"context"
	"fmt"
	"time"

	"github.com/dhawalhost/gokit/config"
	"github.com/jackc/pgx/v5/pgxpool"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// New opens a PostgreSQL database using both GORM (pgx-backed) and a raw pgxpool.
func New(ctx context.Context, cfg config.DatabaseConfig) (*DB, error) {
	// Open raw pgxpool.
	pool, err := pgxpool.New(ctx, cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("database: pgxpool.New: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("database: pgxpool ping: %w", err)
	}

	// Open GORM with pgx driver.
	gormDB, err := gorm.Open(postgres.Open(cfg.DSN), &gorm.Config{})
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("database: gorm.Open: %w", err)
	}

	// Apply connection pool settings via the underlying *sql.DB.
	sqlDB, err := gormDB.DB()
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("database: get sql.DB: %w", err)
	}
	if cfg.MaxOpenConns > 0 {
		sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	}
	if cfg.MaxIdleConns > 0 {
		sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
	}
	if cfg.ConnMaxLifetime > 0 {
		sqlDB.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	} else {
		sqlDB.SetConnMaxLifetime(5 * time.Minute)
	}

	return &DB{GORM: gormDB, Pool: pool}, nil
}
