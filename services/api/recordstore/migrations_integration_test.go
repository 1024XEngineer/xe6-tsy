//go:build integration

package recordstore

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestMigrateRecordsSchema(t *testing.T) {
	pool := testDatabase(t)
	if err := Migrate(t.Context(), pool); err != nil {
		t.Fatalf("first Migrate() error = %v", err)
	}
	if err := Migrate(t.Context(), pool); err != nil {
		t.Fatalf("second Migrate() error = %v", err)
	}
	assertEndIntentRecoverySchema(t, pool)

	statuses, err := AppliedMigrations(t.Context(), pool)
	if err != nil {
		t.Fatalf("AppliedMigrations() error = %v", err)
	}
	want := []struct {
		version int64
		name    string
	}{
		{1, "voice_records"},
		{2, "member5_control_plane"},
		{3, "phone_challenge_hardening"},
		{4, "account_lineage"},
		{5, "phone_digest_v2"},
		{6, "phone_digest_cleanup"},
		{7, "usage_pricing_consistency"},
		{8, "delivery_retry_idempotency"},
		{9, "final_turn_outbox"},
		{10, "session_start_operation_compatibility"},
		{11, "email_bind_challenges"},
		{12, "enable_wechat_channel"},
		{13, "session_failed_terminal_timestamp"},
		{14, "end_intent_recovery"},
		{15, "attribution_snapshot_updates"},
		{16, "attribution_tasks"},
		{17, "backfill_attribution_tasks"},
		{18, "final_turn_outbox_dead_letter"},
		{19, "auto_delivery_destination"},
		{20, "record_history_indexes"},
		{21, "automatic_turn_settlements"},
		{22, "realtime_fallback_playback_operations"},
		{23, "realtime_fallback_playback_claims"},
		{24, "realtime_fallback_playback_claim_tokens"},
		{25, "realtime_fallback_playback_reclaimable"},
		{26, "target_level_message_preferences"},
		{27, "assistant_llm_usage"},
	}
	if len(statuses) != len(want) {
		t.Fatalf("len(AppliedMigrations()) = %d, want %d", len(statuses), len(want))
	}
	for index, expected := range want {
		status := statuses[index]
		if status.Version != expected.version || status.Name != expected.name || status.AppliedAt.IsZero() {
			t.Fatalf("AppliedMigrations()[%d] = %#v, want applied %s version %d", index, status, expected.name, expected.version)
		}
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
		t.Fatalf("PostgreSQL constraint = %q, want %q", postgresError.ConstraintName, constraint)
	}
}

func assertEndIntentRecoverySchema(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	var columns int
	if err := pool.QueryRow(t.Context(), `
		SELECT COUNT(*)
		FROM information_schema.columns
		WHERE table_schema = current_schema()
		  AND table_name = 'voice_session_end_intents'
		  AND column_name IN (
			  'trace_id', 'retry_count', 'last_error', 'next_attempt_at',
			  'recovery_owner', 'recovery_lease_expires_at'
		  )`).Scan(&columns); err != nil {
		t.Fatalf("count EndIntent recovery columns: %v", err)
	}
	if columns != 6 {
		t.Fatalf("EndIntent recovery columns = %d, want 6", columns)
	}
	var indexExists bool
	if err := pool.QueryRow(t.Context(), `
		SELECT to_regclass('voice_session_end_intents_recovery_due_idx') IS NOT NULL
	`).Scan(&indexExists); err != nil {
		t.Fatalf("inspect EndIntent recovery index: %v", err)
	}
	if !indexExists {
		t.Fatal("voice_session_end_intents_recovery_due_idx does not exist")
	}
}

func TestRecordSchemaConstraints(t *testing.T) {
	pool := testDatabase(t)
	if err := Migrate(t.Context(), pool); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	testConcurrentSpeakerMappingConstraint(t, pool)
	testProviderSpeakerConstraint(t, pool)
	testTurnConstraints(t, pool)
	testSessionLifecycleConstraints(t, pool)
	testStartOperationConstraints(t, pool)
	testStartOperationStateConstraints(t, pool)
}

