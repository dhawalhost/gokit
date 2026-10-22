package database

import (
	"context"
	"fmt"
)

// Ping pings both the GORM underlying sql.DB and the pgxpool.
func (db *DB) Ping(ctx context.Context) error {
	if db == nil || (db.GORM == nil && db.Pool == nil) {
		return fmt.Errorf("database: uninitialized database handle")
	}
	if db.GORM != nil {
		sqlDB, err := db.GORM.DB()
		if err != nil {
			return fmt.Errorf("database: get sql.DB: %w", err)
		}
		if err := sqlDB.PingContext(ctx); err != nil {
			return fmt.Errorf("database: sql.DB ping: %w", err)
		}
	}
	if db.Pool != nil {
		if err := db.Pool.Ping(ctx); err != nil {
			return fmt.Errorf("database: pgxpool ping: %w", err)
		}
	}
	return nil
}
