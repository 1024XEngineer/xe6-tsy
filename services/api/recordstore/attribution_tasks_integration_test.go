//go:build integration

package recordstore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
	"github.com/1024XEngineer/xe6-tsy/services/api/turns"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestAttributionTaskFlow verifies the durable chain: storing a pending final turn enqueues one
// task, the worker claims it, the provider resolver maps the speaker key and confirms the turn, and
// the task is settled.
func TestAttributionTaskFlow(t *testing.T) {
	pool := testDatabase(t)
	if err := Migrate(t.Context(), pool); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	insertOwnedSession(t, pool, "session_01", "acct_01")

	providerID := "cluster_01"
	writer := NewTurnWriter(pool)
	event := finalTurnEvent("event_01", "turn_01", "session_01", 1)
	event.ParticipantID = nil
	event.ProviderSpeakerID = &providerID
	event.AttributionStatus = recordsv1.AttributionPending
	if err := writer.StoreFinalTurn(t.Context(), event); err != nil {
		t.Fatalf("StoreFinalTurn() error = %v", err)
	}

	var taskCount int
	if err := pool.QueryRow(t.Context(), `SELECT COUNT(*) FROM attribution_tasks WHERE turn_id = $1`, event.TurnID).Scan(&taskCount); err != nil {
		t.Fatalf("count attribution tasks: %v", err)
	}
	if taskCount != 1 {
		t.Fatalf("attribution tasks = %d, want 1", taskCount)
	}

	services, err := NewServices(pool, make([]byte, 32), sessionOwnerStub{accountID: "acct_01"}, &postgresSessionScopeStub{pool: pool})
	if err != nil {
		t.Fatalf("NewServices() error = %v", err)
	}
	store := NewAttributionTaskStore(pool)
	worker, err := turns.NewAttributionWorker(
		store,
		services.AttributionResolver,
		sessionOwnerStub{accountID: "acct_01"},
		turns.NewServiceAttributionReader(services.Turns),
		services.Turns,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if err != nil {
		t.Fatalf("NewAttributionWorker() error = %v", err)
	}

	delivery, err := store.Receive(t.Context())
	if err != nil {
		t.Fatalf("Receive() error = %v", err)
	}
	task := delivery.Task()
	if task.TurnID != event.TurnID || task.AccountID != "acct_01" {
		t.Fatalf("claimed task = %#v", task)
	}
	if err := worker.Process(t.Context(), delivery); err != nil {
		t.Fatalf("worker Process() error = %v", err)
	}

	corrected, err := services.Turns.Get(t.Context(), "acct_01", event.TurnID)
	if err != nil {
		t.Fatalf("Get(corrected) error = %v", err)
	}
	if corrected.ParticipantID == nil || *corrected.ParticipantID == "" {
		t.Fatalf("corrected participant = %v, want a mapped participant", corrected.ParticipantID)
	}
	if corrected.ProviderSpeakerID == nil || *corrected.ProviderSpeakerID != providerID {
		t.Fatalf("corrected provider id = %v, want %q", corrected.ProviderSpeakerID, providerID)
	}
	if corrected.AttributionStatus != recordsv1.AttributionConfirmed {
		t.Fatalf("corrected status = %q, want confirmed", corrected.AttributionStatus)
	}

	var status string
	if err := pool.QueryRow(t.Context(), `SELECT status FROM attribution_tasks WHERE turn_id = $1`, event.TurnID).Scan(&status); err != nil {
		t.Fatalf("read task status: %v", err)
	}
	if status != "completed" {
		t.Fatalf("task status = %q, want completed", status)
	}
}

func TestAttributionTaskFlowMapsProviderKeyAfterParticipantPageLimit(t *testing.T) {
	pool := testDatabase(t)
	if err := Migrate(t.Context(), pool); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	insertOwnedSession(t, pool, "session_many_participants", "acct_many_participants")
	for index := 1; index <= 101; index++ {
		providerID := fmt.Sprintf("cluster_%03d", index)
		if err := insertParticipant(t.Context(), pool, fmt.Sprintf("participant_%03d", index), "session_many_participants", fmt.Sprintf("speaker_%02d", index), &providerID); err != nil {
			t.Fatalf("insert participant %d: %v", index, err)
		}
	}

	providerID := "cluster_101"
	event := finalTurnEvent("event_many_participants", "turn_many_participants", "session_many_participants", 1)
	event.ParticipantID = nil
	event.ProviderSpeakerID = &providerID
	event.AttributionStatus = recordsv1.AttributionPending
	if err := NewTurnWriter(pool).StoreFinalTurn(t.Context(), event); err != nil {
		t.Fatalf("StoreFinalTurn() error = %v", err)
	}

	services, err := NewServices(pool, make([]byte, 32), sessionOwnerStub{accountID: "acct_many_participants"}, &postgresSessionScopeStub{pool: pool})
	if err != nil {
		t.Fatalf("NewServices() error = %v", err)
	}
	store := NewAttributionTaskStore(pool)
	worker, err := turns.NewAttributionWorker(
		store,
		services.AttributionResolver,
		sessionOwnerStub{accountID: "acct_many_participants"},
		turns.NewServiceAttributionReader(services.Turns),
		services.Turns,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if err != nil {
		t.Fatalf("NewAttributionWorker() error = %v", err)
	}
	delivery, err := store.Receive(t.Context())
	if err != nil {
		t.Fatalf("Receive() error = %v", err)
	}
	if err := worker.Process(t.Context(), delivery); err != nil {
		t.Fatalf("worker Process() error = %v", err)
	}

	var participantID string
	if err := pool.QueryRow(t.Context(), `SELECT participant_id FROM voice_turns WHERE id = $1`, event.TurnID).Scan(&participantID); err != nil {
		t.Fatalf("read mapped participant: %v", err)
	}
	if participantID != "participant_101" {
		t.Fatalf("participant_id = %q, want participant_101", participantID)
	}
}

// TestAttributionTaskFailsWithoutProviderEvidence verifies a pending turn with no provider key is
// permanently failed instead of being acked as completed by the resolver.
func TestAttributionTaskFailsWithoutProviderEvidence(t *testing.T) {
	pool := testDatabase(t)
	if err := Migrate(t.Context(), pool); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	insertOwnedSession(t, pool, "session_01", "acct_01")

	writer := NewTurnWriter(pool)
	event := finalTurnEvent("event_02", "turn_02", "session_01", 2)
	event.ParticipantID = nil
	event.ProviderSpeakerID = nil
	event.AttributionStatus = recordsv1.AttributionPending
	if err := writer.StoreFinalTurn(t.Context(), event); err != nil {
		t.Fatalf("StoreFinalTurn() error = %v", err)
	}

	services, err := NewServices(pool, make([]byte, 32), sessionOwnerStub{accountID: "acct_01"}, &postgresSessionScopeStub{pool: pool})
	if err != nil {
		t.Fatalf("NewServices() error = %v", err)
	}
	store := NewAttributionTaskStore(pool)
	worker, err := turns.NewAttributionWorker(
		store,
		services.AttributionResolver,
		sessionOwnerStub{accountID: "acct_01"},
		turns.NewServiceAttributionReader(services.Turns),
		services.Turns,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if err != nil {
		t.Fatalf("NewAttributionWorker() error = %v", err)
	}

	delivery, err := store.Receive(t.Context())
	if err != nil {
		t.Fatalf("Receive() error = %v", err)
	}
	if err := worker.Process(t.Context(), delivery); err != nil {
		t.Fatalf("worker Process() error = %v", err)
	}

	var status, lastError string
	if err := pool.QueryRow(t.Context(), `SELECT status, COALESCE(last_error, '') FROM attribution_tasks WHERE turn_id = $1`, event.TurnID).Scan(&status, &lastError); err != nil {
		t.Fatalf("read task status: %v", err)
	}
	if status != "failed" {
		t.Fatalf("task status = %q, want failed", status)
	}
	if lastError == "" {
		t.Fatal("failed task must record an error")
	}
}

func TestAttributionTaskWorkerUsesCanonicalOwnerAfterAccountMerge(t *testing.T) {
	pool := testDatabase(t)
	if err := Migrate(t.Context(), pool); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	insertOwnedSession(t, pool, "session_01", "acct_old")
	providerID := "cluster_01"
	event := finalTurnEvent("event_merged_01", "turn_merged_01", "session_01", 1)
	event.ParticipantID = nil
	event.ProviderSpeakerID = &providerID
	event.AttributionStatus = recordsv1.AttributionPending
	if err := NewTurnWriter(pool).StoreFinalTurn(t.Context(), event); err != nil {
		t.Fatalf("StoreFinalTurn() error = %v", err)
	}
	if _, err := pool.Exec(t.Context(), `
INSERT INTO lingow_accounts (id, kind) VALUES
    ('acct_mid', 'anonymous'),
    ('acct_new', 'anonymous');
UPDATE lingow_accounts SET merged_into = 'acct_mid' WHERE id = 'acct_old';
UPDATE lingow_accounts SET merged_into = 'acct_new' WHERE id = 'acct_mid'`); err != nil {
		t.Fatalf("merge account fixture: %v", err)
	}

	owner := NewCanonicalSessionOwner(databaseCanonicalOwner{pool: pool})
	services, err := NewServices(pool, make([]byte, 32), owner, &postgresSessionScopeStub{pool: pool})
	if err != nil {
		t.Fatalf("NewServices() error = %v", err)
	}
	store := NewAttributionTaskStore(pool)
	worker, err := turns.NewAttributionWorker(
		store,
		services.AttributionResolver,
		owner,
		turns.NewServiceAttributionReader(services.Turns),
		services.Turns,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if err != nil {
		t.Fatalf("NewAttributionWorker() error = %v", err)
	}
	delivery, err := store.Receive(t.Context())
	if err != nil {
		t.Fatalf("Receive() error = %v", err)
	}
	if delivery.Task().AccountID != "acct_old" {
		t.Fatalf("task account ID = %q, want enqueue audit owner acct_old", delivery.Task().AccountID)
	}
	if err := worker.Process(t.Context(), delivery); err != nil {
		t.Fatalf("worker Process() error = %v", err)
	}

	corrected, err := services.Turns.Get(t.Context(), "acct_new", event.TurnID)
	if err != nil {
		t.Fatalf("Get(corrected) error = %v", err)
	}
	if corrected.ParticipantID == nil || corrected.AttributionStatus != recordsv1.AttributionConfirmed {
		t.Fatalf("corrected turn = %#v, want confirmed participant", corrected)
	}
}

func TestAttributionTaskEnqueueRejectsExistingTaskForAnotherSession(t *testing.T) {
	pool := testDatabase(t)
	if err := Migrate(t.Context(), pool); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	insertOwnedSession(t, pool, "session_target", "acct_01")
	insertOwnedSession(t, pool, "session_stale", "acct_01")
	if _, err := pool.Exec(t.Context(), `
INSERT INTO attribution_tasks (task_id, turn_id, session_id, account_id, task_type)
VALUES ('attr_turn_stale', 'turn_stale', 'session_stale', 'acct_01', 'turn_attribution')`); err != nil {
		t.Fatalf("insert stale attribution task: %v", err)
	}
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer tx.Rollback(t.Context())

	err = NewAttributionTaskStore(pool).Enqueue(t.Context(), tx, "turn_stale", "session_target")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Enqueue() error = %v, want not found", err)
	}
}

func TestAttributionTaskEnqueueRequiresResolvableSessionOwner(t *testing.T) {
	pool := testDatabase(t)
	if err := Migrate(t.Context(), pool); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer tx.Rollback(t.Context())

	err = NewAttributionTaskStore(pool).Enqueue(t.Context(), tx, "turn_missing", "session_missing")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Enqueue() error = %v, want not found", err)
	}
}

func TestAttributionTaskEnqueueIsIdempotentForExistingTurnTask(t *testing.T) {
	pool := testDatabase(t)
	if err := Migrate(t.Context(), pool); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	insertOwnedSession(t, pool, "session_01", "acct_01")
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer tx.Rollback(t.Context())
	store := NewAttributionTaskStore(pool)

	if err := store.Enqueue(t.Context(), tx, "turn_01", "session_01"); err != nil {
		t.Fatalf("first Enqueue() error = %v", err)
	}
	if err := store.Enqueue(t.Context(), tx, "turn_01", "session_01"); err != nil {
		t.Fatalf("second Enqueue() error = %v", err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatalf("commit tx: %v", err)
	}

	var count int
	if err := pool.QueryRow(t.Context(), `SELECT COUNT(*) FROM attribution_tasks WHERE turn_id = 'turn_01'`).Scan(&count); err != nil {
		t.Fatalf("count attribution tasks: %v", err)
	}
	if count != 1 {
		t.Fatalf("task count = %d, want 1", count)
	}
}

func TestAttributionTaskAckMarksTaskCompleted(t *testing.T) {
	pool := testDatabase(t)
	if err := Migrate(t.Context(), pool); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	seedAttributionTask(t, pool, "event_ack_01", "turn_ack_01", "session_ack_01", 1)
	delivery, err := NewAttributionTaskStore(pool).Receive(t.Context())
	if err != nil {
		t.Fatalf("Receive() error = %v", err)
	}
	if delivery.Task().Attempts != 1 {
		t.Fatalf("Task().Attempts = %d, want 1", delivery.Task().Attempts)
	}
	if err := delivery.Ack(); err != nil {
		t.Fatalf("Ack() error = %v", err)
	}
	var status string
	if err := pool.QueryRow(t.Context(), `SELECT status FROM attribution_tasks WHERE turn_id = 'turn_ack_01'`).Scan(&status); err != nil {
		t.Fatalf("read task status: %v", err)
	}
	if status != "completed" {
		t.Fatalf("task status = %q, want completed", status)
	}
}

func TestAttributionTaskRetrySchedulesAndRecordsError(t *testing.T) {
	pool := testDatabase(t)
	if err := Migrate(t.Context(), pool); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	seedAttributionTask(t, pool, "event_retry_01", "turn_retry_01", "session_retry_01", 1)
	delivery, err := NewAttributionTaskStore(pool).Receive(t.Context())
	if err != nil {
		t.Fatalf("Receive() error = %v", err)
	}
	before := time.Now()
	if err := delivery.Retry("transient failure"); err != nil {
		t.Fatalf("Retry() error = %v", err)
	}
	var (
		status      string
		lastError   *string
		availableAt time.Time
	)
	if err := pool.QueryRow(t.Context(), `
SELECT status, last_error, available_at
FROM attribution_tasks
WHERE turn_id = 'turn_retry_01'`).Scan(&status, &lastError, &availableAt); err != nil {
		t.Fatalf("read retried task: %v", err)
	}
	if status != "pending" || lastError == nil || *lastError != "transient failure" {
		t.Fatalf("retried task status=%q last_error=%v", status, lastError)
	}
	if !availableAt.After(before) {
		t.Fatalf("available_at = %v, want after %v", availableAt, before)
	}
}

func TestAttributionTaskFailMarksTaskFailed(t *testing.T) {
	pool := testDatabase(t)
	if err := Migrate(t.Context(), pool); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	seedAttributionTask(t, pool, "event_fail_01", "turn_fail_01", "session_fail_01", 1)
	delivery, err := NewAttributionTaskStore(pool).Receive(t.Context())
	if err != nil {
		t.Fatalf("Receive() error = %v", err)
	}
	if err := delivery.Fail("no provider speaker key"); err != nil {
		t.Fatalf("Fail() error = %v", err)
	}
	var (
		status    string
		lastError *string
	)
	if err := pool.QueryRow(t.Context(), `
SELECT status, last_error
FROM attribution_tasks
WHERE turn_id = 'turn_fail_01'`).Scan(&status, &lastError); err != nil {
		t.Fatalf("read failed task: %v", err)
	}
	if status != "failed" || lastError == nil || *lastError != "no provider speaker key" {
		t.Fatalf("failed task status=%q last_error=%v", status, lastError)
	}
}

func TestAttributionTaskStaleReceiptSettlementIsNoop(t *testing.T) {
	pool := testDatabase(t)
	if err := Migrate(t.Context(), pool); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	seedAttributionTask(t, pool, "event_stale_01", "turn_stale_01", "session_stale_01", 1)
	store := NewAttributionTaskStore(pool)
	first, err := store.Receive(t.Context())
	if err != nil {
		t.Fatalf("first Receive() error = %v", err)
	}
	if _, err := pool.Exec(t.Context(), `
UPDATE attribution_tasks SET locked_until = CURRENT_TIMESTAMP
WHERE turn_id = 'turn_stale_01'`); err != nil {
		t.Fatalf("expire first lease: %v", err)
	}
	second, err := store.Receive(t.Context())
	if err != nil {
		t.Fatalf("second Receive() error = %v", err)
	}
	if err := second.Ack(); err != nil {
		t.Fatalf("second Ack() error = %v", err)
	}
	if err := first.Ack(); err != nil {
		t.Fatalf("stale first Ack() error = %v", err)
	}
}

func TestAttributionTaskReceiveReturnsOnCancelledContext(t *testing.T) {
	pool := testDatabase(t)
	if err := Migrate(t.Context(), pool); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := NewAttributionTaskStore(pool).Receive(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Receive() error = %v, want context canceled", err)
	}
}

func seedAttributionTask(t *testing.T, pool *pgxpool.Pool, eventID, turnID, sessionID string, sequenceNo int64) {
	t.Helper()
	insertOwnedSession(t, pool, sessionID, "acct_01")
	event := finalTurnEvent(eventID, turnID, sessionID, sequenceNo)
	event.ParticipantID = nil
	event.AttributionStatus = recordsv1.AttributionPending
	if err := NewTurnWriter(pool).StoreFinalTurn(t.Context(), event); err != nil {
		t.Fatalf("StoreFinalTurn() error = %v", err)
	}
}

// TestAttributionBackfillCoversLegacyTurns verifies the backfill migration creates one task per
// pre-existing unresolved turn and repairs tasks acked while the turn stayed unresolved.
func TestAttributionBackfillCoversLegacyTurns(t *testing.T) {
	pool := testDatabase(t)
	if err := Migrate(t.Context(), pool); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	// Simulate a database that has applied through migration 16 before the async worker shipped.
	if _, err := pool.Exec(t.Context(), `DELETE FROM recordstore_schema_migrations WHERE version = 17`); err != nil {
		t.Fatalf("reset backfill migration state: %v", err)
	}
	insertOwnedSession(t, pool, "session_01", "acct_01")

	providerID := "cluster_01"
	if err := insertTurn(t.Context(), pool, "turn_backfill_01", "evt_backfill_01", "session_01", nil, 1, time.Now().UTC()); err != nil {
		t.Fatalf("insert legacy pending turn: %v", err)
	}
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `
		INSERT INTO attribution_tasks (task_id, turn_id, session_id, account_id, task_type, status)
		VALUES ($1, $2, 'session_01', 'acct_01', 'turn_attribution', 'completed')`,
		"attr_turn_backfill_01", "turn_backfill_01"); err != nil {
		t.Fatalf("insert completed task for unresolved turn: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE voice_turns SET provider_speaker_id = $1 WHERE id = $2`,
		providerID, "turn_backfill_01"); err != nil {
		t.Fatalf("set provider speaker id: %v", err)
	}
	if err := Migrate(t.Context(), pool); err != nil {
		t.Fatalf("apply backfill migration: %v", err)
	}

	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM attribution_tasks WHERE turn_id = $1`, "turn_backfill_01").Scan(&status); err != nil {
		t.Fatalf("read backfilled task status: %v", err)
	}
	if status != "pending" {
		t.Fatalf("backfilled task status = %q, want pending", status)
	}
}

type sessionOwnerStub struct {
	accountID string
}

func (s sessionOwnerStub) AccountIDForSession(context.Context, string) (string, error) {
	return s.accountID, nil
}

type postgresSessionScopeStub struct {
	pool *pgxpool.Pool
}

func (s *postgresSessionScopeStub) SessionIDsForAccount(ctx context.Context, accountID string) ([]string, error) {
	reader, err := NewPostgresSessionScopeReader(s.pool)
	if err != nil {
		return nil, err
	}
	return reader.SessionIDsForAccount(ctx, accountID)
}

type databaseCanonicalOwner struct {
	pool *pgxpool.Pool
}

func (o databaseCanonicalOwner) AccountIDForSession(ctx context.Context, sessionID string) (string, error) {
	var accountID string
	if err := o.pool.QueryRow(ctx, `SELECT account_id FROM voice_sessions WHERE id = $1`, sessionID).Scan(&accountID); err != nil {
		return "", MapError(err)
	}
	return accountID, nil
}

func (o databaseCanonicalOwner) CanonicalAccountID(ctx context.Context, accountID string) (string, error) {
	var canonicalID string
	if err := o.pool.QueryRow(ctx, `
WITH RECURSIVE ancestors AS (
    SELECT id, merged_into, ARRAY[id] AS visited
    FROM lingow_accounts
    WHERE id = $1

    UNION ALL

    SELECT parent.id, parent.merged_into, child.visited || parent.id
    FROM lingow_accounts AS parent
    JOIN ancestors AS child ON parent.id = child.merged_into
    WHERE NOT parent.id = ANY(child.visited)
)
SELECT id FROM ancestors
WHERE merged_into IS NULL
LIMIT 1`, accountID).Scan(&canonicalID); err != nil {
		return "", MapError(err)
	}
	return canonicalID, nil
}
