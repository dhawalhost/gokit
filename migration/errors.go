// Package migration provides a lightweight, driver-agnostic SQL schema migration engine
// supporting up/down migrations, embed.FS loading, transactional DDL, and distributed locking.
package migration

import (
	"errors"
)

var (
	// ErrNoMigrationsFound is returned when no migration scripts are found in the provided directory.
	ErrNoMigrationsFound = errors.New("migration: no migrations found")
	// ErrDirtyMigration is returned when the database schema is in an inconsistent, dirty state from a previous failed run.
	ErrDirtyMigration = errors.New("migration: database is in a dirty state")
	// ErrMigrationLockFailed is returned when the distributed migration lock cannot be acquired.
	ErrMigrationLockFailed = errors.New("migration: failed to acquire migration lock")
	// ErrInvalidMigration is returned when a migration file name, version, or format is invalid.
	ErrInvalidMigration = errors.New("migration: invalid migration definition")
	// ErrVersionNotFound is returned when a target migration version does not exist.
	ErrVersionNotFound = errors.New("migration: target version not found")
	// ErrNoChange is returned when all migrations are already up to date.
	ErrNoChange = errors.New("migration: no change")
)
