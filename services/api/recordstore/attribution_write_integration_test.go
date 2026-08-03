//go:build integration

package recordstore

import (
	"errors"
	"testing"
	"time"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
	"github.com/1024XEngineer/xe6-tsy/services/api/participants"
	"github.com/1024XEngineer/xe6-tsy/services/api/turns"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestTurnWriterCorrectAttributionSyncsSpeakerSnapshot(t *testing.T) {
	pool := testDatabase(t)
	if err := Migrate(t.Context(), pool); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	insertOwnedSession(t, pool, "session_01", "acct_01")
	writer := NewParticipantWriter(pool)
	participant, err := writer.FindOrCreate(t.Context(), recordsv1.SpeakerObservation{
		SessionID:         "session_01",
		TurnID:            "turn_01",
		ProviderSpeakerID: "cluster_01",
	})
	if err != nil {
		t.Fatalf("FindOrCreate() error = %v", err)
	}
	displayName := "说话人 A"
	providerID := "diar_01"
	voiceProfileID := "vp_01"
	if _, err := writer.Update(t.Context(), "session_01", participant.ID, participantsUpdateWith(displayName, providerID, voiceProfileID)); err != nil {
		t.Fatalf("participant Update() error = %v", err)
	}

	turnWriter := NewTurnWriter(pool)
	event := finalTurnEvent("event_01", "turn_01", "session_01", 1)
	event.ParticipantID = nil
	event.AttributionStatus = recordsv1.AttributionPending
	if err := turnWriter.StoreFinalTurn(t.Context(), event); err != nil {
		t.Fatalf("StoreFinalTurn() error = %v", err)
	}

	confidence := 0.97
	correctedAt := event.OccurredAt.Add(time.Minute)
	updated, err := turnWriter.CorrectAttribution(t.Context(), turns.AttributionUpdate{
		AccountID:            "acct_01",
		TurnID:               event.TurnID,
		ParticipantID:        participant.ID,
		AttributionStatus:    recordsv1.AttributionCorrected,
		SpeakerConfidence:    &confidence,
		SpeakerConfidenceSet: true,
		CorrectedBy:          recordsv1.CorrectedBySystem,
		CorrectedAt:          correctedAt,
	})
	if err != nil {
		t.Fatalf("CorrectAttribution() error = %v", err)
	}
	if updated.ParticipantID == nil || *updated.ParticipantID != participant.ID {
		t.Fatalf("participant ID = %v, want %q", updated.ParticipantID, participant.ID)
	}
	if updated.SpeakerCode != participant.SpeakerCode {
		t.Fatalf("speaker_code = %q, want %q from target participant", updated.SpeakerCode, participant.SpeakerCode)
	}
	if updated.DisplayName == nil || *updated.DisplayName != displayName {
		t.Fatalf("display_name = %v, want %q from target participant", updated.DisplayName, displayName)
	}
	if updated.ProviderSpeakerID == nil || *updated.ProviderSpeakerID != providerID {
		t.Fatalf("provider_speaker_id = %v, want %q", updated.ProviderSpeakerID, providerID)
	}
	if updated.VoiceProfileID == nil || *updated.VoiceProfileID != voiceProfileID {
		t.Fatalf("voice_profile_id = %v, want %q", updated.VoiceProfileID, voiceProfileID)
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
		updated.CreatedAt != event.OccurredAt {
		t.Fatalf("immutable final turn fields changed: %#v", updated)
	}
}

func TestTurnWriterCorrectAttributionPreservesConfidenceWhenAbsent(t *testing.T) {
	pool := testDatabase(t)
	if err := Migrate(t.Context(), pool); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	insertOwnedSession(t, pool, "session_01", "acct_01")
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
	existingConfidence := 0.4
	event.SpeakerConfidence = &existingConfidence
	if err := writer.StoreFinalTurn(t.Context(), event); err != nil {
		t.Fatalf("StoreFinalTurn() error = %v", err)
	}

	updated, err := writer.CorrectAttribution(t.Context(), turns.AttributionUpdate{
		AccountID:            "acct_01",
		TurnID:               event.TurnID,
		ParticipantID:        participant.ID,
		AttributionStatus:    recordsv1.AttributionConfirmed,
		SpeakerConfidenceSet: false,
		CorrectedBy:          recordsv1.CorrectedBySystem,
		CorrectedAt:          event.OccurredAt.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("CorrectAttribution() error = %v", err)
	}
	if updated.SpeakerConfidence == nil || *updated.SpeakerConfidence != existingConfidence {
		t.Fatalf("speaker_confidence = %v, want preserved %v", updated.SpeakerConfidence, existingConfidence)
	}
}

func TestTurnWriterCorrectAttributionClearsConfidenceWhenExplicitNull(t *testing.T) {
	pool := testDatabase(t)
	if err := Migrate(t.Context(), pool); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	insertOwnedSession(t, pool, "session_01", "acct_01")
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
	existingConfidence := 0.4
	event.SpeakerConfidence = &existingConfidence
	if err := writer.StoreFinalTurn(t.Context(), event); err != nil {
		t.Fatalf("StoreFinalTurn() error = %v", err)
	}

	updated, err := writer.CorrectAttribution(t.Context(), turns.AttributionUpdate{
		AccountID:            "acct_01",
		TurnID:               event.TurnID,
		ParticipantID:        participant.ID,
		AttributionStatus:    recordsv1.AttributionConfirmed,
		SpeakerConfidence:    nil,
		SpeakerConfidenceSet: true,
		CorrectedBy:          recordsv1.CorrectedBySystem,
		CorrectedAt:          event.OccurredAt.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("CorrectAttribution() error = %v", err)
	}
	if updated.SpeakerConfidence != nil {
		t.Fatalf("speaker_confidence = %v, want explicit null", *updated.SpeakerConfidence)
	}
}

func TestTurnWriterCorrectAttributionRejectsCrossSessionParticipant(t *testing.T) {
	pool := testDatabase(t)
	if err := Migrate(t.Context(), pool); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	insertOwnedSession(t, pool, "session_01", "acct_01")
	insertOwnedSession(t, pool, "session_02", "acct_01")
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
		AccountID:         "acct_01",
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

func TestTurnWriterCorrectAttributionRejectsForeignAccount(t *testing.T) {
	pool := testDatabase(t)
	if err := Migrate(t.Context(), pool); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	insertOwnedSession(t, pool, "session_01", "acct_01")
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

	_, err = writer.CorrectAttribution(t.Context(), turns.AttributionUpdate{
		AccountID:         "acct_other",
		TurnID:            event.TurnID,
		ParticipantID:     participant.ID,
		AttributionStatus: recordsv1.AttributionConfirmed,
		CorrectedBy:       recordsv1.CorrectedBySystem,
		CorrectedAt:       event.OccurredAt.Add(time.Minute),
	})
	if !errors.Is(err, turns.ErrTurnNotFound) {
		t.Fatalf("CorrectAttribution() error = %v, want turn not found", err)
	}
}

func insertOwnedSession(t *testing.T, pool *pgxpool.Pool, sessionID, accountID string) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO lingow_accounts (id, kind) VALUES ($1, 'anonymous')
		ON CONFLICT (id) DO NOTHING`, accountID); err != nil {
		t.Fatalf("insert owned account: %v", err)
	}
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO voice_sessions (id, account_id, status, audio_config, capabilities) VALUES
			($1, $2, 'created', '{}'::jsonb, '{}'::jsonb)`, sessionID, accountID); err != nil {
		t.Fatalf("insert owned session: %v", err)
	}
}

func participantsUpdateWith(displayName, providerID, voiceProfileID string) participants.Update {
	return participants.Update{
		DisplayName:          &displayName,
		DisplayNameSet:       true,
		ProviderSpeakerID:    &providerID,
		ProviderSpeakerIDSet: true,
		VoiceProfileID:       &voiceProfileID,
		VoiceProfileIDSet:    true,
		UpdatedAt:            time.Date(2026, 7, 24, 9, 0, 0, 0, time.UTC),
	}
}
