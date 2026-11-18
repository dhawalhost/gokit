package migration

import (
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Migration represents a versioned database schema migration step.
type Migration struct {
	Version int64  `json:"version"`
	Name    string `json:"name"`
	UpSQL   string `json:"up_sql"`
	DownSQL string `json:"down_sql"`
}

// Status reports the execution state of an individual migration.
type Status struct {
	Version   int64      `json:"version"`
	Name      string     `json:"name"`
	Applied   bool       `json:"applied"`
	AppliedAt *time.Time `json:"applied_at,omitempty"`
}

// ParseMigrationFilename extracts the version, descriptive name, and direction from a standard migration file name.
// Supported format: {version}_{name}.{up|down}.sql (e.g. 000001_create_users.up.sql).
func ParseMigrationFilename(filename string) (int64, string, string, error) {
	base := path.Base(filename)
	if !strings.HasSuffix(base, ".sql") {
		return 0, "", "", fmt.Errorf("%w: missing .sql extension: %s", ErrInvalidMigration, filename)
	}

	trimmed := strings.TrimSuffix(base, ".sql")
	parts := strings.Split(trimmed, ".")
	if len(parts) != 2 {
		return 0, "", "", fmt.Errorf("%w: expected {version}_{name}.{up|down}.sql, got: %s", ErrInvalidMigration, filename)
	}

	direction := strings.ToLower(parts[1])
	if direction != "up" && direction != "down" {
		return 0, "", "", fmt.Errorf("%w: invalid direction %q in %s", ErrInvalidMigration, direction, filename)
	}

	nameParts := strings.SplitN(parts[0], "_", 2)
	if len(nameParts) != 2 {
		return 0, "", "", fmt.Errorf("%w: expected {version}_{name}, got: %s", ErrInvalidMigration, parts[0])
	}

	version, err := strconv.ParseInt(nameParts[0], 10, 64)
	if err != nil || version <= 0 {
		return 0, "", "", fmt.Errorf("%w: invalid version number %q: %v", ErrInvalidMigration, nameParts[0], err)
	}

	name := nameParts[1]
	if name == "" {
		return 0, "", "", fmt.Errorf("%w: migration name cannot be empty: %s", ErrInvalidMigration, filename)
	}

	return version, name, direction, nil
}

// LoadFromFS scans a filesystem (os.DirFS or embed.FS) directory and parses all .up.sql and .down.sql migrations.
func LoadFromFS(fsys fs.FS, dir string) ([]*Migration, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("migration: read dir: %w", err)
	}

	byVersion := make(map[int64]*Migration)

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		if !strings.HasSuffix(name, ".sql") {
			continue
		}

		version, migName, direction, err := ParseMigrationFilename(name)
		if err != nil {
			return nil, err
		}

		filePath := path.Join(dir, name)
		content, err := fs.ReadFile(fsys, filePath)
		if err != nil {
			return nil, fmt.Errorf("migration: read file %s: %w", filePath, err)
		}

		m, exists := byVersion[version]
		if !exists {
			m = &Migration{
				Version: version,
				Name:    migName,
			}
			byVersion[version] = m
		} else if m.Name != migName {
			return nil, fmt.Errorf("%w: mismatched name for version %d (%s vs %s)", ErrInvalidMigration, version, m.Name, migName)
		}

		if direction == "up" {
			if m.UpSQL != "" {
				return nil, fmt.Errorf("%w: duplicate up migration for version %d", ErrInvalidMigration, version)
			}
			m.UpSQL = string(content)
		} else {
			if m.DownSQL != "" {
				return nil, fmt.Errorf("%w: duplicate down migration for version %d", ErrInvalidMigration, version)
			}
			m.DownSQL = string(content)
		}
	}

	if len(byVersion) == 0 {
		return nil, ErrNoMigrationsFound
	}

	migrations := make([]*Migration, 0, len(byVersion))
	for _, m := range byVersion {
		if m.UpSQL == "" {
			return nil, fmt.Errorf("%w: missing .up.sql for version %d (%s)", ErrInvalidMigration, m.Version, m.Name)
		}
		migrations = append(migrations, m)
	}

	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].Version < migrations[j].Version
	})

	return migrations, nil
}
