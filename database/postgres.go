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
//
// SECURITY: SQL Injection Prevention
//
// When using GORM:
//   - ALWAYS use parameterized queries with ? placeholders
//   - NEVER concatenate user input into SQL strings
//   - Use db.Where("name = ?", userInput) NOT db.Where("name = " + userInput)
//   - GORM automatically escapes parameters in Where, Create, Update, etc.
//
// Example - SAFE:
//
//	db.GORM.Where("email = ?", userEmail).First(&user)
//	db.GORM.Where("age > ? AND city = ?", minAge, city).Find(&users)
//
// Example - UNSAFE (DO NOT DO THIS):
//
//	db.GORM.Where(fmt.Sprintf("email = '%s'", userEmail)).First(&user)  // VULNERABLE!
//	db.GORM.Exec("SELECT * FROM users WHERE id = " + userID)            // VULNERABLE!
//
// When using raw pgxpool:
//   - ALWAYS use $1, $2, etc. placeholders
//   - Use pool.Query(ctx, "SELECT * FROM users WHERE id = $1", userID)
//   - NEVER use string concatenation or fmt.Sprintf with user input
//
// For dynamic table/column names (which cannot be parameterized):
//   - Validate against a whitelist of allowed values
//   - Use a map or switch statement to validate input
//   - Never allow arbitrary user input as table/column names
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
