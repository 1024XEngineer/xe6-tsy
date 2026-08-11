//go:build integration

package recordstore

import (
	"errors"
	"testing"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/modeprojection"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestModeProjectionPostgresIdempotencyAndConflict(t *testing.T) {
	pool := testDatabase(t)
	if err := Migrate(t.Context(), pool); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	insertModeProjectionSession(t, pool, "mode_projection_account", "mode_projection_session")

	repository := modeprojection.NewPostgresRepository(pool)
	event := modeProjectionEvent(
		"mode-event-1", "mode_projection_session", "runtime-1", 2,
		realtimev1.ModeInterpretation, realtimev1.ModeAssistant,
		time.Date(2026, time.August, 11, 8, 0, 0, 0, time.UTC),
	)
	if err := repository.Project(t.Context(), event); err != nil {
		t.Fatalf("first Project() error = %v", err)
	}
	if err := repository.Project(t.Context(), event); err != nil {
		t.Fatalf("replay Project() error = %v", err)
	}
	conflict := event
	conflict.TraceID = "different-trace"
	if err := repository.Project(t.Context(), conflict); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("conflicting Project() error = %v, want conflict", err)
	}

	projection, err := repository.Latest(t.Context(), event.SessionID)
	if err != nil {
		t.Fatalf("Latest() error = %v", err)
	}
	if projection.LastEventID != event.EventID || projection.ActiveMode != event.ToMode || projection.Generation != event.ResultingGeneration {
		t.Fatalf("Latest() = %#v, want first event projection", projection)
	}
	var auditCount int
	if err := pool.QueryRow(t.Context(), `SELECT COUNT(*) FROM realtime_mode_events WHERE session_id=$1`, event.SessionID).Scan(&auditCount); err != nil {
		t.Fatalf("count mode events: %v", err)
	}
	if auditCount != 1 {
		t.Fatalf("audit event count = %d, want 1", auditCount)
	}
	_, err = pool.Exec(t.Context(), `UPDATE realtime_mode_events SET trace_id='changed' WHERE event_id=$1`, event.EventID)
	assertPostgresCode(t, err, "P0001")
}

func TestModeProjectionPostgresRetainsAuditAndOrdersProjection(t *testing.T) {
	pool := testDatabase(t)
	if err := Migrate(t.Context(), pool); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	insertModeProjectionSession(t, pool, "mode_order_account", "mode_order_session")
	repository := modeprojection.NewPostgresRepository(pool)
	time20 := time.Date(2026, time.August, 11, 8, 0, 20, 0, time.UTC)
	time25 := time20.Add(5 * time.Second)
	events := []realtimev1.ModeChangedEvent{
		modeProjectionEvent("event-20", "mode_order_session", "runtime-a", 3, realtimev1.ModeInterpretation, realtimev1.ModeAssistant, time20),
		modeProjectionEvent("event-21", "mode_order_session", "runtime-a", 2, realtimev1.ModeAssistant, realtimev1.ModeInterpretation, time20.Add(time.Second)),
		modeProjectionEvent("event-25-b", "mode_order_session", "runtime-b", 1, realtimev1.ModeInterpretation, realtimev1.ModeAssistant, time25),
		modeProjectionEvent("event-24-z", "mode_order_session", "runtime-a", 4, realtimev1.ModeAssistant, realtimev1.ModeInterpretation, time25.Add(-time.Second)),
		modeProjectionEvent("event-25-a", "mode_order_session", "runtime-c", 9, realtimev1.ModeAssistant, realtimev1.ModeInterpretation, time25),
		modeProjectionEvent("event-25-c", "mode_order_session", "runtime-d", 1, realtimev1.ModeAssistant, realtimev1.ModeInterpretation, time25),
	}
	for _, event := range events {
		if err := repository.Project(t.Context(), event); err != nil {
			t.Fatalf("Project(%s) error = %v", event.EventID, err)
		}
	}
	projection, err := repository.Latest(t.Context(), "mode_order_session")
	if err != nil {
		t.Fatalf("Latest() error = %v", err)
	}
	if projection.LastEventID != "event-25-c" || projection.RuntimeInstanceID != "runtime-d" || projection.Generation != 1 {
		t.Fatalf("Latest() = %#v, want deterministic event-25-c projection", projection)
	}
	var auditCount int
	if err := pool.QueryRow(t.Context(), `SELECT COUNT(*) FROM realtime_mode_events WHERE session_id='mode_order_session'`).Scan(&auditCount); err != nil {
		t.Fatalf("count mode events: %v", err)
	}
	if auditCount != len(events) {
		t.Fatalf("audit event count = %d, want %d", auditCount, len(events))
	}
}

func TestModeProjectionPostgresValidationAndMissingProjection(t *testing.T) {
	pool := testDatabase(t)
	if err := Migrate(t.Context(), pool); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	repository := modeprojection.NewPostgresRepository(pool)
	if err := repository.Project(t.Context(), realtimev1.ModeChangedEvent{}); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("Project(invalid) error = %v, want invalid argument", err)
	}
	missingSessionEvent := modeProjectionEvent(
		"missing-session-event", "missing-session", "runtime-1", 2,
		realtimev1.ModeInterpretation, realtimev1.ModeAssistant, time.Now().UTC(),
	)
	if err := repository.Project(t.Context(), missingSessionEvent); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Project(missing session) error = %v, want not found", err)
	}
	if _, err := repository.Latest(t.Context(), ""); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("Latest(empty) error = %v, want invalid argument", err)
	}
	if _, err := repository.Latest(t.Context(), "missing-session"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Latest(missing) error = %v, want not found", err)
	}
}

func insertModeProjectionSession(t *testing.T, pool *pgxpool.Pool, accountID, sessionID string) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), `INSERT INTO lingow_accounts (id,kind,created_at) VALUES ($1,'anonymous',CURRENT_TIMESTAMP)`, accountID); err != nil {
		t.Fatalf("insert mode projection account: %v", err)
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO voice_sessions (id,account_id,status,audio_config,capabilities) VALUES ($1,$2,'created','{}'::jsonb,'{}'::jsonb)`, sessionID, accountID); err != nil {
		t.Fatalf("insert mode projection session: %v", err)
	}
}

func modeProjectionEvent(
	eventID string,
	sessionID string,
	runtimeID string,
	generation int64,
	fromMode realtimev1.Mode,
	toMode realtimev1.Mode,
	occurredAt time.Time,
) realtimev1.ModeChangedEvent {
	return realtimev1.ModeChangedEvent{
		EventVersion:        realtimev1.ModeChangedEventVersion,
		EventID:             eventID,
		TraceID:             "trace-" + eventID,
		SessionID:           sessionID,
		RuntimeInstanceID:   runtimeID,
		OperationID:         "operation-" + eventID,
		FromMode:            fromMode,
		ToMode:              toMode,
		ResultingGeneration: generation,
		OccurredAt:          occurredAt,
	}
}
