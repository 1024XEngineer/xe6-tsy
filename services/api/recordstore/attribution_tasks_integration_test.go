//go:build integration

package recordstore

import (
	"context"
	"io"
	"log/slog"
	"testing"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
	"github.com/1024XEngineer/xe6-tsy/services/api/turns"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestAttributionTaskFlow verifies the durable chain: storing a pending final turn enqueues one
// task, the worker claims it, the resolver decision corrects the turn, and the task is settled.
func TestAttributionTaskFlow(t *testing.T) {
	pool := testDatabase(t)
	if err := Migrate(t.Context(), pool); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	insertOwnedSession(t, pool, "session_01", "acct_01")

	writer := NewTurnWriter(pool)
	event := finalTurnEvent("event_01", "turn_01", "session_01", 1)
	event.ParticipantID = nil
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

	participant, err := NewParticipantWriter(pool).FindOrCreate(t.Context(), recordsv1.SpeakerObservation{
		SessionID:         "session_01",
		TurnID:            "turn_01",
		ProviderSpeakerID: "cluster_01",
	})
	if err != nil {
		t.Fatalf("FindOrCreate() error = %v", err)
	}

	services, err := NewServices(pool, make([]byte, 32), sessionOwnerStub{accountID: "acct_01"}, &postgresSessionScopeStub{pool: pool})
	if err != nil {
		t.Fatalf("NewServices() error = %v", err)
	}
	store := NewAttributionTaskStore(pool)
	worker, err := turns.NewAttributionWorker(
		store,
		&turns.SingleDecisionResolver{Decision: &turns.AttributionDecision{
			ParticipantID:        participant.ID,
			AttributionStatus:    recordsv1.AttributionConfirmed,
			SpeakerConfidence:    confidencePtr(0.9),
			SpeakerConfidenceSet: true,
		}},
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
	if corrected.ParticipantID == nil || *corrected.ParticipantID != participant.ID {
		t.Fatalf("corrected participant = %v, want %q", corrected.ParticipantID, participant.ID)
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

func confidencePtr(value float64) *float64 {
	return &value
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
