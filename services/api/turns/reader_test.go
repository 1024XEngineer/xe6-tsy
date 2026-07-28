package turns

import (
	"context"
	"errors"
	"testing"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
)

func TestReadFinalTurnsRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name      string
		accountID string
		turnIDs   []string
	}{
		{name: "missing account", turnIDs: []string{"vt_01"}},
		{name: "missing turns", accountID: "acct_01"},
		{name: "oversized batch", accountID: "acct_01", turnIDs: make([]string, recordsv1.MaxFinalTurnBatchSize+1)},
		{name: "empty turn", accountID: "acct_01", turnIDs: []string{"vt_01", ""}},
		{name: "duplicate turn", accountID: "acct_01", turnIDs: []string{"vt_01", "vt_01"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := &fakeRepository{}
			service := NewService(repository, fakeSessionOwners{}, nil)

			_, err := service.ReadFinalTurns(context.Background(), test.accountID, test.turnIDs)
			if !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("ReadFinalTurns() error = %v, want invalid request", err)
			}
			if repository.readCalls != 0 {
				t.Fatalf("ReadFinalTurns() repository calls = %d, want 0", repository.readCalls)
			}
		})
	}
}

func TestReadFinalTurnsPreservesAccountScopeAndRequestOrder(t *testing.T) {
	repository := &fakeRepository{snapshots: []recordsv1.FinalTurnSnapshot{
		{TurnID: "vt_02", SessionID: "vs_02"},
		{TurnID: "vt_01", SessionID: "vs_01"},
	}, mutateReadTurnIDs: true}
	service := NewService(repository, fakeSessionOwners{}, nil)
	turnIDs := []string{"vt_01", "vt_02"}

	snapshots, err := service.ReadFinalTurns(context.Background(), "acct_01", turnIDs)
	if err != nil {
		t.Fatalf("ReadFinalTurns() error = %v", err)
	}
	if repository.readCalls != 1 || repository.readAccountID != "acct_01" {
		t.Fatalf("repository calls = %d, account = %q", repository.readCalls, repository.readAccountID)
	}
	if len(repository.readTurnIDs) != 2 || repository.readTurnIDs[0] != "vt_01" || repository.readTurnIDs[1] != "vt_02" {
		t.Fatalf("repository turn IDs = %#v", repository.readTurnIDs)
	}
	if turnIDs[0] != "vt_01" || turnIDs[1] != "vt_02" {
		t.Fatalf("caller turn IDs changed to %#v", turnIDs)
	}
	if len(snapshots) != 2 || snapshots[0].TurnID != "vt_01" || snapshots[1].TurnID != "vt_02" {
		t.Fatalf("ReadFinalTurns() snapshots = %#v", snapshots)
	}
}

func TestReadFinalTurnsRejectsIncompleteBatch(t *testing.T) {
	repository := &fakeRepository{snapshots: []recordsv1.FinalTurnSnapshot{{TurnID: "vt_01"}}}
	service := NewService(repository, fakeSessionOwners{}, nil)

	snapshots, err := service.ReadFinalTurns(context.Background(), "acct_01", []string{"vt_01", "vt_missing"})
	if !errors.Is(err, ErrTurnNotFound) {
		t.Fatalf("ReadFinalTurns() error = %v, want turn not found", err)
	}
	if snapshots != nil {
		t.Fatalf("ReadFinalTurns() snapshots = %#v, want nil", snapshots)
	}
}

func TestReadFinalTurnsRejectsRepositoryInvariantViolations(t *testing.T) {
	tests := []struct {
		name      string
		snapshots []recordsv1.FinalTurnSnapshot
	}{
		{name: "empty ID", snapshots: []recordsv1.FinalTurnSnapshot{{}}},
		{name: "unrequested ID", snapshots: []recordsv1.FinalTurnSnapshot{{TurnID: "vt_other"}}},
		{name: "duplicate ID", snapshots: []recordsv1.FinalTurnSnapshot{{TurnID: "vt_01"}, {TurnID: "vt_01"}}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := NewService(&fakeRepository{snapshots: test.snapshots}, fakeSessionOwners{}, nil)

			snapshots, err := service.ReadFinalTurns(context.Background(), "acct_01", []string{"vt_01"})
			if err == nil {
				t.Fatal("ReadFinalTurns() error = nil, want repository invariant error")
			}
			if snapshots != nil {
				t.Fatalf("ReadFinalTurns() snapshots = %#v, want nil", snapshots)
			}
		})
	}
}

func TestReadFinalTurnsPropagatesRepositoryError(t *testing.T) {
	wantErr := errors.New("read failed")
	service := NewService(&fakeRepository{readErr: wantErr}, fakeSessionOwners{}, nil)

	_, err := service.ReadFinalTurns(context.Background(), "acct_01", []string{"vt_01"})
	if !errors.Is(err, wantErr) {
		t.Fatalf("ReadFinalTurns() error = %v, want %v", err, wantErr)
	}
}

func TestReadFinalTurnsCopiesNullableSnapshotFields(t *testing.T) {
	participantID := "p_01"
	speakerLabel := "Speaker 1"
	repository := &fakeRepository{snapshots: []recordsv1.FinalTurnSnapshot{{
		TurnID:               "vt_01",
		ParticipantID:        &participantID,
		SpeakerLabelSnapshot: &speakerLabel,
	}}}
	service := NewService(repository, fakeSessionOwners{}, nil)

	snapshots, err := service.ReadFinalTurns(context.Background(), "acct_01", []string{"vt_01"})
	if err != nil {
		t.Fatalf("ReadFinalTurns() error = %v", err)
	}
	participantID = "p_changed"
	speakerLabel = "Changed"
	if got := *snapshots[0].ParticipantID; got != "p_01" {
		t.Fatalf("ParticipantID = %q, want p_01", got)
	}
	if got := *snapshots[0].SpeakerLabelSnapshot; got != "Speaker 1" {
		t.Fatalf("SpeakerLabelSnapshot = %q, want Speaker 1", got)
	}

	*snapshots[0].ParticipantID = "p_result_changed"
	*snapshots[0].SpeakerLabelSnapshot = "Result changed"
	if participantID != "p_changed" || speakerLabel != "Changed" {
		t.Fatalf("repository pointers changed to participant=%q label=%q", participantID, speakerLabel)
	}
}
