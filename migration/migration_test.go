package migration_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/dhawalhost/gokit/distlock"
	"github.com/dhawalhost/gokit/migration"
)

func TestParseMigrationFilename(t *testing.T) {
	t.Run("valid filenames", func(t *testing.T) {
		v, name, dir, err := migration.ParseMigrationFilename("000001_create_users.up.sql")
		if err != nil || v != 1 || name != "create_users" || dir != "up" {
			t.Errorf("unexpected parse: %d, %s, %s (%v)", v, name, dir, err)
		}

		v, name, dir, err = migration.ParseMigrationFilename("20261026_add_indices.down.sql")
		if err != nil || v != 20261026 || name != "add_indices" || dir != "down" {
			t.Errorf("unexpected parse: %d, %s, %s (%v)", v, name, dir, err)
		}
	})

	t.Run("invalid filenames", func(t *testing.T) {
		invalid := []string{
			"001_create_users.txt",
			"create_users.up.sql",
			"001_create_users.invalid.sql",
			"001_create_users.up.down.sql",
			"-5_create_users.up.sql",
			"001_.up.sql",
		}
		for _, file := range invalid {
			if _, _, _, err := migration.ParseMigrationFilename(file); !errors.Is(err, migration.ErrInvalidMigration) {
				t.Errorf("expected ErrInvalidMigration for %s, got: %v", file, err)
			}
		}
	})
}

func TestLoadFromFS(t *testing.T) {
	t.Run("successful loading", func(t *testing.T) {
		fsys := fstest.MapFS{
			"migrations/001_init.up.sql":        &fstest.MapFile{Data: []byte("CREATE TABLE users (id INT);")},
			"migrations/001_init.down.sql":      &fstest.MapFile{Data: []byte("DROP TABLE users;")},
			"migrations/002_add_email.up.sql":   &fstest.MapFile{Data: []byte("ALTER TABLE users ADD email TEXT;")},
			"migrations/002_add_email.down.sql": &fstest.MapFile{Data: []byte("ALTER TABLE users DROP COLUMN email;")},
			"migrations/003_orders.up.sql":      &fstest.MapFile{Data: []byte("CREATE TABLE orders (id INT);")},
			"migrations/notes.txt":              &fstest.MapFile{Data: []byte("ignored")},
		}

		migs, err := migration.LoadFromFS(fsys, "migrations")
		if err != nil {
			t.Fatalf("failed to load migrations from FS: %v", err)
		}

		if len(migs) != 3 {
			t.Fatalf("expected 3 migrations, got %d", len(migs))
		}
		if migs[0].Version != 1 || migs[1].Version != 2 || migs[2].Version != 3 {
			t.Errorf("expected ordered versions 1, 2, 3")
		}
		if migs[0].UpSQL != "CREATE TABLE users (id INT);" || migs[0].DownSQL != "DROP TABLE users;" {
			t.Errorf("unexpected SQL content in migration 1: %+v", migs[0])
		}
	})

	t.Run("no migrations found", func(t *testing.T) {
		fsys := fstest.MapFS{
			"empty/notes.txt": &fstest.MapFile{Data: []byte("no sql")},
		}
		_, err := migration.LoadFromFS(fsys, "empty")
		if !errors.Is(err, migration.ErrNoMigrationsFound) {
			t.Errorf("expected ErrNoMigrationsFound, got: %v", err)
		}
	})

	t.Run("missing up migration", func(t *testing.T) {
		fsys := fstest.MapFS{
			"migrations/001_init.down.sql": &fstest.MapFile{Data: []byte("DROP TABLE users;")},
		}
		_, err := migration.LoadFromFS(fsys, "migrations")
		if !errors.Is(err, migration.ErrInvalidMigration) {
			t.Errorf("expected ErrInvalidMigration for missing .up.sql, got: %v", err)
		}
	})

	t.Run("duplicate version", func(t *testing.T) {
		fsys := fstest.MapFS{
			"migrations/001_init.up.sql":       &fstest.MapFile{Data: []byte("CREATE TABLE t1;")},
			"migrations/001_other_name.up.sql": &fstest.MapFile{Data: []byte("CREATE TABLE t2;")},
		}
		_, err := migration.LoadFromFS(fsys, "migrations")
		if !errors.Is(err, migration.ErrInvalidMigration) {
			t.Errorf("expected ErrInvalidMigration for duplicate version, got: %v", err)
		}
	})

	t.Run("read dir error", func(t *testing.T) {
		fsys := fstest.MapFS{}
		_, err := migration.LoadFromFS(fsys, "nonexistent")
		if err == nil {
			t.Errorf("expected error reading nonexistent dir")
		}
	})

	t.Run("invalid sql filename in dir", func(t *testing.T) {
		fsys := fstest.MapFS{
			"migrations/bad_format.sql": &fstest.MapFile{Data: []byte("CREATE TABLE t1;")},
		}
		_, err := migration.LoadFromFS(fsys, "migrations")
		if !errors.Is(err, migration.ErrInvalidMigration) {
			t.Errorf("expected ErrInvalidMigration, got %v", err)
		}
	})
}

