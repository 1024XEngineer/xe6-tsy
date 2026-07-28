//go:build integration

package recordstore

import (
	"errors"
	"testing"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
	"github.com/1024XEngineer/xe6-tsy/services/api/participants"
)

func TestParticipantReadRepositoryPaginatesWithinAccountScope(t *testing.T) {
	pool := testDatabase(t)
	if err := Migrate(t.Context(), pool); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	for _, participant := range []struct {
		id          string
		sessionID   string
		speakerCode string
	}{
		{id: "participant_03", sessionID: "session_01", speakerCode: "speaker_03"},
		{id: "participant_01", sessionID: "session_01", speakerCode: "speaker_01"},
		{id: "participant_02", sessionID: "session_01", speakerCode: "speaker_02"},
		{id: "participant_other", sessionID: "session_02", speakerCode: "speaker_01"},
	} {
		if err := insertParticipant(t.Context(), pool, participant.id, participant.sessionID, participant.speakerCode, nil); err != nil {
			t.Fatalf("insert participant %s: %v", participant.id, err)
		}
	}

	codec, err := NewCursorCodec([]byte("participant-integration-key"))
	if err != nil {
		t.Fatalf("NewCursorCodec() error = %v", err)
	}
	repository, err := NewParticipantReadRepository(pool, codec)
	if err != nil {
		t.Fatalf("NewParticipantReadRepository() error = %v", err)
	}

	first, err := repository.List(t.Context(), "account_01", "session_01", participantQuery(2, ""))
	if err != nil {
		t.Fatalf("first List() error = %v", err)
	}
	assertParticipantIDs(t, first.Items, "participant_01", "participant_02")
	if first.NextCursor == nil {
		t.Fatal("first List() next cursor = nil")
	}

	second, err := repository.List(t.Context(), "account_01", "session_01", participantQuery(2, *first.NextCursor))
	if err != nil {
		t.Fatalf("second List() error = %v", err)
	}
	assertParticipantIDs(t, second.Items, "participant_03")
	if second.NextCursor != nil {
		t.Fatalf("second List() next cursor = %q, want nil", *second.NextCursor)
	}

	_, err = repository.List(t.Context(), "account_02", "session_01", participantQuery(2, *first.NextCursor))
	if !errors.Is(err, participants.ErrInvalidRequest) {
		t.Fatalf("cross-account cursor error = %v, want invalid request", err)
	}
	_, err = repository.List(t.Context(), "account_01", "session_01", participantQuery(1, *first.NextCursor))
	if !errors.Is(err, participants.ErrInvalidRequest) {
		t.Fatalf("changed-limit cursor error = %v, want invalid request", err)
	}
}

func participantQuery(limit int, cursor string) recordsv1.ListParticipantsQuery {
	return recordsv1.ListParticipantsQuery{Limit: limit, Cursor: cursor}
}

func assertParticipantIDs(t *testing.T, items []recordsv1.Participant, want ...string) {
	t.Helper()
	if len(items) != len(want) {
		t.Fatalf("participant count = %d, want %d: %#v", len(items), len(want), items)
	}
	for index := range want {
		if items[index].ID != want[index] {
			t.Fatalf("participant %d ID = %q, want %q", index, items[index].ID, want[index])
		}
	}
}
