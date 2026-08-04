//go:build integration

package recordstore

import (
	"errors"
	"sync"
	"testing"
	"time"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
)

func TestTurnWriterStoresAndReplaysFinalTurn(t *testing.T) {
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
		t.Fatalf("first StoreFinalTurn() error = %v", err)
	}
	if err := writer.StoreFinalTurn(t.Context(), event); err != nil {
		t.Fatalf("replay StoreFinalTurn() error = %v", err)
	}

	var (
		count                 int
		languageConfigVersion int64
		createdAt             time.Time
	)
	if err := pool.QueryRow(t.Context(), `
SELECT COUNT(*), MAX(language_config_version), MAX(created_at)
FROM voice_turns
WHERE id = $1`, event.TurnID).Scan(&count, &languageConfigVersion, &createdAt); err != nil {
		t.Fatalf("read stored final turn: %v", err)
	}
	if count != 1 || languageConfigVersion != event.LanguageConfigVersion || !createdAt.Equal(event.OccurredAt) {
		t.Fatalf("stored final turn count=%d version=%d created_at=%v", count, languageConfigVersion, createdAt)
	}
}

func TestTurnWriterReplaysLegacyHashWithRouteFields(t *testing.T) {
	pool := testDatabase(t)
	if err := Migrate(t.Context(), pool); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	writer := NewTurnWriter(pool)
	legacyEvent := finalTurnEvent("event_legacy", "turn_legacy", "session_legacy", 1)
	legacyEvent.ParticipantID = nil
	legacyEvent.AttributionStatus = recordsv1.AttributionPending
	if err := writer.StoreFinalTurn(t.Context(), legacyEvent); err != nil {
		t.Fatalf("store legacy FinalTurn() error = %v", err)
	}

	currentEvent := legacyEvent
	currentEvent.TTSEnabled = true
	currentEvent.DeliveryEnabled = true
	if err := writer.StoreFinalTurn(t.Context(), currentEvent); err != nil {
		t.Fatalf("replay current FinalTurn() error = %v", err)
	}
}

func TestTurnWriterRejectsConflictingReplayKeys(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*recordsv1.FinalTurnEvent)
	}{
		{
			name: "event ID",
			mutate: func(event *recordsv1.FinalTurnEvent) {
				event.TranslatedText = "different translation"
			},
		},
		{
			name: "turn ID",
			mutate: func(event *recordsv1.FinalTurnEvent) {
				event.EventID = "event_02"
				event.TranslatedText = "different translation"
			},
		},
		{
			name: "session sequence",
			mutate: func(event *recordsv1.FinalTurnEvent) {
				event.EventID = "event_02"
				event.TurnID = "turn_02"
				event.TranslatedText = "different translation"
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
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
				t.Fatalf("seed StoreFinalTurn() error = %v", err)
			}

			test.mutate(&event)
			if err := writer.StoreFinalTurn(t.Context(), event); !errors.Is(err, domain.ErrConflict) {
				t.Fatalf("conflicting StoreFinalTurn() error = %v, want conflict", err)
			}
		})
	}
}

func TestTurnWriterConcurrentReplayCreatesOneTurn(t *testing.T) {
	pool := testDatabase(t)
	if err := Migrate(t.Context(), pool); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	insertOwnedSession(t, pool, "session_01", "acct_01")
	writer := NewTurnWriter(pool)
	event := finalTurnEvent("event_01", "turn_01", "session_01", 1)
	event.ParticipantID = nil
	event.AttributionStatus = recordsv1.AttributionPending

	const callers = 8
	start := make(chan struct{})
	errorsByCaller := make(chan error, callers)
	var callersDone sync.WaitGroup
	for range callers {
		callersDone.Go(func() {
			<-start
			errorsByCaller <- writer.StoreFinalTurn(t.Context(), event)
		})
	}
	close(start)
	callersDone.Wait()
	close(errorsByCaller)
	for err := range errorsByCaller {
		if err != nil {
			t.Fatalf("concurrent StoreFinalTurn() error = %v", err)
		}
	}

	var count int
	if err := pool.QueryRow(t.Context(), `SELECT COUNT(*) FROM voice_turns`).Scan(&count); err != nil {
		t.Fatalf("count final turns: %v", err)
	}
	if count != 1 {
		t.Fatalf("final turn count = %d, want 1", count)
	}
}

