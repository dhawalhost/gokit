package migration

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"sort"
	"time"
)

var validTableNameRegex = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// RowScanner abstracts single-row scanning.
type RowScanner interface {
	Scan(dest ...any) error
}

// RowsScanner abstracts multi-row iteration and scanning.
type RowsScanner interface {
	Next() bool
	Scan(dest ...any) error
	Close() error
	Err() error
}

// TxExecutor abstracts transactional operations.
type TxExecutor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	Commit() error
	Rollback() error
}

// DB represents query and transaction execution capabilities.
type DB interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (RowsScanner, error)
	QueryRowContext(ctx context.Context, query string, args ...any) RowScanner
	BeginTx(ctx context.Context, opts *sql.TxOptions) (TxExecutor, error)
}

type sqlDBWrapper struct {
	db *sql.DB
}

func (w *sqlDBWrapper) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return w.db.ExecContext(ctx, query, args...)
}

func (w *sqlDBWrapper) QueryContext(ctx context.Context, query string, args ...any) (RowsScanner, error) {
	return w.db.QueryContext(ctx, query, args...)
}

func (w *sqlDBWrapper) QueryRowContext(ctx context.Context, query string, args ...any) RowScanner {
	return w.db.QueryRowContext(ctx, query, args...)
}

func (w *sqlDBWrapper) BeginTx(ctx context.Context, opts *sql.TxOptions) (TxExecutor, error) {
	return w.db.BeginTx(ctx, opts)
}

// Migrator manages versioned SQL schema execution and rollback state.
type Migrator struct {
	db         DB
	migrations []*Migration
	cfg        Config
}

// New creates a Migrator with the provided database handle, migrations list, and options.
func New(db DB, migrations []*Migration, opts ...Option) (*Migrator, error) {
	if db == nil {
		return nil, fmt.Errorf("%w: db handle cannot be nil", ErrInvalidMigration)
	}

	cfg := DefaultConfig()
	for _, opt := range opts {
		opt(&cfg)
	}

	if !validTableNameRegex.MatchString(cfg.TableName) {
		return nil, fmt.Errorf("%w: invalid table name %q", ErrInvalidMigration, cfg.TableName)
	}

	// Sort migrations ascending by version
	sorted := make([]*Migration, len(migrations))
	copy(sorted, migrations)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Version < sorted[j].Version
	})

	// Check for duplicate versions
	for i := 1; i < len(sorted); i++ {
		if sorted[i].Version == sorted[i-1].Version {
			return nil, fmt.Errorf("%w: duplicate version %d", ErrInvalidMigration, sorted[i].Version)
		}
	}

	return &Migrator{
		db:         db,
		migrations: sorted,
		cfg:        cfg,
	}, nil
}

// NewSQL initializes a Migrator directly from a standard library *sql.DB handle.
func NewSQL(db *sql.DB, migrations []*Migration, opts ...Option) (*Migrator, error) {
	if db == nil {
		return nil, fmt.Errorf("%w: db handle cannot be nil", ErrInvalidMigration)
	}
	return New(&sqlDBWrapper{db: db}, migrations, opts...)
}

