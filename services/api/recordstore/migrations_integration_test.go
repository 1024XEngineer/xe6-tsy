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

	statuses, err := AppliedMigrations(t.Context(), pool)
	if err != nil {
		t.Fatalf("AppliedMigrations() error = %v", err)
	}
	if len(statuses) != 1 {
		t.Fatalf("len(AppliedMigrations()) = %d, want 1", len(statuses))
	}
	if status := statuses[0]; status.Version != 1 || status.Name != "voice_records" || status.AppliedAt.IsZero() {
		t.Fatalf("AppliedMigrations()[0] = %#v, want applied voice_records version 1", status)
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
	_, err := pool.Exec(ctx, `
		INSERT INTO voice_turns (
			id, event_id, session_id, participant_id, speaker_code, sequence_no,
			source_language, target_language, language_config_version, source_text,
			translated_text, attribution_status, started_at, ended_at, created_at
		) VALUES (
			$1, $2, $3, $4, 'speaker_01', $5,
			'zh-CN', 'en-US', 1, 'source',
			'translation', 'pending', $6, $6, $6
		)`,
		id,
		eventID,
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