func TestTurnWriterConcurrentConflictingReplayKeepsOnePayload(t *testing.T) {
	pool := testDatabase(t)
	if err := Migrate(t.Context(), pool); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	insertOwnedSession(t, pool, "session_01", "acct_01")
	writer := NewTurnWriter(pool)
	first := finalTurnEvent("event_01", "turn_01", "session_01", 1)
	first.ParticipantID = nil
	first.AttributionStatus = recordsv1.AttributionPending
	second := first
	second.TranslatedText = "different translation"

	start := make(chan struct{})
	results := make(chan error, 2)
	var callersDone sync.WaitGroup
	for _, event := range []recordsv1.FinalTurnEvent{first, second} {
		callersDone.Go(func() {
			<-start
			results <- writer.StoreFinalTurn(t.Context(), event)
		})
	}
	close(start)
	callersDone.Wait()
	close(results)

	var successes, conflicts int
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, domain.ErrConflict):
			conflicts++
		default:
			t.Fatalf("concurrent StoreFinalTurn() error = %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent results = %d successes, %d conflicts; want one each", successes, conflicts)
	}

	var count int
	if err := pool.QueryRow(t.Context(), `SELECT COUNT(*) FROM voice_turns`).Scan(&count); err != nil {
		t.Fatalf("count final turns: %v", err)
	}
	if count != 1 {
		t.Fatalf("final turn count = %d, want 1", count)
	}
}

func TestTurnWriterRejectsParticipantFromAnotherSession(t *testing.T) {
	pool := testDatabase(t)
	if err := Migrate(t.Context(), pool); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	participant, err := NewParticipantWriter(pool).FindOrCreate(t.Context(), recordsv1.SpeakerObservation{
		SessionID:         "session_02",
		TurnID:            "turn_02",
		ProviderSpeakerID: "cluster_01",
	})
	if err != nil {
		t.Fatalf("FindOrCreate() error = %v", err)
	}
	event := finalTurnEvent("event_01", "turn_01", "session_01", 1)
	event.ParticipantID = &participant.ID

	if err := NewTurnWriter(pool).StoreFinalTurn(t.Context(), event); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("StoreFinalTurn() error = %v, want not found", err)
	}
}

func TestTurnWriterRollsBackPendingTurnWhenAttributionTaskCannotEnqueue(t *testing.T) {
	pool := testDatabase(t)
	if err := Migrate(t.Context(), pool); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	event := finalTurnEvent("event_missing_session", "turn_missing_session", "session_missing", 1)
	event.ParticipantID = nil
	event.AttributionStatus = recordsv1.AttributionPending

	if err := NewTurnWriter(pool).StoreFinalTurn(t.Context(), event); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("StoreFinalTurn() error = %v, want not found", err)
	}

	var turnCount int
	if err := pool.QueryRow(t.Context(), `SELECT COUNT(*) FROM voice_turns WHERE id = $1`, event.TurnID).Scan(&turnCount); err != nil {
		t.Fatalf("count rolled back turn: %v", err)
	}
	if turnCount != 0 {
		t.Fatalf("rolled back turn count = %d, want 0", turnCount)
	}
}

func finalTurnEvent(eventID, turnID, sessionID string, sequenceNo int64) recordsv1.FinalTurnEvent {
	startedAt := time.Date(2026, time.July, 28, 10, 0, 0, 0, time.UTC)
	participantID := "participant_01"
	label := "Speaker One"
	confidence := 0.91
	return recordsv1.FinalTurnEvent{
		EventVersion:          recordsv1.FinalTurnEventVersion,
		EventID:               eventID,
		TraceID:               "trace_01",
		TurnID:                turnID,
		SessionID:             sessionID,
		ParticipantID:         &participantID,
		SequenceNo:            sequenceNo,
		SourceLanguage:        "en-US",
		TargetLanguage:        "zh-CN",
		LanguageConfigVersion: 3,
		SourceText:            "Hello",
		TranslatedText:        "Ni hao",
		SpeakerCode:           "speaker_01",
		SpeakerLabelSnapshot:  &label,
		SpeakerConfidence:     &confidence,
		AttributionStatus:     recordsv1.AttributionProvisional,
		StartedAt:             startedAt,
		EndedAt:               startedAt.Add(2 * time.Second),
		OccurredAt:            startedAt.Add(3 * time.Second),
	}
}
