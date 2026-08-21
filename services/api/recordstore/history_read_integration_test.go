//go:build integration

package recordstore

import (
	"errors"
	"testing"
	"time"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
	"github.com/1024XEngineer/xe6-tsy/services/api/turns"
)

func TestTurnReadRepositoryPaginatesAccountHistory(t *testing.T) {
	pool := testDatabase(t)
	if err := Migrate(t.Context(), pool); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	participantID := "history_participant"
	if err := insertParticipant(t.Context(), pool, participantID, "session_01", "speaker_01", nil); err != nil {
		t.Fatalf("insert participant: %v", err)
	}
	baseTime := time.Date(2026, time.July, 28, 9, 0, 0, 0, time.UTC)
	fixtures := []readTurnFixture{
		{id: "history_04", eventID: "history_event_04", sessionID: "session_02", sequenceNo: 1, speakerCode: "speaker_pending", sourceLanguage: "en-US", targetLanguage: "zh-CN", status: recordsv1.AttributionPending, createdAt: baseTime.Add(4 * time.Second)},
		{id: "history_03_b", eventID: "history_event_03_b", sessionID: "session_01", sequenceNo: 3, participantID: &participantID, speakerCode: "speaker_01", sourceLanguage: "zh-CN", targetLanguage: "en-US", status: recordsv1.AttributionConfirmed, createdAt: baseTime.Add(3 * time.Second)},
		{id: "history_03_a", eventID: "history_event_03_a", sessionID: "session_01", sequenceNo: 2, participantID: &participantID, speakerCode: "speaker_01", sourceLanguage: "zh-CN", targetLanguage: "en-US", status: recordsv1.AttributionProvisional, createdAt: baseTime.Add(3 * time.Second)},
		{id: "history_01", eventID: "history_event_01", sessionID: "session_01", sequenceNo: 1, speakerCode: "speaker_pending", sourceLanguage: "en-US", targetLanguage: "zh-CN", status: recordsv1.AttributionPending, createdAt: baseTime.Add(time.Second)},
		{id: "history_foreign", eventID: "history_event_foreign", sessionID: "session_03", sequenceNo: 1, speakerCode: "speaker_pending", sourceLanguage: "zh-CN", targetLanguage: "en-US", status: recordsv1.AttributionPending, createdAt: baseTime.Add(5 * time.Second)},
	}
	for _, fixture := range fixtures {
		insertReadTurn(t, pool, fixture)
	}

	codec, err := NewCursorCodec([]byte("history-integration-key"))
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

	first, err := repository.ListHistory(t.Context(), "account_01", recordsv1.ListTurnsQuery{Limit: 2})
	if err != nil {
		t.Fatalf("first ListHistory() error = %v", err)
	}
	assertTurnIDs(t, first.Items, "history_04", "history_03_b")
	if first.NextCursor == nil {
		t.Fatal("first ListHistory() next cursor = nil")
	}
	second, err := repository.ListHistory(t.Context(), "account_01", recordsv1.ListTurnsQuery{Limit: 2, Cursor: *first.NextCursor})
	if err != nil {
		t.Fatalf("second ListHistory() error = %v", err)
	}
	assertTurnIDs(t, second.Items, "history_03_a", "history_01")
	_, err = repository.ListHistory(t.Context(), "account_01", recordsv1.ListTurnsQuery{
		Limit:     2,
		SessionID: "session_01",
		Cursor:    *first.NextCursor,
	})
	if !errors.Is(err, turns.ErrInvalidRequest) {
		t.Fatalf("filtered cursor error = %v, want invalid request", err)
	}

	from := baseTime.Add(3 * time.Second)
	to := baseTime.Add(3 * time.Second)
	filtered, err := repository.ListHistory(t.Context(), "account_01", recordsv1.ListTurnsQuery{
		Limit:          10,
		SessionID:      "session_01",
		ParticipantID:  participantID,
		SourceLanguage: "zh-CN",
		TargetLanguage: "en-US",
		CreatedFrom:    &from,
		CreatedTo:      &to,
	})
	if err != nil {
		t.Fatalf("filtered ListHistory() error = %v", err)
	}
	assertTurnIDs(t, filtered.Items, "history_03_b", "history_03_a")

	foreign, err := repository.ListHistory(t.Context(), "account_02", recordsv1.ListTurnsQuery{Limit: 10})
	if err != nil {
		t.Fatalf("foreign ListHistory() error = %v", err)
	}
	assertTurnIDs(t, foreign.Items, "history_foreign")

	_, err = repository.ListHistory(t.Context(), "account_02", recordsv1.ListTurnsQuery{Limit: 2, Cursor: *first.NextCursor})
	if !errors.Is(err, turns.ErrInvalidRequest) {
		t.Fatalf("cross-account cursor error = %v, want invalid request", err)
	}
}