func testSessionLifecycleConstraints(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	createdAt := time.Date(2026, time.July, 29, 10, 0, 0, 0, time.UTC)
	endedAt := createdAt.Add(time.Minute)
	_, err := pool.Exec(t.Context(), `
		INSERT INTO lingow_accounts (id, kind, created_at)
		VALUES ('acct_session_constraints', 'anonymous', $1)`, createdAt)
	if err != nil {
		t.Fatalf("insert session constraint account: %v", err)
	}
	startedAt := createdAt.Add(time.Minute)
	activeEndedAt := startedAt.Add(time.Minute)
	failureCode := "runtime_failed"

	valid := []struct {
		name      string
		status    string
		startedAt *time.Time
		endedAt   *time.Time
		failure   *string
	}{
		{name: "created", status: "created"},
		{name: "active", status: "active", startedAt: &startedAt},
		{name: "created_to_ended", status: "ended", endedAt: &endedAt},
		{name: "active_to_ended", status: "ended", startedAt: &startedAt, endedAt: &activeEndedAt},
		{name: "active_to_failed", status: "failed", startedAt: &startedAt, endedAt: &activeEndedAt, failure: &failureCode},
	}
	for _, test := range valid {
		t.Run("valid_"+test.name, func(t *testing.T) {
			if err := insertLifecycleSession(
				t, pool, "session_valid_"+test.name, test.status,
				test.startedAt, test.endedAt, test.failure, createdAt,
			); err != nil {
				t.Fatalf("insert valid lifecycle session: %v", err)
			}
		})
	}

	beforeCreated := createdAt.Add(-time.Second)
	beforeStarted := startedAt.Add(-time.Second)
	invalid := []struct {
		name      string
		status    string
		startedAt *time.Time
		endedAt   *time.Time
		failure   *string
	}{
		{name: "active_without_start", status: "active"},
		{name: "ended_without_end", status: "ended", startedAt: &startedAt},
		{name: "ended_before_created", status: "ended", endedAt: &beforeCreated},
		{name: "ended_before_started", status: "ended", startedAt: &startedAt, endedAt: &beforeStarted},
		{name: "failed_without_error", status: "failed", startedAt: &startedAt},
		{name: "failed_without_end", status: "failed", startedAt: &startedAt, failure: &failureCode},
		{name: "failed_before_started", status: "failed", startedAt: &startedAt, endedAt: &beforeStarted, failure: &failureCode},
	}
	for _, test := range invalid {
		t.Run("invalid_"+test.name, func(t *testing.T) {
			err := insertLifecycleSession(
				t, pool, "session_invalid_"+test.name, test.status,
				test.startedAt, test.endedAt, test.failure, createdAt,
			)
			assertConstraintViolation(t, err, "voice_sessions_timestamps_valid")
		})
	}
}

func insertLifecycleSession(
	t *testing.T,
	pool *pgxpool.Pool,
	id string,
	status string,
	startedAt *time.Time,
	endedAt *time.Time,
	failureCode *string,
	createdAt time.Time,
) error {
	t.Helper()
	_, err := pool.Exec(t.Context(), `
		INSERT INTO voice_sessions (
			id, account_id, status, audio_config, capabilities,
			failure_error_code, started_at, ended_at, created_at
		) VALUES (
			$1, 'acct_session_constraints', $2, '{}'::jsonb, '{}'::jsonb,
			$3, $4, $5, $6
		)`, id, status, failureCode, startedAt, endedAt, createdAt)
	return err
}

