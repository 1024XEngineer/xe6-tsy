//go:build integration

package recordstore

import (
	"errors"
	"testing"
	"time"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
	"github.com/1024XEngineer/xe6-tsy/services/api/turns"
)

func TestTurnReadRepositoryReadsCompleteOrderedFinalTurnBatch(t *testing.T) {
	pool := testDatabase(t)
	if err := Migrate(t.Context(), pool); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	participantID := "snapshot_participant"
	if err := insertParticipant(t.Context(), pool, participantID, "session_01", "speaker_01", nil); err != nil {
		t.Fatalf("insert participant: %v", err)
	}
	displayName := "Speaker A"
	createdAt := time.Date(2026, time.July, 28, 10, 0, 0, 0, time.UTC)
	fixtures := []readTurnFixture{
		{id: "snapshot_01", eventID: "snapshot_event_01", sessionID: "session_01", sequenceNo: 1, participantID: &participantID, speakerCode: "speaker_01", displayName: &displayName, sourceLanguage: "zh-CN", targetLanguage: "en-US", status: recordsv1.AttributionConfirmed, createdAt: createdAt},
		{id: "snapshot_02", eventID: "snapshot_event_02", sessionID: "session_02", sequenceNo: 1, speakerCode: "speaker_pending", sourceLanguage: "en-US", targetLanguage: "zh-CN", status: recordsv1.AttributionPending, createdAt: createdAt.Add(time.Second)},
		{id: "snapshot_foreign", eventID: "snapshot_event_foreign", sessionID: "session_03", sequenceNo: 1, speakerCode: "speaker_pending", sourceLanguage: "zh-CN", targetLanguage: "en-US", status: recordsv1.AttributionPending, createdAt: createdAt.Add(2 * time.Second)},
	}
	for _, fixture := range fixtures {
		insertReadTurn(t, pool, fixture)
	}

	codec, err := NewCursorCodec([]byte("snapshot-integration-key"))
	if err != nil {
		t.Fatalf("NewCursorCodec() error = %v", err)
	}
	repository, err := NewTurnReadRepository(pool, codec, staticAccountSessionScopes{
		"account_01": {"session_01", "session_02"},
		"account_02": {"session_03"},
	})
	if err != nil {
		t.Fatalf("NewTurnReadRepository() error = %v", err)
	}

	snapshots, err := repository.ReadFinalTurns(t.Context(), "account_01", []string{"snapshot_02", "snapshot_01"})
	if err != nil {
		t.Fatalf("ReadFinalTurns() error = %v", err)
	}
	if len(snapshots) != 2 || snapshots[0].TurnID != "snapshot_02" || snapshots[1].TurnID != "snapshot_01" {
		t.Fatalf("ReadFinalTurns() order = %#v", snapshots)
	}
	if snapshots[0].ParticipantID != nil || snapshots[0].SpeakerLabelSnapshot != nil {
		t.Fatalf("pending snapshot = %#v, want nullable attribution", snapshots[0])
	}
	if snapshots[1].ParticipantID == nil || *snapshots[1].ParticipantID != participantID || snapshots[1].SpeakerLabelSnapshot == nil || *snapshots[1].SpeakerLabelSnapshot != displayName {
		t.Fatalf("attributed snapshot = %#v", snapshots[1])
	}

	tests := []struct {
		name string
		ids  []string
	}{
		{name: "missing", ids: []string{"snapshot_01", "snapshot_missing"}},
		{name: "foreign", ids: []string{"snapshot_01", "snapshot_foreign"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshots, err := repository.ReadFinalTurns(t.Context(), "account_01", test.ids)
			if !errors.Is(err, turns.ErrTurnNotFound) {
				t.Fatalf("ReadFinalTurns() error = %v, want not found", err)
			}
			if snapshots != nil {
				t.Fatalf("ReadFinalTurns() snapshots = %#v, want nil", snapshots)
			}
		})
	}
}