// Mock DB for in-memory testing of migration lifecycle without external database
type mockRecord struct {
	version   int64
	dirty     bool
	appliedAt time.Time
}

type mockDB struct {
	mu            sync.Mutex
	records       map[int64]mockRecord
	queries       []string
	failNextTx    bool
	failNextExec  bool
	failNextQuery bool
	execCount     int
	failExecAt    int
}

func newMockDB() *mockDB {
	return &mockDB{
		records: make(map[int64]mockRecord),
	}
}

type mockResult struct{}

func (m mockResult) LastInsertId() (int64, error) { return 0, nil }
func (m mockResult) RowsAffected() (int64, error) { return 1, nil }

func (m *mockDB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.execCount++
	if m.failExecAt > 0 && m.execCount == m.failExecAt {
		return nil, errors.New("simulated exec error at index")
	}

	if m.failNextExec {
		m.failNextExec = false
		return nil, errors.New("simulated exec error")
	}

	m.queries = append(m.queries, query)

	if strings.Contains(query, "DELETE FROM") {
		if len(args) > 0 {
			if v, ok := args[0].(int64); ok {
				delete(m.records, v)
			}
		} else {
			m.records = make(map[int64]mockRecord)
		}
	} else if strings.Contains(query, "INSERT INTO") {
		if len(args) >= 3 {
			v := args[0].(int64)
			dirty := args[1].(bool)
			t := args[2].(time.Time)
			m.records[v] = mockRecord{version: v, dirty: dirty, appliedAt: t}
		}
	} else if strings.Contains(query, "UPDATE") && strings.Contains(query, "dirty = $1") {
		if len(args) >= 3 {
			dirty := args[0].(bool)
			t := args[1].(time.Time)
			v := args[2].(int64)
			m.records[v] = mockRecord{version: v, dirty: dirty, appliedAt: t}
		}
	}

	return mockResult{}, nil
}

type mockRow struct {
	record *mockRecord
}

func (r *mockRow) Scan(dest ...any) error {
	if r.record == nil {
		return sql.ErrNoRows
	}
	*dest[0].(*int64) = r.record.version
	*dest[1].(*bool) = r.record.dirty
	return nil
}

func (m *mockDB) QueryRowContext(ctx context.Context, query string, args ...any) migration.RowScanner {
	m.mu.Lock()
	defer m.mu.Unlock()

	var highest *mockRecord
	for _, rec := range m.records {
		r := rec
		if highest == nil || r.version > highest.version {
			highest = &r
		}
	}
	return &mockRow{record: highest}
}

type mockRows struct {
	records []mockRecord
	idx     int
}

func (r *mockRows) Next() bool {
	return r.idx < len(r.records)
}

func (r *mockRows) Scan(dest ...any) error {
	rec := r.records[r.idx]
	r.idx++
	*dest[0].(*int64) = rec.version
	*dest[1].(*time.Time) = rec.appliedAt
	return nil
}

func (r *mockRows) Close() error { return nil }
func (r *mockRows) Err() error   { return nil }

