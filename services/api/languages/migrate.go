package languages

import (
	"context"
	"embed"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// languagesMigrationLockKey serializes ApplyMigrations across API processes.
// Stable application-defined advisory-lock key (issue #88 / languages module).
const languagesMigrationLockKey int64 = 0x4c414e4788 // "LANG" + 0x88

// ApplyMigrations runs embedded SQL migration files in lexical order.
// Each file is applied at most once, tracked in schema_migrations.
//
// Concurrent callers are serialized with a session-scoped PostgreSQL advisory
// lock on a single pooled connection so two API instances cannot both observe a
// migration as missing, apply it, and race on the schema_migrations insert.
func ApplyMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire migration connection: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, languagesMigrationLockKey); err != nil {
		return fmt.Errorf("acquire migration advisory lock: %w", err)
	}
	defer func() {
		// Unlock on the same connection that took the lock.
		_, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, languagesMigrationLockKey)
	}()

	if _, err := conn.Exec(ctx, `
CREATE TABLE IF NOT EXISTS schema_migrations (
    filename   TEXT PRIMARY KEY,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
)`); err != nil {
		return fmt.Errorf("ensure schema_migrations: %w", err)
	}

	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)

	for _, name := range names {
		if err := applyOneMigration(ctx, conn, name); err != nil {
			return err
		}
	}
	return nil
}

func applyOneMigration(ctx context.Context, conn *pgxpool.Conn, name string) error {
	var exists bool
	if err := conn.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE filename = $1)`, name,
	).Scan(&exists); err != nil {
		return fmt.Errorf("check migration %s: %w", name, err)
	}
	if exists {
		return nil
	}

	sqlBytes, err := migrationFS.ReadFile("migrations/" + name)
	if err != nil {
		return fmt.Errorf("read migration %s: %w", name, err)
	}

	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin migration %s: %w", name, err)
	}
	defer tx.Rollback(ctx)

	// Re-check under the transaction after the advisory lock so a concurrent
	// winner is observed as already applied instead of failing the loser.
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE filename = $1)`, name,
	).Scan(&exists); err != nil {
		return fmt.Errorf("re-check migration %s: %w", name, err)
	}
	if exists {
		return nil
	}

	if _, err := tx.Exec(ctx, string(sqlBytes)); err != nil {
		return fmt.Errorf("apply migration %s: %w", name, err)
	}

	// ON CONFLICT treats a lost claim race as success; migration SQL is idempotent.
	if _, err := tx.Exec(ctx,
		`INSERT INTO schema_migrations (filename) VALUES ($1) ON CONFLICT (filename) DO NOTHING`, name,
	); err != nil {
		return fmt.Errorf("record migration %s: %w", name, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit migration %s: %w", name, err)
	}
	return nil
}