// Init creates the migrations tracking table if it does not already exist.
func (m *Migrator) Init(ctx context.Context) error {
	query := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
		version BIGINT PRIMARY KEY,
		dirty BOOLEAN NOT NULL,
		applied_at TIMESTAMP NOT NULL
	)`, m.cfg.TableName)

	_, err := m.db.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("migration: init table %s: %w", m.cfg.TableName, err)
	}
	return nil
}

// Version returns the current database migration version and whether it is dirty.
func (m *Migrator) Version(ctx context.Context) (version int64, dirty bool, err error) {
	if err := m.Init(ctx); err != nil {
		return 0, false, err
	}

	query := fmt.Sprintf(`SELECT version, dirty FROM %s ORDER BY version DESC LIMIT 1`, m.cfg.TableName)
	row := m.db.QueryRowContext(ctx, query)

	err = row.Scan(&version, &dirty)
	if err != nil {
		if err == sql.ErrNoRows {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("migration: get version: %w", err)
	}
	return version, dirty, nil
}

// Up applies all pending migrations in ascending order.
func (m *Migrator) Up(ctx context.Context) error {
	return m.UpStep(ctx, len(m.migrations))
}

// UpStep applies up to the specified number of pending migrations.
func (m *Migrator) UpStep(ctx context.Context, steps int) error {
	if steps <= 0 {
		return nil
	}

	unlock, err := m.acquireLock(ctx)
	if err != nil {
		return err
	}
	defer unlock()

	currentVer, dirty, err := m.Version(ctx)
	if err != nil {
		return err
	}
	if dirty {
		return fmt.Errorf("%w: at version %d (manual intervention or Force required)", ErrDirtyMigration, currentVer)
	}

	var pending []*Migration
	for _, mig := range m.migrations {
		if mig.Version > currentVer {
			pending = append(pending, mig)
		}
	}

	if len(pending) == 0 {
		return ErrNoChange
	}

	if steps < len(pending) {
		pending = pending[:steps]
	}

	for _, mig := range pending {
		if err := m.applyMigration(ctx, mig); err != nil {
			return err
		}
	}

	return nil
}

// Down rolls back all applied migrations in descending order.
func (m *Migrator) Down(ctx context.Context) error {
	return m.DownStep(ctx, len(m.migrations))
}

// DownStep rolls back up to the specified number of applied migrations.
func (m *Migrator) DownStep(ctx context.Context, steps int) error {
	if steps <= 0 {
		return nil
	}

	unlock, err := m.acquireLock(ctx)
	if err != nil {
		return err
	}
	defer unlock()

	currentVer, dirty, err := m.Version(ctx)
	if err != nil {
		return err
	}
	if dirty {
		return fmt.Errorf("%w: at version %d", ErrDirtyMigration, currentVer)
	}
	if currentVer == 0 {
		return ErrNoChange
	}

	// Collect applied migrations in descending order
	var applied []*Migration
	for i := len(m.migrations) - 1; i >= 0; i-- {
		mig := m.migrations[i]
		if mig.Version <= currentVer {
			applied = append(applied, mig)
		}
	}

	if len(applied) == 0 {
		return ErrNoChange
	}

	if steps < len(applied) {
		applied = applied[:steps]
	}

	for _, mig := range applied {
		if err := m.rollbackMigration(ctx, mig); err != nil {
			return err
		}
	}

	return nil
}

// MigrateTo navigates forward or backward to the designated target version.
func (m *Migrator) MigrateTo(ctx context.Context, targetVersion int64) error {
	currentVer, dirty, err := m.Version(ctx)
	if err != nil {
		return err
	}
	if dirty {
		return fmt.Errorf("%w: at version %d", ErrDirtyMigration, currentVer)
	}

	if targetVersion == currentVer {
		return ErrNoChange
	}

	if targetVersion != 0 {
		found := false
		for _, mig := range m.migrations {
			if mig.Version == targetVersion {
				found = true
				break
			}
		}
		if !found {
			return ErrVersionNotFound
		}
	}

	if targetVersion > currentVer {
		count := 0
		for _, mig := range m.migrations {
			if mig.Version > currentVer && mig.Version <= targetVersion {
				count++
			}
		}
		if count == 0 {
			return ErrVersionNotFound
		}
		return m.UpStep(ctx, count)
	}

	// Rollback to targetVersion
	count := 0
	for i := len(m.migrations) - 1; i >= 0; i-- {
		mig := m.migrations[i]
		if mig.Version <= currentVer && mig.Version > targetVersion {
			count++
		}
	}
	if count == 0 {
		return ErrVersionNotFound
	}
	return m.DownStep(ctx, count)
}

// Force manually marks a version as applied and clears any dirty state.
func (m *Migrator) Force(ctx context.Context, version int64) error {
	unlock, err := m.acquireLock(ctx)
	if err != nil {
		return err
	}
	defer unlock()

	if err := m.Init(ctx); err != nil {
		return err
	}

	// Delete all existing entries and insert forced version
	delQuery := fmt.Sprintf(`DELETE FROM %s`, m.cfg.TableName)
	if _, err := m.db.ExecContext(ctx, delQuery); err != nil {
		return fmt.Errorf("migration: force delete: %w", err)
	}

	if version > 0 {
		insQuery := fmt.Sprintf(`INSERT INTO %s (version, dirty, applied_at) VALUES ($1, $2, $3)`, m.cfg.TableName)
		if _, err := m.db.ExecContext(ctx, insQuery, version, false, time.Now().UTC()); err != nil {
			return fmt.Errorf("migration: force insert: %w", err)
		}
	}

	return nil
}

// Status returns a detailed list of all migrations and their application status.
func (m *Migrator) Status(ctx context.Context) ([]Status, error) {
	if err := m.Init(ctx); err != nil {
		return nil, err
	}

	query := fmt.Sprintf(`SELECT version, applied_at FROM %s`, m.cfg.TableName)
	rows, err := m.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("migration: query status: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	appliedMap := make(map[int64]time.Time)
	for rows.Next() {
		var v int64
		var t time.Time
		if err := rows.Scan(&v, &t); err != nil {
			return nil, err
		}
		appliedMap[v] = t
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	statuses := make([]Status, len(m.migrations))
	for i, mig := range m.migrations {
		appliedAt, isApplied := appliedMap[mig.Version]
		s := Status{
			Version: mig.Version,
			Name:    mig.Name,
			Applied: isApplied,
		}
		if isApplied {
			s.AppliedAt = &appliedAt
		}
		statuses[i] = s
	}

	return statuses, nil
}

func (m *Migrator) applyMigration(ctx context.Context, mig *Migration) error {
	now := time.Now().UTC()

	// 1. Mark dirty
	dirtyQuery := fmt.Sprintf(`INSERT INTO %s (version, dirty, applied_at) VALUES ($1, $2, $3)
		ON CONFLICT (version) DO UPDATE SET dirty = EXCLUDED.dirty, applied_at = EXCLUDED.applied_at`, m.cfg.TableName)
	if _, err := m.db.ExecContext(ctx, dirtyQuery, mig.Version, true, now); err != nil {
		// Fallback for databases without ON CONFLICT (e.g. standard SQL)
		_ = m.db.QueryRowContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE version = $1`, m.cfg.TableName), mig.Version)
		_, _ = m.db.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s (version, dirty, applied_at) VALUES ($1, $2, $3)`, m.cfg.TableName), mig.Version, true, now)
	}

	// 2. Execute migration script
	if m.cfg.NoTransaction {
		if _, err := m.db.ExecContext(ctx, mig.UpSQL); err != nil {
			return fmt.Errorf("migration: execute up %d (%s): %w", mig.Version, mig.Name, err)
		}
	} else {
		tx, err := m.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("migration: begin tx: %w", err)
		}
		if _, err := tx.ExecContext(ctx, mig.UpSQL); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migration: execute up %d (%s): %w", mig.Version, mig.Name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("migration: commit tx: %w", err)
		}
	}

	// 3. Mark clean
	cleanQuery := fmt.Sprintf(`UPDATE %s SET dirty = $1, applied_at = $2 WHERE version = $3`, m.cfg.TableName)
	if _, err := m.db.ExecContext(ctx, cleanQuery, false, time.Now().UTC(), mig.Version); err != nil {
		return fmt.Errorf("migration: clean status %d: %w", mig.Version, err)
	}

	return nil
}

func (m *Migrator) rollbackMigration(ctx context.Context, mig *Migration) error {
	if mig.DownSQL == "" {
		return fmt.Errorf("%w: missing down SQL for version %d (%s)", ErrInvalidMigration, mig.Version, mig.Name)
	}

	// 1. Mark dirty
	dirtyQuery := fmt.Sprintf(`UPDATE %s SET dirty = $1 WHERE version = $2`, m.cfg.TableName)
	_, _ = m.db.ExecContext(ctx, dirtyQuery, true, mig.Version)

	// 2. Execute rollback
	if m.cfg.NoTransaction {
		if _, err := m.db.ExecContext(ctx, mig.DownSQL); err != nil {
			return fmt.Errorf("migration: execute down %d (%s): %w", mig.Version, mig.Name, err)
		}
	} else {
		tx, err := m.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("migration: begin tx: %w", err)
		}
		if _, err := tx.ExecContext(ctx, mig.DownSQL); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migration: execute down %d (%s): %w", mig.Version, mig.Name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("migration: commit tx: %w", err)
		}
	}

	// 3. Delete migration record
	delQuery := fmt.Sprintf(`DELETE FROM %s WHERE version = $1`, m.cfg.TableName)
	if _, err := m.db.ExecContext(ctx, delQuery, mig.Version); err != nil {
		return fmt.Errorf("migration: delete version %d: %w", mig.Version, err)
	}

	return nil
}

func (m *Migrator) acquireLock(ctx context.Context) (func(), error) {
	if m.cfg.Locker == nil {
		return func() {}, nil
	}
	lock, err := m.cfg.Locker.Obtain(ctx, m.cfg.LockKey, m.cfg.LockTTL)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMigrationLockFailed, err)
	}
	return func() {
		_ = lock.Unlock(context.Background())
	}, nil
}