func (m *mockDB) QueryContext(ctx context.Context, query string, args ...any) (migration.RowsScanner, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.failNextQuery {
		m.failNextQuery = false
		return nil, errors.New("simulated query error")
	}

	records := make([]mockRecord, 0, len(m.records))
	for _, rec := range m.records {
		records = append(records, rec)
	}
	return &mockRows{records: records}, nil
}

type mockTx struct {
	db        *mockDB
	committed bool
}

func (t *mockTx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return t.db.ExecContext(ctx, query, args...)
}

func (t *mockTx) Commit() error {
	t.committed = true
	return nil
}

func (t *mockTx) Rollback() error {
	return nil
}

func (m *mockDB) BeginTx(ctx context.Context, opts *sql.TxOptions) (migration.TxExecutor, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.failNextTx {
		m.failNextTx = false
		return nil, errors.New("simulated begin tx error")
	}

	return &mockTx{db: m}, nil
}

func TestMigrator_Lifecycle(t *testing.T) {
	ctx := context.Background()

	migs := []*migration.Migration{
		{Version: 1, Name: "create_users", UpSQL: "CREATE TABLE users;", DownSQL: "DROP TABLE users;"},
		{Version: 2, Name: "create_orders", UpSQL: "CREATE TABLE orders;", DownSQL: "DROP TABLE orders;"},
		{Version: 3, Name: "create_items", UpSQL: "CREATE TABLE items;", DownSQL: "DROP TABLE items;"},
	}

	t.Run("constructor validations", func(t *testing.T) {
		if _, err := migration.New(nil, migs); !errors.Is(err, migration.ErrInvalidMigration) {
			t.Errorf("expected ErrInvalidMigration on nil db")
		}

		db := newMockDB()
		if _, err := migration.New(db, migs, migration.WithTableName("invalid-table-name!")); !errors.Is(err, migration.ErrInvalidMigration) {
			t.Errorf("expected ErrInvalidMigration on bad table name")
		}

		dupMigs := []*migration.Migration{
			{Version: 1, Name: "init"},
			{Version: 1, Name: "init_again"},
		}
		if _, err := migration.New(db, dupMigs); !errors.Is(err, migration.ErrInvalidMigration) {
			t.Errorf("expected ErrInvalidMigration on duplicate version")
		}

		if _, err := migration.NewSQL(nil, migs); !errors.Is(err, migration.ErrInvalidMigration) {
			t.Errorf("expected ErrInvalidMigration on nil NewSQL")
		}
	})

	t.Run("up, down, and step lifecycle", func(t *testing.T) {
		db := newMockDB()
		locker := distlock.NewInMemoryLocker()
		migrator, err := migration.New(db, migs,
			migration.WithTableName("custom_migrations"),
			migration.WithLocker(locker, "test-mig-lock", 10*time.Second),
		)
		if err != nil {
			t.Fatalf("failed to create migrator: %v", err)
		}

		// Initial version should be 0
		v, dirty, err := migrator.Version(ctx)
		if err != nil || v != 0 || dirty {
			t.Errorf("expected version 0 clean, got: %d (dirty: %v)", v, dirty)
		}

		// UpStep 1
		if err := migrator.UpStep(ctx, 1); err != nil {
			t.Fatalf("UpStep(1) failed: %v", err)
		}
		v, _, _ = migrator.Version(ctx)
		if v != 1 {
			t.Errorf("expected version 1, got %d", v)
		}

		// Up remaining
		if err := migrator.Up(ctx); err != nil {
			t.Fatalf("Up() failed: %v", err)
		}
		v, _, _ = migrator.Version(ctx)
		if v != 3 {
			t.Errorf("expected version 3, got %d", v)
		}

		// Up again returns ErrNoChange
		if err := migrator.Up(ctx); !errors.Is(err, migration.ErrNoChange) {
			t.Errorf("expected ErrNoChange, got: %v", err)
		}

		// Status check
		statuses, err := migrator.Status(ctx)
		if err != nil || len(statuses) != 3 {
			t.Fatalf("failed to get status: %v", err)
		}
		for _, s := range statuses {
			if !s.Applied {
				t.Errorf("expected migration %d to be applied", s.Version)
			}
		}

		// DownStep 1
		if err := migrator.DownStep(ctx, 1); err != nil {
			t.Fatalf("DownStep(1) failed: %v", err)
		}
		v, _, _ = migrator.Version(ctx)
		if v != 2 {
			t.Errorf("expected version 2 after DownStep(1), got %d", v)
		}

		// Down all
		if err := migrator.Down(ctx); err != nil {
			t.Fatalf("Down() failed: %v", err)
		}
		v, _, _ = migrator.Version(ctx)
		if v != 0 {
			t.Errorf("expected version 0 after Down(), got %d", v)
		}

		// Down at version 0 returns ErrNoChange
		if err := migrator.Down(ctx); !errors.Is(err, migration.ErrNoChange) {
			t.Errorf("expected ErrNoChange on Down at 0, got: %v", err)
		}
	})

	t.Run("MigrateTo and Force", func(t *testing.T) {
		db := newMockDB()
		migrator, _ := migration.New(db, migs, migration.WithNoTransaction())

		// MigrateTo forward
		if err := migrator.MigrateTo(ctx, 2); err != nil {
			t.Fatalf("MigrateTo(2) failed: %v", err)
		}
		v, _, _ := migrator.Version(ctx)
		if v != 2 {
			t.Errorf("expected version 2, got %d", v)
		}

		// MigrateTo same version
		if err := migrator.MigrateTo(ctx, 2); !errors.Is(err, migration.ErrNoChange) {
			t.Errorf("expected ErrNoChange on MigrateTo(2), got: %v", err)
		}

		// MigrateTo backward
		if err := migrator.MigrateTo(ctx, 1); err != nil {
			t.Fatalf("MigrateTo(1) failed: %v", err)
		}
		v, _, _ = migrator.Version(ctx)
		if v != 1 {
			t.Errorf("expected version 1, got %d", v)
		}

		// MigrateTo missing version
		if err := migrator.MigrateTo(ctx, 999); !errors.Is(err, migration.ErrVersionNotFound) {
			t.Errorf("expected ErrVersionNotFound for 999, got: %v", err)
		}

		// Force clears and sets version
		if err := migrator.Force(ctx, 3); err != nil {
			t.Fatalf("Force(3) failed: %v", err)
		}
		v, dirty, _ := migrator.Version(ctx)
		if v != 3 || dirty {
			t.Errorf("expected version 3 clean after force, got %d (dirty: %v)", v, dirty)
		}
	})

	t.Run("dirty migration error handling", func(t *testing.T) {
		db := newMockDB()
		migrator, _ := migration.New(db, migs)

		// Inject dirty record
		db.records[1] = mockRecord{version: 1, dirty: true, appliedAt: time.Now()}

		if err := migrator.Up(ctx); !errors.Is(err, migration.ErrDirtyMigration) {
			t.Errorf("expected ErrDirtyMigration on Up, got: %v", err)
		}
		if err := migrator.Down(ctx); !errors.Is(err, migration.ErrDirtyMigration) {
			t.Errorf("expected ErrDirtyMigration on Down, got: %v", err)
		}
		if err := migrator.MigrateTo(ctx, 2); !errors.Is(err, migration.ErrDirtyMigration) {
			t.Errorf("expected ErrDirtyMigration on MigrateTo, got: %v", err)
		}
	})

	t.Run("missing down SQL rejection", func(t *testing.T) {
		db := newMockDB()
		oneWay := []*migration.Migration{
			{Version: 1, Name: "one_way", UpSQL: "CREATE TABLE t;"},
		}
		migrator, _ := migration.New(db, oneWay)
		_ = migrator.Up(ctx)

		if err := migrator.Down(ctx); !errors.Is(err, migration.ErrInvalidMigration) {
			t.Errorf("expected ErrInvalidMigration rolling back migration without down SQL, got: %v", err)
		}
	})

	t.Run("locked error propagation", func(t *testing.T) {
		db := newMockDB()
		badLocker := &alwaysFailLocker{}
		migrator, _ := migration.New(db, migs, migration.WithLocker(badLocker, "key", 10*time.Second))

		if err := migrator.Up(ctx); !errors.Is(err, migration.ErrMigrationLockFailed) {
			t.Errorf("expected ErrMigrationLockFailed on Up, got: %v", err)
		}
	})
}

