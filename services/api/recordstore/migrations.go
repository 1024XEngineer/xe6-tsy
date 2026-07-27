package recordstore

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const migrationLockID int64 = 592517231924490297

// migrationFiles is the ordered, up-only schema history for the record-store adapter.
//
//go:embed migrations/*.up.sql
var migrationFiles embed.FS

type migration struct {
	Version int64
	Name    string
	SQL     string
}

// MigrationStatus is the durable version state after a migration has committed.
type MigrationStatus struct {
	Version   int64
	Name      string
	AppliedAt time.Time
}

// Migrate applies every embedded migration exactly once under a PostgreSQL advisory lock.
// The runner supports only forward migrations; failures roll back the migration and its version row.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return fmt.Errorf("migrate record store: nil PostgreSQL pool")
	}
	migrations, err := embeddedMigrations()
	if err != nil {
		return err
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin record-store migration transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", migrationLockID); err != nil {
		return fmt.Errorf("lock record-store migrations: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS recordstore_schema_migrations (
			version BIGINT PRIMARY KEY,
			name TEXT NOT NULL,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`); err != nil {
		return fmt.Errorf("create record-store migration state: %w", err)
	}

	applied, err := appliedMigrationNames(ctx, tx)
	if err != nil {
		return err
	}
	for _, migration := range migrations {
		if name, exists := applied[migration.Version]; exists {
			if name != migration.Name {
				return fmt.Errorf("record-store migration version %d name is %q, want %q", migration.Version, name, migration.Name)
			}
			continue
		}
		if _, err := tx.Exec(ctx, migration.SQL, pgx.QueryExecModeSimpleProtocol); err != nil {
			return fmt.Errorf("apply record-store migration %06d_%s: %w", migration.Version, migration.Name, err)
		}
		if _, err := tx.Exec(ctx,
			"INSERT INTO recordstore_schema_migrations (version, name) VALUES ($1, $2)",
			migration.Version,
			migration.Name,
		); err != nil {
			return fmt.Errorf("record record-store migration %06d_%s: %w", migration.Version, migration.Name, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit record-store migrations: %w", err)
	}
	return nil
}

// AppliedMigrations returns committed record-store migration versions in execution order.
func AppliedMigrations(ctx context.Context, pool *pgxpool.Pool) ([]MigrationStatus, error) {
	if pool == nil {
		return nil, fmt.Errorf("read record-store migration state: nil PostgreSQL pool")
	}
	rows, err := pool.Query(ctx, `
		SELECT version, name, applied_at
		FROM recordstore_schema_migrations
		ORDER BY version ASC`)
	if err != nil {
		return nil, fmt.Errorf("read record-store migration state: %w", err)
	}
	defer rows.Close()

	var statuses []MigrationStatus
	for rows.Next() {
		var status MigrationStatus
		if err := rows.Scan(&status.Version, &status.Name, &status.AppliedAt); err != nil {
			return nil, fmt.Errorf("scan record-store migration state: %w", err)
		}
		statuses = append(statuses, status)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate record-store migration state: %w", err)
	}
	return statuses, nil
}

func appliedMigrationNames(ctx context.Context, tx pgx.Tx) (map[int64]string, error) {
	rows, err := tx.Query(ctx, "SELECT version, name FROM recordstore_schema_migrations")
	if err != nil {
		return nil, fmt.Errorf("read record-store migration state: %w", err)
	}
	defer rows.Close()

	versions := make(map[int64]string)
	for rows.Next() {
		var version int64
		var name string
		if err := rows.Scan(&version, &name); err != nil {
			return nil, fmt.Errorf("scan record-store migration state: %w", err)
		}
		versions[version] = name
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate record-store migration state: %w", err)
	}
	return versions, nil
}

func embeddedMigrations() ([]migration, error) {
	files, err := fs.Glob(migrationFiles, "migrations/*.up.sql")
	if err != nil {
		return nil, fmt.Errorf("find record-store migrations: %w", err)
	}
	slices.Sort(files)
	migrations := make([]migration, 0, len(files))
	versions := make(map[int64]struct{}, len(files))
	for _, file := range files {
		migration, err := readMigration(file)
		if err != nil {
			return nil, err
		}
		if _, exists := versions[migration.Version]; exists {
			return nil, fmt.Errorf("duplicate record-store migration version %d", migration.Version)
		}
		versions[migration.Version] = struct{}{}
		migrations = append(migrations, migration)
	}
	return migrations, nil
}

func readMigration(file string) (migration, error) {
	base := strings.TrimSuffix(path.Base(file), ".up.sql")
	versionText, name, found := strings.Cut(base, "_")
	if !found || versionText == "" || name == "" {
		return migration{}, fmt.Errorf("invalid record-store migration filename %q", file)
	}
	version, err := strconv.ParseInt(versionText, 10, 64)
	if err != nil || version < 1 {
		return migration{}, fmt.Errorf("invalid record-store migration version in %q", file)
	}
	sql, err := migrationFiles.ReadFile(file)
	if err != nil {
		return migration{}, fmt.Errorf("read record-store migration %q: %w", file, err)
	}
	return migration{Version: version, Name: name, SQL: string(sql)}, nil
}
