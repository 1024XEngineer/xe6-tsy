//go:build integration

package languages

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func testDatabaseURL(t *testing.T) string {
	t.Helper()
	if url := os.Getenv("DATABASE_URL"); url != "" {
		return url
	}
	// Local default matching the developer's Postgres (password 123456).
	return "postgres://postgres:123456@localhost:5432/lingow?sslmode=disable"
}

func openTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, testDatabaseURL(t))
	if err != nil {
		t.Fatalf("connect postgres: %v", err)
	}
	t.Cleanup(pool.Close)

	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("ping postgres: %v", err)
	}
	if err := ApplyMigrations(ctx, pool); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	return pool
}

func cleanupSession(t *testing.T, pool *pgxpool.Pool, sessionID string) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`DELETE FROM voice_session_language_configs WHERE session_id = $1`, sessionID)
	if err != nil {
		t.Fatalf("cleanup session %s: %v", sessionID, err)
	}
}

func TestPostgresMigrationsAndSupportedLanguages(t *testing.T) {
	pool := openTestPool(t)
	store := NewPostgresStore(pool, nil)

	langs, err := store.ListSupportedLanguages(context.Background(), true)
	if err != nil {
		t.Fatalf("ListSupportedLanguages: %v", err)
	}
	if len(langs) < 2 {
		t.Fatalf("expected seeded zh-CN/en-US, got %#v", langs)
	}

	codes := map[string]bool{}
	for _, lang := range langs {
		codes[lang.LanguageCode] = true
	}
	if !codes["zh-CN"] || !codes["en-US"] {
		t.Fatalf("missing P0 languages in %#v", langs)
	}
}

func TestPostgresCreateActiveConfigLifecycle(t *testing.T) {
	pool := openTestPool(t)
	store := NewPostgresStore(pool, fixedClock{at: time.Date(2026, 7, 27, 2, 0, 0, 0, time.UTC)})
	ctx := context.Background()
	sessionID := "vs_lang_it_001"
	cleanupSession(t, pool, sessionID)

	pairs := []LanguagePair{
		{Source: "zh-CN", Target: "en-US"},
		{Source: "en-US", Target: "zh-CN"},
	}

	first, err := store.CreateActiveConfig(ctx, CreateConfigInput{
		SessionID:      sessionID,
		LanguagePairs:  pairs,
		CreatedBy:      "user_test",
		IdempotencyKey: "ik_first",
	})
	if err != nil {
		t.Fatalf("create first: %v", err)
	}
	if first.Version != 1 || first.Status != StatusActive || first.EffectiveUntil != nil {
		t.Fatalf("unexpected first config: %#v", first)
	}

	active, err := store.GetActiveConfig(ctx, sessionID)
	if err != nil {
		t.Fatalf("GetActiveConfig: %v", err)
	}
	if active.ID != first.ID || active.Version != 1 {
		t.Fatalf("active mismatch: %#v", active)
	}

	expected := 1
	second, err := store.CreateActiveConfig(ctx, CreateConfigInput{
		SessionID:       sessionID,
		LanguagePairs:   pairs,
		CreatedBy:       "user_test",
		IdempotencyKey:  "ik_second",
		ExpectedVersion: &expected,
	})
	if err != nil {
		t.Fatalf("create second: %v", err)
	}
	if second.Version != 2 || second.Status != StatusActive {
		t.Fatalf("unexpected second config: %#v", second)
	}

	active, err = store.GetActiveConfig(ctx, sessionID)
	if err != nil {
		t.Fatalf("GetActiveConfig after switch: %v", err)
	}
	if active.Version != 2 {
		t.Fatalf("want active version 2, got %d", active.Version)
	}

	items, next, err := store.ListConfigs(ctx, ListConfigsQuery{SessionID: sessionID, Limit: 10})
	if err != nil {
		t.Fatalf("ListConfigs: %v", err)
	}
	if next != "" {
		t.Fatalf("unexpected next cursor %q", next)
	}
	if len(items) != 2 {
		t.Fatalf("want 2 history rows, got %d", len(items))
	}
	if items[0].Version != 2 || items[1].Version != 1 {
		t.Fatalf("history not version DESC: %#v", items)
	}
	if items[1].Status != StatusSuperseded || items[1].EffectiveUntil == nil {
		t.Fatalf("superseded row incomplete: %#v", items[1])
	}
}

func TestPostgresExpectedVersionConflict(t *testing.T) {
	pool := openTestPool(t)
	store := NewPostgresStore(pool, nil)
	ctx := context.Background()
	sessionID := "vs_lang_it_002"
	cleanupSession(t, pool, sessionID)

	pairs := []LanguagePair{
		{Source: "zh-CN", Target: "en-US"},
		{Source: "en-US", Target: "zh-CN"},
	}
	if _, err := store.CreateActiveConfig(ctx, CreateConfigInput{
		SessionID: sessionID, LanguagePairs: pairs, CreatedBy: "user_test",
	}); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	wrong := 99
	_, err := store.CreateActiveConfig(ctx, CreateConfigInput{
		SessionID:       sessionID,
		LanguagePairs:   pairs,
		CreatedBy:       "user_test",
		ExpectedVersion: &wrong,
	})
	if !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("error = %v, want ErrVersionConflict", err)
	}
}

func TestPostgresIdempotencyKeyLookupAndConflict(t *testing.T) {
	pool := openTestPool(t)
	store := NewPostgresStore(pool, nil)
	ctx := context.Background()
	sessionID := "vs_lang_it_003"
	cleanupSession(t, pool, sessionID)

	pairs := []LanguagePair{
		{Source: "zh-CN", Target: "en-US"},
		{Source: "en-US", Target: "zh-CN"},
	}
	created, err := store.CreateActiveConfig(ctx, CreateConfigInput{
		SessionID:      sessionID,
		LanguagePairs:  pairs,
		CreatedBy:      "user_test",
		IdempotencyKey: "ik_replay",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := store.GetConfigByIdempotencyKey(ctx, "ik_replay")
	if err != nil {
		t.Fatalf("GetConfigByIdempotencyKey: %v", err)
	}
	if got.ID != created.ID {
		t.Fatalf("idempotent lookup mismatch: %#v vs %#v", got, created)
	}

	_, err = store.CreateActiveConfig(ctx, CreateConfigInput{
		SessionID:      "vs_lang_it_003b",
		LanguagePairs:  pairs,
		CreatedBy:      "user_test",
		IdempotencyKey: "ik_replay",
	})
	if !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("error = %v, want ErrIdempotencyConflict", err)
	}
}

func TestPostgresGetActiveConfigMissing(t *testing.T) {
	pool := openTestPool(t)
	store := NewPostgresStore(pool, nil)
	_, err := store.GetActiveConfig(context.Background(), "vs_missing_session")
	if !errors.Is(err, ErrNoActiveConfig) {
		t.Fatalf("error = %v, want ErrNoActiveConfig", err)
	}
}

type fixedClock struct{ at time.Time }

func (c fixedClock) Now() time.Time { return c.at }