type alwaysFailLocker struct{}

func (l *alwaysFailLocker) Obtain(ctx context.Context, key string, ttl time.Duration, opts ...distlock.Option) (distlock.Lock, error) {
	return nil, errors.New("lock busy")
}

func (l *alwaysFailLocker) WithLock(ctx context.Context, key string, ttl time.Duration, fn func(ctx context.Context) error, opts ...distlock.Option) error {
	return errors.New("lock busy")
}

func TestMigrator_EdgeCases(t *testing.T) {
	ctx := context.Background()
	migs := []*migration.Migration{
		{Version: 1, Name: "init", UpSQL: "CREATE TABLE t;", DownSQL: "DROP TABLE t;"},
	}

	t.Run("zero steps return nil", func(t *testing.T) {
		db := newMockDB()
		m, _ := migration.New(db, migs)
		if err := m.UpStep(ctx, 0); err != nil {
			t.Errorf("expected nil on UpStep(0)")
		}
		if err := m.DownStep(ctx, 0); err != nil {
			t.Errorf("expected nil on DownStep(0)")
		}
	})

	t.Run("force version 0", func(t *testing.T) {
		db := newMockDB()
		m, _ := migration.New(db, migs)
		_ = m.Up(ctx)
		if err := m.Force(ctx, 0); err != nil {
			t.Fatalf("Force(0) failed: %v", err)
		}
		v, _, _ := m.Version(ctx)
		if v != 0 {
			t.Errorf("expected version 0 after Force(0), got: %d", v)
		}
	})

	t.Run("tx begin failure in apply and rollback", func(t *testing.T) {
		db := newMockDB()
		m, _ := migration.New(db, migs)

		db.failNextTx = true
		if err := m.Up(ctx); err == nil {
			t.Errorf("expected error when begin tx fails on apply")
		}

		_ = m.Force(ctx, 1)
		db.failNextTx = true
		if err := m.Down(ctx); err == nil {
			t.Errorf("expected error when begin tx fails on rollback")
		}
	})

	t.Run("tx execution failure in apply and rollback", func(t *testing.T) {
		db := newMockDB()
		m, _ := migration.New(db, migs)

		db.failNextExec = true
		if err := m.Up(ctx); err == nil {
			t.Errorf("expected error when exec fails in tx on apply")
		}
	})

	t.Run("NoTransaction execution failure in apply and rollback", func(t *testing.T) {
		db := newMockDB()
		m, _ := migration.New(db, migs, migration.WithNoTransaction())

		db.failNextExec = true
		if err := m.Up(ctx); err == nil {
			t.Errorf("expected error when Up fails with NoTransaction")
		}

		_ = m.Force(ctx, 1)
		db.failNextExec = true
		if err := m.Down(ctx); err == nil {
			t.Errorf("expected error when Down fails with NoTransaction")
		}
	})

	t.Run("Force failures", func(t *testing.T) {
		db := newMockDB()
		m, _ := migration.New(db, migs)

		db.failNextExec = true
		if err := m.Force(ctx, 1); err == nil {
			t.Errorf("expected error when delete fails in Force")
		}

		db.failExecAt = 3 // Exec 1: Init table, Exec 2: delete, Exec 3: insert fails
		if err := m.Force(ctx, 2); err == nil {
			t.Errorf("expected error when insert fails in Force")
		}
	})

	t.Run("apply cleanQuery and rollback delQuery failures", func(t *testing.T) {
		db := newMockDB()
		m, _ := migration.New(db, migs)
		db.failExecAt = 4
		if err := m.Up(ctx); err == nil {
			t.Errorf("expected error when cleanQuery fails")
		}

		db2 := newMockDB()
		m2, _ := migration.New(db2, migs)
		_ = m2.Force(ctx, 1)
		db2.execCount = 0
		db2.failExecAt = 3
		if err := m2.Down(ctx); err == nil {
			t.Errorf("expected error when delQuery fails in rollback")
		}
	})

	t.Run("Status query failure", func(t *testing.T) {
		db := newMockDB()
		m, _ := migration.New(db, migs)

		db.failNextQuery = true
		if _, err := m.Status(ctx); err == nil {
			t.Errorf("expected error when query fails in Status")
		}
	})

	t.Run("NewSQL constructor and sqlDBWrapper", func(t *testing.T) {
		initMockDriver()
		db, err := sql.Open("mocksql", "fake")
		if err != nil {
			t.Fatalf("failed to open sql: %v", err)
		}
		defer db.Close()

		// nil DB check
		if _, err := migration.NewSQL(nil, migs); !errors.Is(err, migration.ErrInvalidMigration) {
			t.Errorf("expected ErrInvalidMigration for nil sql.DB")
		}

		m, err := migration.NewSQL(db, migs)
		if err != nil {
			t.Fatalf("failed to construct with NewSQL: %v", err)
		}

		if err := m.Init(ctx); err != nil {
			t.Errorf("Init failed: %v", err)
		}

		v, dirty, err := m.Version(ctx)
		if err != nil {
			t.Errorf("Version failed: %v", err)
		}
		if v != 0 || dirty {
			t.Errorf("unexpected version: %d, %v", v, dirty)
		}

		if err := m.UpStep(ctx, 1); err != nil {
			t.Errorf("UpStep failed: %v", err)
		}

		status, err := m.Status(ctx)
		if err != nil {
			t.Errorf("Status failed: %v", err)
		}
		if len(status) != 1 {
			t.Errorf("expected 1 status, got %d", len(status))
		}
	})
}

