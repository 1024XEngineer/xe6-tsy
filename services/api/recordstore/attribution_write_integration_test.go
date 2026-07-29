//go:build integration

package recordstore

import (
	"errors"
	"testing"
	"time"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
	"github.com/1024XEngineer/xe6-tsy/services/api/turns"
)

func TestTurnWriterCorrectAttributionPreservesImmutableFields(t *testing.T) {
	pool := testDatabase(t)
	if err := Migrate(t.Context(), pool); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	participant, err := NewParticipantWriter(pool).FindOrCreate(t.Context(), recordsv1.SpeakerObservation{
		SessionID:         "session_01",
		TurnID:            "turn_01",
		ProviderSpeakerID: "cluster_01",
	})
	if err != nil {
		t.Fatalf("FindOrCreate() error = %v", err)
	}

	writer := NewTurnWriter(pool)
	event := finalTurnEvent("event_01", "turn_01", "session_01", 1)
	event.ParticipantID = nil
	event.AttributionStatus = recordsv1.AttributionPending
	if err := writer.StoreFinalTurn(t.Context(), event); err != nil {
		t.Fatalf("StoreFinalTurn() error = %v", err)
	}

	confidence := 0.97
	correctedAt := event.OccurredAt.Add(time.Minute)
	updated, err := writer.CorrectAttribution(t.Context(), turns.AttributionUpdate{
		TurnID:            event.TurnID,
		ParticipantID:     participant.ID,
		AttributionStatus: recordsv1.AttributionCorrected,
		SpeakerConfidence: &confidence,
		CorrectedBy:       recordsv1.CorrectedBySystem,
		CorrectedAt:       correctedAt,
	})
	if err != nil {
		t.Fatalf("CorrectAttribution() error = %v", err)
	}
	if updated.ParticipantID == nil || *updated.ParticipantID != participant.ID {
		t.Fatalf("participant ID = %v, want %q", updated.ParticipantID, participant.ID)
	}
	if updated.AttributionStatus != recordsv1.AttributionCorrected || updated.SpeakerConfidence == nil || *updated.SpeakerConfidence != confidence {
		t.Fatalf("updated attribution = %#v", updated)
	}
	if updated.CorrectedBy == nil || *updated.CorrectedBy != recordsv1.CorrectedBySystem || updated.CorrectedAt == nil || !updated.CorrectedAt.Equal(correctedAt) {
		t.Fatalf("correction audit fields = %#v", updated)
	}
	if updated.SourceText != event.SourceText ||
		updated.TranslatedText != event.TranslatedText ||
		updated.SourceLanguage != event.SourceLanguage ||
		updated.TargetLanguage != event.TargetLanguage ||
		updated.LanguageConfigVersion != event.LanguageConfigVersion ||
		updated.SpeakerCode != event.SpeakerCode ||
		updated.CreatedAt != event.OccurredAt {
		t.Fatalf("immutable final turn fields changed: %#v", updated)
	}
}

func TestTurnWriterCorrectAttributionRejectsCrossSessionParticipant(t *testing.T) {
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
	writer := NewTurnWriter(pool)
	event := finalTurnEvent("event_01", "turn_01", "session_01", 1)
	event.ParticipantID = nil
	event.AttributionStatus = recordsv1.AttributionPending
	if err := writer.StoreFinalTurn(t.Context(), event); err != nil {
		t.Fatalf("StoreFinalTurn() error = %v", err)
	}

	_, err = writer.CorrectAttribution(t.Context(), turns.AttributionUpdate{
		TurnID:            event.TurnID,
		ParticipantID:     participant.ID,
		AttributionStatus: recordsv1.AttributionConfirmed,
		CorrectedBy:       recordsv1.CorrectedBySystem,
		CorrectedAt:       event.OccurredAt.Add(time.Minute),
	})
	if !errors.Is(err, turns.ErrInvalidAttribution) {
		t.Fatalf("CorrectAttribution() error = %v, want invalid attribution", err)
	}

	var (
		participantID     *string
		attributionStatus recordsv1.AttributionStatus
	)
	if err := pool.QueryRow(t.Context(), `
SELECT participant_id, attribution_status
FROM voice_turns
WHERE id = $1`, event.TurnID).Scan(&participantID, &attributionStatus); err != nil {
		t.Fatalf("read unchanged attribution: %v", err)
	}
	if participantID != nil || attributionStatus != recordsv1.AttributionPending {
		t.Fatalf("failed correction changed participant=%v status=%q", participantID, attributionStatus)
	}
}