func testStartOperationConstraints(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	createdAt := time.Date(2026, time.July, 28, 10, 0, 0, 0, time.UTC)
	_, err := pool.Exec(t.Context(), `
		INSERT INTO lingow_accounts (id, kind, created_at)
		VALUES ('acct_start_01', 'anonymous', $1)`, createdAt)
	if err != nil {
		t.Fatalf("insert start-operation account: %v", err)
	}
	_, err = pool.Exec(t.Context(), `
		INSERT INTO voice_sessions (
			id, account_id, status, audio_config, capabilities, created_at
		) VALUES (
			'vs_start_01', 'acct_start_01', 'created', '{}'::jsonb, '{}'::jsonb, $1
		)`, createdAt)
	if err != nil {
		t.Fatalf("insert start-operation session: %v", err)
	}

	insert := func(operationID, key, status string, claimID *string, startedAt *time.Time) error {
		_, err := pool.Exec(t.Context(), `
			INSERT INTO voice_session_start_operations (
				operation_id, session_id, account_id, idempotency_key, request_hash,
				status, compensation_claim_id, started_at, created_at, updated_at
			) VALUES ($1, 'vs_start_01', 'acct_start_01', $2, 'hash', $3, $4, $5, $6, $6)`,
			operationID, key, status, claimID, startedAt, createdAt)
		return err
	}

	if err := insert("op_start_01", "start_key_01", "pending", nil, nil); err != nil {
		t.Fatalf("insert pending start operation: %v", err)
	}
	assertPostgresCode(t, insert("op_start_02", "start_key_02", "pending", nil, nil), "23505")

	startedAt := createdAt.Add(time.Minute)
	_, err = pool.Exec(t.Context(), `
		UPDATE voice_session_start_operations
		SET status = 'completed', started_at = $1, updated_at = $1
		WHERE operation_id = 'op_start_01'`, startedAt)
	if err != nil {
		t.Fatalf("complete start operation: %v", err)
	}
	if err := insert("op_start_02", "start_key_02", "pending", nil, nil); err != nil {
		t.Fatalf("insert pending operation after completion: %v", err)
	}

	claimID := "claim_start_02"
	_, err = pool.Exec(t.Context(), `
		UPDATE voice_session_start_operations
		SET status = 'compensation_failed', compensation_claim_id = $1, updated_at = $2
		WHERE operation_id = 'op_start_02'`, claimID, startedAt)
	if err != nil {
		t.Fatalf("mark compensation failed: %v", err)
	}
	assertPostgresCode(t, insert("op_start_03", "start_key_03", "pending", nil, nil), "23505")

	assertPostgresCode(
		t,
		insert("op_invalid_status", "start_key_invalid", "unknown", nil, nil),
		"23514",
	)
	assertConstraintViolation(
		t,
		insert("op_duplicate_key", "start_key_01", "completed", nil, &startedAt),
		"voice_session_start_operations_key_unique",
	)
	_, err = pool.Exec(t.Context(), `
		INSERT INTO voice_session_start_operations (
			operation_id, session_id, account_id, idempotency_key, request_hash,
			status, created_at, updated_at
		) VALUES (
			'op_missing_session', 'vs_missing', 'acct_start_01',
			'start_key_missing', 'hash', 'pending', $1, $1
		)`, createdAt)
	assertConstraintViolation(
		t,
		err,
		"voice_session_start_operations_session_key",
	)
}

func testStartOperationStateConstraints(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	createdAt := time.Date(2026, time.July, 29, 11, 0, 0, 0, time.UTC)
	_, err := pool.Exec(t.Context(), `
		INSERT INTO lingow_accounts (id, kind, created_at)
		VALUES ('acct_start_states', 'anonymous', $1)`, createdAt)
	if err != nil {
		t.Fatalf("insert start-operation state account: %v", err)
	}
	for _, sessionID := range []string{
		"session_invalid_pending",
		"session_invalid_compensating",
		"session_invalid_completed",
		"session_invalid_compensated",
	} {
		_, err = pool.Exec(t.Context(), `
			INSERT INTO voice_sessions (
				id, account_id, status, audio_config, capabilities, created_at
			) VALUES ($1, 'acct_start_states', 'created', '{}'::jsonb, '{}'::jsonb, $2)`,
			sessionID, createdAt)
		if err != nil {
			t.Fatalf("insert start-operation state session %q: %v", sessionID, err)
		}
	}

	insert := func(operationID, sessionID, status string, claimID *string, startedAt *time.Time) error {
		_, err := pool.Exec(t.Context(), `
			INSERT INTO voice_session_start_operations (
				operation_id, session_id, account_id, idempotency_key, request_hash,
				status, compensation_claim_id, started_at, created_at, updated_at
			) VALUES ($1, $2, 'acct_start_states', $1, 'hash', $3, $4, $5, $6, $6)`,
			operationID, sessionID, status, claimID, startedAt, createdAt)
		return err
	}

	assertPostgresCode(t, insert(
		"op_invalid_pending", "session_invalid_pending", "pending", nil, &createdAt,
	), "23514")
	assertPostgresCode(t, insert(
		"op_invalid_compensating", "session_invalid_compensating", "compensating", nil, nil,
	), "23514")
	assertPostgresCode(t, insert(
		"op_invalid_completed", "session_invalid_completed", "completed", nil, nil,
	), "23514")
	assertPostgresCode(t, insert(
		"op_invalid_compensated", "session_invalid_compensated", "compensated", nil, nil,
	), "23514")
}

func testConcurrentSpeakerMappingConstraint(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	const writers = 8
	start := make(chan struct{})
	errorsByWriter := make(chan error, writers)
	var writersDone sync.WaitGroup
	for writer := range writers {
		writersDone.Go(func() {
			<-start
			errorsByWriter <- insertParticipant(t.Context(), pool, fmt.Sprintf("participant_%d", writer), "session_01", "speaker_01", nil)
		})
	}
	close(start)
	writersDone.Wait()
	close(errorsByWriter)

	successes := 0
	conflicts := 0
	for err := range errorsByWriter {
		if err == nil {
			successes++
			continue
		}
		assertPostgresCode(t, err, "23505")
		conflicts++
	}
	if successes != 1 || conflicts != writers-1 {
		t.Fatalf("concurrent participant inserts = %d successes, %d conflicts; want 1 success and %d conflicts", successes, conflicts, writers-1)
	}
}

