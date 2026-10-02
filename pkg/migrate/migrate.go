package migrate

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
	"time"
)

// Migration представляет одну миграцию
type Migration struct {
	Version string
	Name    string
	Up      string
	Down    string
}

// Migrator выполняет SQL-миграции
type Migrator struct {
	db         *sql.DB
	migrations []Migration
}

// NewMigrator создаёт новый Migrator
func NewMigrator(db *sql.DB, migrations []Migration) *Migrator {
	return &Migrator{
		db:         db,
		migrations: migrations,
	}
}

// Migrate выполняет все миграции
func (m *Migrator) Migrate(ctx context.Context) error {
	if err := m.createMigrationsTable(ctx); err != nil {
		return fmt.Errorf("failed to create migrations table: %w", err)
	}

	applied, err := m.getAppliedVersions(ctx)
	if err != nil {
		return fmt.Errorf("failed to get applied versions: %w", err)
	}

	for _, migration := range m.migrations {
		if applied[migration.Version] {
			continue
		}

		if err := m.applyMigration(ctx, migration); err != nil {
			return fmt.Errorf("failed to apply migration %s: %w", migration.Version, err)
		}
	}

	return nil
}

// Rollback откатывает последнюю миграцию
func (m *Migrator) Rollback(ctx context.Context) error {
	var version string
	err := m.db.QueryRowContext(ctx, `
		SELECT version FROM migrations 
		ORDER BY version DESC LIMIT 1
	`).Scan(&version)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to get last migration: %w", err)
	}

	for _, migration := range m.migrations {
		if migration.Version == version {
			tx, err := m.db.BeginTx(ctx, nil)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, migration.Down); err != nil {
				tx.Rollback()
				return fmt.Errorf("failed to rollback migration %s: %w", version, err)
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM migrations WHERE version = ?`, version); err != nil {
				tx.Rollback()
				return err
			}
			return tx.Commit()
		}
	}

	return nil
}

func (m *Migrator) createMigrationsTable(ctx context.Context) error {
	_, err := m.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS migrations (
			version TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			applied_at INTEGER NOT NULL
		)
	`)
	return err
}

func (m *Migrator) getAppliedVersions(ctx context.Context) (map[string]bool, error) {
	rows, err := m.db.QueryContext(ctx, `SELECT version FROM migrations`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	applied := make(map[string]bool)
	for rows.Next() {
		var version string
		if err := rows.Scan(&version); err != nil {
			return nil, err
		}
		applied[version] = true
	}
	return applied, rows.Err()
}

func (m *Migrator) applyMigration(ctx context.Context, migration Migration) error {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, migration.Up); err != nil {
		tx.Rollback()
		return err
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO migrations (version, name, applied_at) VALUES (?, ?, ?)`,
		migration.Version, migration.Name, time.Now().Unix(),
	); err != nil {
		tx.Rollback()
		return err
	}

	return tx.Commit()
}

// LoadMigrationsFromFS загружает миграции из файловой системы
func LoadMigrationsFromFS(fsys fs.FS, dir string) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, err
	}

	var migrations []Migration
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".up.sql") {
			continue
		}

		baseName := strings.TrimSuffix(entry.Name(), ".up.sql")
		parts := strings.SplitN(baseName, "_", 2)
		if len(parts) != 2 {
			continue
		}

		version := parts[0]
		name := parts[1]

		upContent, err := fs.ReadFile(fsys, path.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}

		downContent, err := fs.ReadFile(fsys, path.Join(dir, strings.TrimSuffix(entry.Name(), ".up.sql")+".down.sql"))
		if err != nil {
			return nil, err
		}

		migrations = append(migrations, Migration{
			Version: version,
			Name:    name,
			Up:      string(upContent),
			Down:    string(downContent),
		})
	}

	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].Version < migrations[j].Version
	})

	return migrations, nil
}
