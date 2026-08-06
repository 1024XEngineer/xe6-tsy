//go:build integration

package languages

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/oklog/ulid/v2"
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

func TestQwenRealtimeCatalogMigrationRepairsMissingLanguages(t *testing.T) {
	pool := openTestPool(t)
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin transaction: %v", err)
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `
CREATE TEMP TABLE supported_languages (
    language_code      VARCHAR(10) PRIMARY KEY,
    display_name       VARCHAR(64) NOT NULL,
    display_name_en    VARCHAR(64) NOT NULL DEFAULT '',
    supports_as_source BOOLEAN NOT NULL DEFAULT TRUE,
    supports_as_target BOOLEAN NOT NULL DEFAULT TRUE,
    sort_order         INT NOT NULL DEFAULT 0,
    is_active          BOOLEAN NOT NULL DEFAULT TRUE
) ON COMMIT DROP;
INSERT INTO supported_languages (language_code, display_name, display_name_en)
VALUES
    ('th-TH', 'Thai', 'Thai'),
    ('id-ID', 'Indonesian', 'Indonesian'),
    ('vi-VN', 'Vietnamese', 'Vietnamese');`)
	if err != nil {
		t.Fatalf("seed incomplete catalog: %v", err)
	}

	migration, err := migrationFS.ReadFile("migrations/004_qwen_realtime_language_intersection.sql")
	if err != nil {
		t.Fatalf("read Qwen catalog migration: %v", err)
	}
	if _, err := tx.Exec(ctx, string(migration)); err != nil {
		t.Fatalf("apply Qwen catalog migration: %v", err)
	}

	rows, err := tx.Query(ctx, `SELECT language_code, is_active FROM supported_languages`)
	if err != nil {
		t.Fatalf("query migrated catalog: %v", err)
	}
	defer rows.Close()

	active := make(map[string]bool)
	for rows.Next() {
		var code string
		var isActive bool
		if err := rows.Scan(&code, &isActive); err != nil {
			t.Fatalf("scan migrated language: %v", err)
		}
		active[code] = isActive
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate migrated catalog: %v", err)
	}

	for _, code := range []string{"ja-JP", "ko-KR", "fr-FR", "de-DE", "ru-RU", "pt-BR", "it-IT", "es-ES"} {
		if !active[code] {
			t.Errorf("expected %s to be present and active", code)
		}
	}
	for _, code := range []string{"th-TH", "id-ID", "vi-VN"} {
		if active[code] {
			t.Errorf("expected %s to be inactive", code)
		}
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

func TestPostgresConcurrentFirstCreateDifferentIdempotencyKeys(t *testing.T) {
	pool := openTestPool(t)
	ctx := context.Background()
	sessionID := "vs_lang_it_concurrent_001"
	cleanupSession(t, pool, sessionID)

	pairsJSON := `[{"source":"zh-CN","target":"en-US"},{"source":"en-US","target":"zh-CN"}]`

	tx1, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx1: %v", err)
	}
	defer tx1.Rollback(ctx)

	tx2, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx2: %v", err)
	}
	defer tx2.Rollback(ctx)

	// Both observe "no active row" before either inserts — FOR UPDATE cannot
	// serialize this case, which is the race the constraint mapping must handle.
	for i, tx := range []interface {
		QueryRow(context.Context, string, ...any) pgx.Row
	}{tx1, tx2} {
		var id string
		scanErr := tx.QueryRow(ctx, `
SELECT id FROM voice_session_language_configs
WHERE session_id = $1 AND status = 'active'
FOR UPDATE`, sessionID).Scan(&id)
		if !errors.Is(scanErr, pgx.ErrNoRows) {
			t.Fatalf("tx%d lock scan = %v, want ErrNoRows", i+1, scanErr)
		}
	}

	now := time.Now().UTC()
	if _, err := tx1.Exec(ctx, `
INSERT INTO voice_session_language_configs (
    id, session_id, version, language_pairs, status,
    effective_from, effective_until, created_by, idempotency_key, created_at, updated_at
) VALUES ($1,$2,1,$3::jsonb,'active',$4,NULL,'user_a','ik_concurrent_a',$4,$4)`,
		ulid.Make().String(), sessionID, pairsJSON, now,
	); err != nil {
		t.Fatalf("tx1 insert: %v", err)
	}
	if err := tx1.Commit(ctx); err != nil {
		t.Fatalf("tx1 commit: %v", err)
	}

	_, err = tx2.Exec(ctx, `
INSERT INTO voice_session_language_configs (
    id, session_id, version, language_pairs, status,
    effective_from, effective_until, created_by, idempotency_key, created_at, updated_at
) VALUES ($1,$2,1,$3::jsonb,'active',$4,NULL,'user_b','ik_concurrent_b',$4,$4)`,
		ulid.Make().String(), sessionID, pairsJSON, now,
	)
	mapped := mapInsertUniqueViolation(err)
	if !errors.Is(mapped, ErrVersionConflict) {
		t.Fatalf("lost first-create race mapped to %v (raw=%v), want ErrVersionConflict", mapped, err)
	}
	if errors.Is(mapped, ErrIdempotencyConflict) {
		t.Fatalf("must not classify active/version race as idempotency conflict")
	}
}

type fixedClock struct{ at time.Time }

func (c fixedClock) Now() time.Time { return c.at }
