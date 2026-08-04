//go:build integration

package recordstore

import (
	"errors"
	"testing"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
	"github.com/1024XEngineer/xe6-tsy/services/api/turns"
)

func TestRecordReadersReturnNoRowsForEmptyAccountScope(t *testing.T) {
	pool := testDatabase(t)
	if err := Migrate(t.Context(), pool); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	codec, err := NewCursorCodec([]byte("empty-scope-integration-key"))
	if err != nil {
		t.Fatalf("NewCursorCodec() error = %v", err)
	}
	participants, err := NewParticipantReadRepository(pool, codec)
	if err != nil {
		t.Fatalf("NewParticipantReadRepository() error = %v", err)
	}
	turnsRepository, err := NewTurnReadRepository(pool, codec, staticAccountSessionScopes{})
	if err != nil {
		t.Fatalf("NewTurnReadRepository() error = %v", err)
	}

	participantPage, err := participants.List(t.Context(), "account_empty", "session_missing", recordsv1.ListParticipantsQuery{Limit: 20})
	if err != nil || len(participantPage.Items) != 0 {
		t.Fatalf("participant List() = %#v, %v; want empty page", participantPage, err)
	}
	turnPage, err := turnsRepository.ListSession(t.Context(), "account_empty", "session_missing", recordsv1.ListTurnsQuery{Limit: 20})
	if err != nil || len(turnPage.Items) != 0 {
		t.Fatalf("turn ListSession() = %#v, %v; want empty page", turnPage, err)
	}
	history, err := turnsRepository.ListHistory(t.Context(), "account_empty", recordsv1.ListTurnsQuery{Limit: 20})
	if err != nil || len(history.Items) != 0 {
		t.Fatalf("ListHistory() = %#v, %v; want empty page", history, err)
	}
	if _, err := turnsRepository.Find(t.Context(), "account_empty", "turn_missing"); !errors.Is(err, turns.ErrTurnNotFound) {
		t.Fatalf("Find() error = %v, want not found", err)
	}
	if snapshots, err := turnsRepository.ReadFinalTurns(t.Context(), "account_empty", []string{"turn_missing"}); !errors.Is(err, turns.ErrTurnNotFound) || snapshots != nil {
		t.Fatalf("ReadFinalTurns() = %#v, %v; want nil and not found", snapshots, err)
	}
}