func testProviderSpeakerConstraint(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	providerSpeakerID := "cluster_01"
	if err := insertParticipant(t.Context(), pool, "participant_provider_01", "session_01", "speaker_02", &providerSpeakerID); err != nil {
		t.Fatalf("insert participant with provider key: %v", err)
	}
	err := insertParticipant(t.Context(), pool, "participant_provider_02", "session_01", "speaker_03", &providerSpeakerID)
	assertPostgresCode(t, err, "23505")
}

func testTurnConstraints(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	createdAt := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)
	if err := insertTurn(t.Context(), pool, "turn_01", "event_01", "session_01", nil, 1, createdAt); err != nil {
		t.Fatalf("insert voice turn: %v", err)
	}
	assertPostgresCode(t, insertTurnWithPayloadHash(t.Context(), pool, "turn_hash", "event_hash", "session_hash", nil, 1, createdAt, []byte{1}), "23514")
	assertPostgresCode(t, insertTurn(t.Context(), pool, "turn_02", "event_01", "session_02", nil, 1, createdAt), "23505")
	assertPostgresCode(t, insertTurn(t.Context(), pool, "turn_03", "event_03", "session_01", nil, 1, createdAt), "23505")

	missingParticipantID := "participant_missing"
	assertPostgresCode(t, insertTurn(t.Context(), pool, "turn_04", "event_04", "session_01", &missingParticipantID, 2, createdAt), "23503")

	_, err := pool.Exec(t.Context(), "UPDATE voice_turns SET source_text = 'edited' WHERE id = 'turn_01'")
	if err == nil {
		t.Fatal("updating immutable source_text succeeded, want an error")
	}
	if _, err := pool.Exec(t.Context(), "UPDATE voice_turns SET attribution_status = 'confirmed', speaker_confidence = 0.9 WHERE id = 'turn_01'"); err != nil {
		t.Fatalf("updating attribution fields: %v", err)
	}
	if _, err := pool.Exec(t.Context(), "UPDATE voice_turns SET speaker_code = 'speaker_02' WHERE id = 'turn_01'"); err != nil {
		t.Fatalf("updating speaker snapshot field: %v", err)
	}
	if _, err := pool.Exec(t.Context(), "UPDATE voice_turns SET source_text = 'edited' WHERE id = 'turn_01'"); err == nil {
		t.Fatal("updating source_text after snapshot update succeeded, want an error")
	}
}

func insertParticipant(ctx context.Context, pool *pgxpool.Pool, id, sessionID, speakerCode string, providerSpeakerID *string) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO voice_session_participants (
			id, session_id, speaker_code, provider_speaker_id, created_at, updated_at
		) VALUES ($1, $2, $3, $4, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		id,
		sessionID,
		speakerCode,
		providerSpeakerID,
	)
	return err
}

func insertTurn(ctx context.Context, pool *pgxpool.Pool, id, eventID, sessionID string, participantID *string, sequenceNo int64, createdAt time.Time) error {
	return insertTurnWithPayloadHash(ctx, pool, id, eventID, sessionID, participantID, sequenceNo, createdAt, make([]byte, 32))
}

func insertTurnWithPayloadHash(ctx context.Context, pool *pgxpool.Pool, id, eventID, sessionID string, participantID *string, sequenceNo int64, createdAt time.Time, payloadHash []byte) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO voice_turns (
			id, event_id, event_payload_hash, session_id, participant_id, speaker_code, sequence_no,
			source_language, target_language, language_config_version, source_text,
			translated_text, attribution_status, started_at, ended_at, created_at
		) VALUES (
			$1, $2, $3, $4, $5, 'speaker_01', $6,
			'zh-CN', 'en-US', 1, 'source',
			'translation', 'pending', $7, $7, $7
		)`,
		id,
		eventID,
		payloadHash,
		sessionID,
		participantID,
		sequenceNo,
		createdAt,
	)
	return err
}

func assertPostgresCode(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("PostgreSQL error = nil, want SQLSTATE %s", want)
	}
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) {
		t.Fatalf("error = %v, want PostgreSQL SQLSTATE %s", err, want)
	}
	if postgresError.Code != want {
		t.Fatalf("PostgreSQL SQLSTATE = %s, want %s", postgresError.Code, want)
	}
}
