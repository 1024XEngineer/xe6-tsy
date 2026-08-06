//go:build integration

package recordstore

import (
	"sync"
	"testing"
	"time"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
	"github.com/1024XEngineer/xe6-tsy/services/api/participants"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestParticipantWriterFindOrCreateIsSessionScopedAndConcurrent(t *testing.T) {
	pool := testDatabase(t)
	if err := Migrate(t.Context(), pool); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	writer := NewParticipantWriter(pool)
	observation := recordsv1.SpeakerObservation{
		SessionID:         "session_01",
		TurnID:            "turn_01",
		ProviderSpeakerID: "cluster_01",
	}

	const callers = 8
	start := make(chan struct{})
	results := make(chan recordsv1.Participant, callers)
	errorsByCaller := make(chan error, callers)
	var callersDone sync.WaitGroup
	for range callers {
		callersDone.Go(func() {
			<-start
			participant, err := writer.FindOrCreate(t.Context(), observation)
			results <- participant
			errorsByCaller <- err
		})
	}
	close(start)
	callersDone.Wait()
	close(results)
	close(errorsByCaller)

	var participantID string
	for err := range errorsByCaller {
		if err != nil {
			t.Fatalf("FindOrCreate() error = %v", err)
		}
	}
	for participant := range results {
		if participantID == "" {
			participantID = participant.ID
		}
		if participant.ID != participantID || participant.SpeakerCode != "speaker_01" {
			t.Fatalf("concurrent participant = %#v, want ID %q and speaker_01", participant, participantID)
		}
	}

	second, err := writer.FindOrCreate(t.Context(), recordsv1.SpeakerObservation{
		SessionID:         "session_01",
		TurnID:            "turn_02",
		ProviderSpeakerID: "cluster_02",
	})
	if err != nil {
		t.Fatalf("second FindOrCreate() error = %v", err)
	}
	if second.ID == participantID || second.SpeakerCode != "speaker_02" {
		t.Fatalf("second participant = %#v, want a new speaker_02", second)
	}

	otherSession, err := writer.FindOrCreate(t.Context(), recordsv1.SpeakerObservation{
		SessionID:         "session_02",
		TurnID:            "turn_03",
		ProviderSpeakerID: "cluster_01",
	})
	if err != nil {
		t.Fatalf("other-session FindOrCreate() error = %v", err)
	}
	if otherSession.ID == participantID || otherSession.SpeakerCode != "speaker_01" {
		t.Fatalf("other-session participant = %#v, want a new speaker_01", otherSession)
	}
}

func TestParticipantWriterUpdateDoesNotChangeTurnSnapshot(t *testing.T) {
	pool := testDatabase(t)
	if err := Migrate(t.Context(), pool); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	writer := NewParticipantWriter(pool)
	participant, err := writer.FindOrCreate(t.Context(), recordsv1.SpeakerObservation{
		SessionID:         "session_01",
		TurnID:            "turn_01",
		ProviderSpeakerID: "cluster_01",
	})
	if err != nil {
		t.Fatalf("FindOrCreate() error = %v", err)
	}

	createdAt := time.Date(2026, time.July, 28, 10, 0, 0, 0, time.UTC)
	if err := insertTurn(t.Context(), pool, "turn_01", "event_01", "session_01", &participant.ID, 1, createdAt); err != nil {
		t.Fatalf("insert turn: %v", err)
	}
	before := turnParticipantSnapshot(t, pool, "turn_01")

	displayName := "Speaker One"
	voiceProfileID := "voice_profile_01"
	updatedAt := createdAt.Add(time.Minute)
	updated, err := writer.Update(t.Context(), "session_01", participant.ID, participants.Update{
		DisplayName:       &displayName,
		DisplayNameSet:    true,
		VoiceProfileID:    &voiceProfileID,
		VoiceProfileIDSet: true,
		UpdatedAt:         updatedAt,
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updated.DisplayName == nil || *updated.DisplayName != displayName || updated.VoiceProfileID == nil || *updated.VoiceProfileID != voiceProfileID {
		t.Fatalf("updated participant = %#v", updated)
	}

	cleared, err := writer.Update(t.Context(), "session_01", participant.ID, participants.Update{
		VoiceProfileIDSet: true,
		UpdatedAt:         updatedAt.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("clear Update() error = %v", err)
	}
	if cleared.VoiceProfileID != nil {
		t.Fatalf("cleared voice profile = %v, want nil", *cleared.VoiceProfileID)
	}

	after := turnParticipantSnapshot(t, pool, "turn_01")
	if after != before {
		t.Fatalf("turn participant snapshot changed from %#v to %#v", before, after)
	}
}

type participantSnapshot struct {
	speakerCode       string
	displayName       *string
	providerSpeakerID *string
	voiceProfileID    *string
}

func turnParticipantSnapshot(t *testing.T, pool *pgxpool.Pool, turnID string) participantSnapshot {
	t.Helper()
	var snapshot participantSnapshot
	if err := pool.QueryRow(t.Context(), `
SELECT speaker_code, display_name, provider_speaker_id, voice_profile_id
FROM voice_turns
WHERE id = $1`, turnID).Scan(
		&snapshot.speakerCode,
		&snapshot.displayName,
		&snapshot.providerSpeakerID,
		&snapshot.voiceProfileID,
	); err != nil {
		t.Fatalf("read turn participant snapshot: %v", err)
	}
	return snapshot
}