type mockSQLDriver struct{}

func (d *mockSQLDriver) Open(name string) (driver.Conn, error) {
	return &mockSQLConn{}, nil
}

type mockSQLConn struct{}

func (c *mockSQLConn) Prepare(query string) (driver.Stmt, error) {
	return &mockSQLStmt{query: query}, nil
}

func (c *mockSQLConn) Close() error {
	return nil
}

func (c *mockSQLConn) Begin() (driver.Tx, error) {
	return &mockSQLTx{}, nil
}

type mockSQLTx struct{}

func (t *mockSQLTx) Commit() error   { return nil }
func (t *mockSQLTx) Rollback() error { return nil }

type mockSQLStmt struct {
	query string
}

func (s *mockSQLStmt) Close() error { return nil }

func (s *mockSQLStmt) NumInput() int { return -1 }

func (s *mockSQLStmt) Exec(args []driver.Value) (driver.Result, error) {
	return driver.RowsAffected(1), nil
}

func (s *mockSQLStmt) Query(args []driver.Value) (driver.Rows, error) {
	if strings.Contains(s.query, "SELECT version, applied_at") {
		return &mockSQLRows{
			cols: []string{"version", "applied_at"},
			data: [][]driver.Value{
				{int64(1), time.Now()},
			},
		}, nil
	}
	return &mockSQLRows{
		cols: []string{"version", "dirty"},
		data: [][]driver.Value{
			{int64(0), false},
		},
	}, nil
}

type mockSQLRows struct {
	cols []string
	data [][]driver.Value
	idx  int
}

func (r *mockSQLRows) Columns() []string { return r.cols }
func (r *mockSQLRows) Close() error      { return nil }
func (r *mockSQLRows) Next(dest []driver.Value) error {
	if r.idx >= len(r.data) {
		return io.EOF
	}
	row := r.data[r.idx]
	r.idx++
	for i := range row {
		dest[i] = row[i]
	}
	return nil
}

var registerOnce sync.Once

func initMockDriver() {
	registerOnce.Do(func() {
		sql.Register("mocksql", &mockSQLDriver{})
	})
}
