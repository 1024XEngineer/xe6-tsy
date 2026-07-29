//go:build integration

package recordstore

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestMigrateLegacySessionStartSchema(t *testing.T) {
	pool := testDatabase(t)
	seedLegacySessionV2(t, pool)

	if err := Migrate(t.Context(), pool); err != nil {
		t.Fatalf("Migrate() legacy schema error = %v", err)
	}
	if err := Migrate(t.Context(), pool); err != nil {
		t.Fatalf("second Migrate() legacy schema error = %v", err)
	}

	assertSessionStartCompatibilitySchema(t, pool)
	statuses, err := AppliedMigrations(t.Context(), pool)
	if err != nil {
		t.Fatalf("AppliedMigrations() error = %v", err)
	}
	if len(statuses) != 10 ||
		statuses[9].Version != 10 ||
		statuses[9].Name != "session_start_operation_compatibility" {
		t.Fatalf("AppliedMigrations() = %#v", statuses)
	}

	var legacyCount int
	var legacyComment *string
	if err := pool.QueryRow(t.Context(), `
		SELECT COUNT(*) FROM voice_session_start_requests
	`).Scan(&legacyCount); err != nil {
		t.Fatalf("count legacy start request rows: %v", err)
	}
	if err := pool.QueryRow(t.Context(), `
		SELECT obj_description('voice_session_start_requests'::regclass, 'pg_class')
	`).Scan(&legacyComment); err != nil {
		t.Fatalf("inspect legacy start request comment: %v", err)
	}
	if legacyCount != 0 {
		t.Fatalf("legacy start request rows = %d, want 0", legacyCount)
	}
	if legacyComment == nil || !strings.Contains(*legacyComment, "DEPRECATED") {
		t.Fatalf("legacy start request comment = %#v", legacyComment)
	}

	createdAt := time.Date(2026, time.July, 29, 14, 0, 0, 0, time.UTC)
	endedAt := createdAt.Add(time.Minute)
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO voice_sessions (
			id, account_id, status, audio_config, capabilities, ended_at, created_at
		) VALUES (
			'legacy_direct_end', 'legacy_account', 'ended',
			'{}'::jsonb, '{}'::jsonb, $1, $2
		)`, endedAt, createdAt); err != nil {
		t.Fatalf("insert directly-ended session after legacy upgrade: %v", err)
	}
}

func TestMigrateRejectsMalformedSessionStartOperations(t *testing.T) {
	pool := testDatabase(t)
	seedMigrationLedger(t, pool, 9)
	if _, err := pool.Exec(t.Context(), malformedSessionSchemaSQL); err != nil {
		t.Fatalf("seed malformed session schema: %v", err)
	}

	err := Migrate(t.Context(), pool)
	if err == nil || !strings.Contains(err.Error(), "missing one or more critical columns") {
		t.Fatalf("Migrate() malformed schema error = %v", err)
	}

	var versionCount int
	if err := pool.QueryRow(t.Context(), `
		SELECT COUNT(*) FROM recordstore_schema_migrations WHERE version = 10
	`).Scan(&versionCount); err != nil {
		t.Fatalf("count compatibility migration ledger row: %v", err)
	}
	if versionCount != 0 {
		t.Fatalf("compatibility migration ledger rows = %d, want 0", versionCount)
	}

	var legacyComment *string
	if err := pool.QueryRow(t.Context(), `
		SELECT obj_description('voice_session_start_requests'::regclass, 'pg_class')
	`).Scan(&legacyComment); err != nil {
		t.Fatalf("read rolled-back legacy comment: %v", err)
	}
	if legacyComment != nil {
		t.Fatalf("legacy comment survived failed migration: %q", *legacyComment)
	}

	createdAt := time.Date(2026, time.July, 29, 15, 0, 0, 0, time.UTC)
	_, err = pool.Exec(t.Context(), `
		INSERT INTO voice_sessions (
			id, account_id, status, audio_config, capabilities, ended_at, created_at
		) VALUES (
			'malformed_direct_end', 'malformed_account', 'ended',
			'{}'::jsonb, '{}'::jsonb, $1, $2
		)`, createdAt.Add(time.Minute), createdAt)
	assertPostgresCode(t, err, "23514")
}

func assertSessionStartCompatibilitySchema(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	var operationTable, legacyTable *string
	if err := pool.QueryRow(t.Context(), `
		SELECT
			to_regclass('voice_session_start_operations')::text,
			to_regclass('voice_session_start_requests')::text
	`).Scan(&operationTable, &legacyTable); err != nil {
		t.Fatalf("inspect session start tables: %v", err)
	}
	if operationTable == nil {
		t.Fatal("voice_session_start_operations does not exist")
	}

	var criticalColumns int
	if err := pool.QueryRow(t.Context(), `
		SELECT COUNT(*)
		FROM information_schema.columns
		WHERE table_schema = current_schema()
		  AND table_name = 'voice_session_start_operations'
		  AND column_name IN (
			  'operation_id', 'session_id', 'account_id',
			  'idempotency_key', 'request_hash', 'status'
		  )
	`).Scan(&criticalColumns); err != nil {
		t.Fatalf("count StartOperation critical columns: %v", err)
	}
	if criticalColumns != 6 {
		t.Fatalf("StartOperation critical columns = %d, want 6", criticalColumns)
	}

	for _, object := range []struct {
		name string
		kind string
	}{
		{name: "voice_session_start_operations_pkey", kind: "constraint"},
		{name: "voice_session_start_operations_key_unique", kind: "constraint"},
		{name: "voice_session_start_operations_one_unfinished_per_session", kind: "index"},
		{name: "voice_session_start_operations_account_session_key_idx", kind: "index"},
	} {
		t.Run(object.name, func(t *testing.T) {
			var exists bool
			query := `
				SELECT EXISTS (
					SELECT 1 FROM pg_constraint
					WHERE conrelid = 'voice_session_start_operations'::regclass
					  AND conname = $1
				)`
			if object.kind == "index" {
				query = `
					SELECT EXISTS (
						SELECT 1
						FROM pg_class AS index_record
						JOIN pg_index AS metadata ON metadata.indexrelid = index_record.oid
						WHERE metadata.indrelid = 'voice_session_start_operations'::regclass
						  AND index_record.relname = $1
					)`
			}
			if err := pool.QueryRow(t.Context(), query, object.name).Scan(&exists); err != nil {
				t.Fatalf("inspect %s: %v", object.name, err)
			}
			if !exists {
				t.Fatalf("%s does not exist", object.name)
			}
		})
	}
}

func seedMigrationLedger(t *testing.T, pool *pgxpool.Pool, through int64) {
	t.Helper()
	migrations, err := embeddedMigrations()
	if err != nil {
		t.Fatalf("embeddedMigrations() error = %v", err)
	}
	if _, err := pool.Exec(t.Context(), `
		CREATE TABLE recordstore_schema_migrations (
			version BIGINT PRIMARY KEY,
			name TEXT NOT NULL,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`); err != nil {
		t.Fatalf("create migration ledger fixture: %v", err)
	}
	for _, migration := range migrations {
		if migration.Version > through {
			break
		}
		if _, err := pool.Exec(t.Context(), `
			INSERT INTO recordstore_schema_migrations (version, name)
			VALUES ($1, $2)`, migration.Version, migration.Name); err != nil {
			t.Fatalf("insert migration ledger version %d: %v", migration.Version, err)
		}
	}
}

func seedLegacySessionV2(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	seedMigrationLedger(t, pool, 2)
	if _, err := pool.Exec(
		t.Context(),
		legacySessionV2FixtureSQL,
		pgx.QueryExecModeSimpleProtocol,
	); err != nil {
		t.Fatalf("seed legacy v2 fixture: %v", err)
	}
}

func assertConstraintViolation(t *testing.T, err error, constraint string) {
	t.Helper()
	if err == nil {
		t.Fatalf("PostgreSQL error = nil, want constraint %s", constraint)
	}
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) {
		t.Fatalf("error = %v, want PostgreSQL constraint %s", err, constraint)
	}
	if postgresError.ConstraintName != constraint {
		t.Fatalf(
			"PostgreSQL constraint = %q, want %q",
			postgresError.ConstraintName,
			constraint,
		)
	}
}

const legacySessionV2FixtureSQL = `
CREATE TABLE lingow_accounts (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,
    phone_hash TEXT,
    merged_into TEXT REFERENCES lingow_accounts (id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT accounts_identity_valid CHECK (
        (kind = 'anonymous' AND phone_hash IS NULL)
        OR (kind = 'registered' AND phone_hash IS NOT NULL AND merged_into IS NULL)
    )
);

CREATE TABLE lingow_phone_challenges (
    id TEXT PRIMARY KEY,
    phone_hash TEXT NOT NULL,
    code_hash TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    used_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE voice_sessions (
    id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL REFERENCES lingow_accounts (id) ON DELETE RESTRICT,
    status TEXT NOT NULL,
    audio_config JSONB NOT NULL,
    capabilities JSONB NOT NULL,
    failure_error_code TEXT,
    started_at TIMESTAMPTZ,
    ended_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT voice_sessions_timestamps_valid CHECK (
        (status = 'created' AND started_at IS NULL AND ended_at IS NULL AND failure_error_code IS NULL)
        OR (status = 'active' AND started_at IS NOT NULL AND ended_at IS NULL AND failure_error_code IS NULL)
        OR (
            status = 'ended'
            AND started_at IS NOT NULL
            AND ended_at IS NOT NULL
            AND ended_at >= started_at
            AND failure_error_code IS NULL
        )
        OR (status = 'failed' AND started_at IS NOT NULL AND ended_at IS NULL AND failure_error_code IS NOT NULL)
    ),
    CONSTRAINT voice_sessions_id_account_id_key UNIQUE (id, account_id)
);

CREATE TABLE voice_session_create_requests (
    account_id TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    request_hash TEXT NOT NULL,
    session_id TEXT NOT NULL,
    PRIMARY KEY (account_id, idempotency_key),
    FOREIGN KEY (session_id, account_id)
        REFERENCES voice_sessions (id, account_id) ON DELETE RESTRICT
);

CREATE TABLE voice_session_start_requests (
    account_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    request_hash TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (account_id, idempotency_key),
    FOREIGN KEY (session_id, account_id)
        REFERENCES voice_sessions (id, account_id) ON DELETE RESTRICT
);

CREATE TABLE voice_session_end_intents (
    session_id TEXT NOT NULL,
    account_id TEXT NOT NULL,
    reason TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    request_hash TEXT NOT NULL,
    requested_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    PRIMARY KEY (session_id, account_id),
    FOREIGN KEY (session_id, account_id)
        REFERENCES voice_sessions (id, account_id) ON DELETE RESTRICT
);

CREATE TABLE lingow_usage_records (
    id TEXT PRIMARY KEY,
    cost_amount NUMERIC(20, 8),
    currency TEXT
);

CREATE FUNCTION recordstore_reject_usage_record_updates()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'usage records are immutable';
END;
$$;

CREATE TRIGGER lingow_usage_records_reject_updates
    BEFORE UPDATE ON lingow_usage_records
    FOR EACH ROW
    EXECUTE FUNCTION recordstore_reject_usage_record_updates();

CREATE TABLE outbound_messages (
    id TEXT PRIMARY KEY
);

CREATE TABLE delivery_attempts (
    id TEXT PRIMARY KEY
);

INSERT INTO lingow_accounts (id, kind)
VALUES ('legacy_account', 'anonymous');
`

const malformedSessionSchemaSQL = `
CREATE TABLE lingow_accounts (
    id TEXT PRIMARY KEY
);

CREATE TABLE voice_sessions (
    id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL,
    status TEXT NOT NULL,
    audio_config JSONB NOT NULL,
    capabilities JSONB NOT NULL,
    failure_error_code TEXT,
    started_at TIMESTAMPTZ,
    ended_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT voice_sessions_timestamps_valid CHECK (
        (status = 'created' AND started_at IS NULL AND ended_at IS NULL AND failure_error_code IS NULL)
        OR (
            status = 'ended'
            AND started_at IS NOT NULL
            AND ended_at IS NOT NULL
            AND ended_at >= started_at
            AND failure_error_code IS NULL
        )
    )
);

CREATE TABLE voice_session_start_requests (
    id TEXT PRIMARY KEY
);

CREATE TABLE voice_session_start_operations (
    status TEXT NOT NULL
);

INSERT INTO lingow_accounts (id) VALUES ('malformed_account');
`
