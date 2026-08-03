//go:build integration

package recordstore

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
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

// TestAttributionBackfillCoversLegacyTurns verifies the backfill migration creates one task per
// pre-existing unresolved turn and repairs tasks acked while the turn stayed unresolved.
func TestAttributionBackfillCoversLegacyTurns(t *testing.T) {
	pool := testDatabase(t)
	if err := Migrate(t.Context(), pool); err != nil {
		t.Fatalf("Migrate() error = %v", err)
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
		t.Fatalf("re-run Migrate() error = %v", err)
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
