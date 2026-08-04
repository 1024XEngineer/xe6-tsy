//go:build integration

package recordstore

import (
	"context"
	"errors"
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
		turns.NewServiceAttributionReader(services.Turns, services.Participants),
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
		turns.NewServiceAttributionReader(services.Turns, services.Participants),
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
INSERT INTO lingow_accounts (id, kind) VALUES ('acct_new', 'anonymous');
UPDATE lingow_accounts SET merged_into = 'acct_new' WHERE id = 'acct_old'`); err != nil {
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
		turns.NewServiceAttributionReader(services.Turns, services.Participants),
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
	if err := o.pool.QueryRow(ctx, `SELECT COALESCE(merged_into, id) FROM lingow_accounts WHERE id = $1`, accountID).Scan(&canonicalID); err != nil {
		return "", MapError(err)
	}
	return canonicalID, nil
}
