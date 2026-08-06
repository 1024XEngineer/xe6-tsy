package languages

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestApplyMigrationsSmoke runs without the integration build tag when
// RUN_LANGUAGE_DB_SMOKE=1 is set. Default unit tests stay offline.
func TestApplyMigrationsSmoke(t *testing.T) {
	if os.Getenv("RUN_LANGUAGE_DB_SMOKE") != "1" {
		t.Skip("set RUN_LANGUAGE_DB_SMOKE=1 to apply migrations against local Postgres")
	}

	url := os.Getenv("DATABASE_URL")
	if url == "" {
		url = "postgres://postgres:123456@localhost:5432/lingow?sslmode=disable"
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	if err := ApplyMigrations(ctx, pool); err != nil {
		t.Fatalf("ApplyMigrations: %v", err)
	}
	// Second apply must be a no-op.
	if err := ApplyMigrations(ctx, pool); err != nil {
		t.Fatalf("ApplyMigrations second pass: %v", err)
	}

	store := NewPostgresStore(pool, nil)
	langs, err := store.ListSupportedLanguages(ctx, true)
	if err != nil {
		t.Fatalf("ListSupportedLanguages: %v", err)
	}
	if len(langs) < 2 {
		t.Fatalf("expected seed languages, got %d", len(langs))
	}
}
