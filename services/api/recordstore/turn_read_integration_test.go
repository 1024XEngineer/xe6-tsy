//go:build integration

package recordstore

import (
	"context"
	"errors"
	"testing"
	"time"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
	"github.com/1024XEngineer/xe6-tsy/services/api/turns"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestTurnReadRepositoryListsAndFindsWithinAccountScope(t *testing.T) {
	pool := testDatabase(t)
	if err := Migrate(t.Context(), pool); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	participantID := "participant_01"
	if err := insertParticipant(t.Context(), pool, participantID, "session_01", "speaker_01", nil); err != nil {
		t.Fatalf("insert participant: %v", err)
	}
	baseTime := time.Date(2026, time.July, 28, 8, 0, 0, 0, time.UTC)
	fixtures := []readTurnFixture{
		{id: "turn_03", eventID: "event_03", sessionID: "session_01", sequenceNo: 3, participantID: &participantID, speakerCode: "speaker_01", sourceLanguage: "zh-CN", targetLanguage: "en-US", status: recordsv1.AttributionProvisional, createdAt: baseTime.Add(3 * time.Second)},
		{id: "turn_01", eventID: "event_01", sessionID: "session_01", sequenceNo: 1, speakerCode: "speaker_pending", sourceLanguage: "zh-CN", targetLanguage: "en-US", status: recordsv1.AttributionPending, createdAt: baseTime.Add(time.Second)},
		{id: "turn_02", eventID: "event_02", sessionID: "session_01", sequenceNo: 2, participantID: &participantID, speakerCode: "speaker_01", sourceLanguage: "en-US", targetLanguage: "zh-CN", status: recordsv1.AttributionConfirmed, createdAt: baseTime.Add(2 * time.Second)},
		{id: "turn_other", eventID: "event_other", sessionID: "session_02", sequenceNo: 1, speakerCode: "speaker_pending", sourceLanguage: "zh-CN", targetLanguage: "en-US", status: recordsv1.AttributionPending, createdAt: baseTime},
	}
	for _, fixture := range fixtures {
		insertReadTurn(t, pool, fixture)
	}

	codec, err := NewCursorCodec([]byte("turn-integration-key"))
	if err != nil {
		t.Fatalf("NewCursorCodec() error = %v", err)
	}
	scopes := staticAccountSessionScopes{
		"account_01": {"session_01"},
		"account_02": {"session_02"},
	}
	repository, err := NewTurnReadRepository(pool, codec, scopes)
	if err != nil {
		t.Fatalf("NewTurnReadRepository() error = %v", err)
	}

	first, err := repository.ListSession(t.Context(), "account_01", "session_01", recordsv1.ListTurnsQuery{Limit: 2})
	if err != nil {
		t.Fatalf("first ListSession() error = %v", err)
	}
	assertTurnIDs(t, first.Items, "turn_01", "turn_02")
	if first.NextCursor == nil {
		t.Fatal("first ListSession() next cursor = nil")
	}
	second, err := repository.ListSession(t.Context(), "account_01", "session_01", recordsv1.ListTurnsQuery{Limit: 2, Cursor: *first.NextCursor})
	if err != nil {
		t.Fatalf("second ListSession() error = %v", err)
	}
	assertTurnIDs(t, second.Items, "turn_03")

	filtered, err := repository.ListSession(t.Context(), "account_01", "session_01", recordsv1.ListTurnsQuery{
		Limit:             10,
		ParticipantID:     participantID,
		SpeakerCode:       "speaker_01",
		AttributionStatus: recordsv1.AttributionProvisional,
		SourceLanguage:    "zh-CN",
		TargetLanguage:    "en-US",
	})
	if err != nil {
		t.Fatalf("filtered ListSession() error = %v", err)
	}
	assertTurnIDs(t, filtered.Items, "turn_03")

	foreign, err := repository.ListSession(t.Context(), "account_02", "session_01", recordsv1.ListTurnsQuery{Limit: 20})
	if err != nil {
		t.Fatalf("foreign ListSession() error = %v", err)
	}
	assertTurnIDs(t, foreign.Items)

	found, err := repository.Find(t.Context(), "account_01", "turn_02")
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	if found.ID != "turn_02" || found.ParticipantID == nil || *found.ParticipantID != participantID {
		t.Fatalf("Find() = %#v", found)
	}
	if _, err := repository.Find(t.Context(), "account_02", "turn_02"); !errors.Is(err, turns.ErrTurnNotFound) {
		t.Fatalf("cross-account Find() error = %v, want not found", err)
	}
}

type staticAccountSessionScopes map[string][]string

func (scopes staticAccountSessionScopes) SessionIDsForAccount(_ context.Context, accountID string) ([]string, error) {
	return append([]string(nil), scopes[accountID]...), nil
}

type readTurnFixture struct {
	id             string
	eventID        string
	sessionID      string
	sequenceNo     int64
	participantID  *string
	speakerCode    string
	displayName    *string
	sourceLanguage string
	targetLanguage string
	status         recordsv1.AttributionStatus
	createdAt      time.Time
}

func insertReadTurn(t *testing.T, pool *pgxpool.Pool, fixture readTurnFixture) {
	t.Helper()
	_, err := pool.Exec(t.Context(), `
		INSERT INTO voice_turns (
			id, event_id, event_payload_hash, session_id, participant_id, speaker_code, display_name,
			sequence_no, source_language, target_language, language_config_version,
			source_text, translated_text, attribution_status, started_at, ended_at, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 1, 'source', 'translation', $11, $12, $12, $12)`,
		fixture.id,
		fixture.eventID,
		make([]byte, 32),
		fixture.sessionID,
		fixture.participantID,
		fixture.speakerCode,
		fixture.displayName,
		fixture.sequenceNo,
		fixture.sourceLanguage,
		fixture.targetLanguage,
		fixture.status,
		fixture.createdAt,
	)
	if err != nil {
		t.Fatalf("insert turn %s: %v", fixture.id, err)
	}
}

func assertTurnIDs(t *testing.T, items []recordsv1.VoiceTurn, want ...string) {
	t.Helper()
	if len(items) != len(want) {
		t.Fatalf("turn count = %d, want %d: %#v", len(items), len(want), items)
	}
	for index := range want {
		if items[index].ID != want[index] {
			t.Fatalf("turn %d ID = %q, want %q", index, items[index].ID, want[index])
		}
	}
}
