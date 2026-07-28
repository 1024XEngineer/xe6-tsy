package delivery

import (
	"context"
	"errors"
	"testing"
	"time"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
)

func TestRecordsTurnReaderMapsSnapshotsInProviderOrder(t *testing.T) {
	participantID := "participant_01"
	speakerLabel := "Speaker 1"
	createdAt := time.Date(2026, time.July, 28, 9, 30, 0, 0, time.UTC)
	provider := &recordsTurnReaderFake{snapshots: []recordsv1.FinalTurnSnapshot{
		{
			TurnID:                "turn_02",
			SessionID:             "session_02",
			ParticipantID:         nil,
			SpeakerLabelSnapshot:  nil,
			SourceLanguage:        "en-US",
			TargetLanguage:        "zh-CN",
			LanguageConfigVersion: 3,
			SourceText:            "Second source",
			TranslatedText:        "Second translation",
			CreatedAt:             createdAt.Add(time.Second),
		},
		{
			TurnID:                "turn_01",
			SessionID:             "session_01",
			ParticipantID:         &participantID,
			SpeakerLabelSnapshot:  &speakerLabel,
			SourceLanguage:        "zh-CN",
			TargetLanguage:        "en-US",
			LanguageConfigVersion: 2,
			SourceText:            "First source",
			TranslatedText:        "First translation",
			CreatedAt:             createdAt,
		},
	}}
	reader := NewRecordsTurnReader(provider)

	snapshots, err := reader.ReadFinalTurns(context.Background(), "account_01", []string{"turn_02", "turn_01"})
	if err != nil {
		t.Fatalf("ReadFinalTurns() error = %v", err)
	}
	if provider.accountID != "account_01" || len(provider.turnIDs) != 2 || provider.turnIDs[0] != "turn_02" || provider.turnIDs[1] != "turn_01" {
		t.Fatalf("provider request = account %q, turns %#v", provider.accountID, provider.turnIDs)
	}
	if len(snapshots) != 2 || snapshots[0].TurnID != "turn_02" || snapshots[1].TurnID != "turn_01" {
		t.Fatalf("ReadFinalTurns() snapshots = %#v", snapshots)
	}
	first := snapshots[0]
	if first.ParticipantID != nil || first.SpeakerLabelSnapshot != nil || first.LanguageConfigVersion != 3 || first.SourceText != "Second source" || !first.CreatedAt.Equal(createdAt.Add(time.Second)) {
		t.Fatalf("first snapshot = %#v", first)
	}
	second := snapshots[1]
	if second.ParticipantID == nil || *second.ParticipantID != "participant_01" || second.SpeakerLabelSnapshot == nil || *second.SpeakerLabelSnapshot != "Speaker 1" {
		t.Fatalf("second snapshot nullable fields = %#v", second)
	}
	if second.SessionID != "session_01" || second.SourceLanguage != "zh-CN" || second.TargetLanguage != "en-US" || second.LanguageConfigVersion != 2 || second.SourceText != "First source" || second.TranslatedText != "First translation" || !second.CreatedAt.Equal(createdAt) {
		t.Fatalf("second snapshot = %#v", second)
	}
}

func TestRecordsTurnReaderCopiesProviderSnapshots(t *testing.T) {
	participantID := "participant_01"
	speakerLabel := "Speaker 1"
	provider := &recordsTurnReaderFake{snapshots: []recordsv1.FinalTurnSnapshot{{
		TurnID:               "turn_01",
		ParticipantID:        &participantID,
		SpeakerLabelSnapshot: &speakerLabel,
	}}}
	reader := NewRecordsTurnReader(provider)

	snapshots, err := reader.ReadFinalTurns(context.Background(), "account_01", []string{"turn_01"})
	if err != nil {
		t.Fatalf("ReadFinalTurns() error = %v", err)
	}
	participantID = "participant_changed"
	speakerLabel = "Changed"
	provider.snapshots[0].TurnID = "turn_changed"
	if snapshots[0].TurnID != "turn_01" || *snapshots[0].ParticipantID != "participant_01" || *snapshots[0].SpeakerLabelSnapshot != "Speaker 1" {
		t.Fatalf("delivery snapshot changed with provider data: %#v", snapshots[0])
	}

	*snapshots[0].ParticipantID = "delivery_changed"
	*snapshots[0].SpeakerLabelSnapshot = "Delivery changed"
	if participantID != "participant_changed" || speakerLabel != "Changed" {
		t.Fatalf("provider pointers changed to participant=%q label=%q", participantID, speakerLabel)
	}
}

func TestRecordsTurnReaderPropagatesProviderError(t *testing.T) {
	wantErr := errors.New("read final turns")
	reader := NewRecordsTurnReader(&recordsTurnReaderFake{err: wantErr})

	snapshots, err := reader.ReadFinalTurns(context.Background(), "account_01", []string{"turn_01"})
	if !errors.Is(err, wantErr) {
		t.Fatalf("ReadFinalTurns() error = %v, want %v", err, wantErr)
	}
	if snapshots != nil {
		t.Fatalf("ReadFinalTurns() snapshots = %#v, want nil", snapshots)
	}
}

func TestRecordsTurnReaderPropagatesContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	reader := NewRecordsTurnReader(&recordsTurnReaderFake{useContextError: true})

	_, err := reader.ReadFinalTurns(ctx, "account_01", []string{"turn_01"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ReadFinalTurns() error = %v, want context canceled", err)
	}
}

func TestNewRecordsTurnReaderRejectsMissingProvider(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("NewRecordsTurnReader(nil) did not panic")
		}
	}()

	NewRecordsTurnReader(nil)
}

type recordsTurnReaderFake struct {
	snapshots       []recordsv1.FinalTurnSnapshot
	err             error
	useContextError bool
	accountID       string
	turnIDs         []string
}

func (reader *recordsTurnReaderFake) ReadFinalTurns(ctx context.Context, accountID string, turnIDs []string) ([]recordsv1.FinalTurnSnapshot, error) {
	reader.accountID = accountID
	reader.turnIDs = append([]string(nil), turnIDs...)
	if reader.useContextError {
		return nil, ctx.Err()
	}
	return reader.snapshots, reader.err
}
