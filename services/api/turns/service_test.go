package turns

import (
	"context"
	"errors"
	"testing"
	"time"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
)

func TestConsumeFinalTurnIsIdempotentAndPreservesEvent(t *testing.T) {
	repository := &fakeRepository{}
	service := NewService(repository, fakeSessionOwners{}, nil)
	participantID := "p_01"
	confidence := 0.91
	event := recordsv1.FinalTurnEvent{
		EventID:               "evt_01",
		TurnID:                "vt_01",
		SessionID:             "vs_01",
		ParticipantID:         &participantID,
		SequenceNo:            4,
		SourceLanguage:        "en-US",
		TargetLanguage:        "zh-CN",
		LanguageConfigVersion: 8,
		SourceText:            "Hello",
		TranslatedText:        "Ni hao",
		SpeakerCode:           "speaker_01",
		SpeakerConfidence:     &confidence,
		AttributionStatus:     recordsv1.AttributionProvisional,
		StartedAt:             time.Date(2026, 7, 24, 8, 0, 0, 0, time.UTC),
		EndedAt:               time.Date(2026, 7, 24, 8, 0, 2, 0, time.UTC),
		OccurredAt:            time.Date(2026, 7, 24, 8, 0, 3, 0, time.UTC),
	}

	if err := service.ConsumeFinalTurn(context.Background(), event); err != nil {
		t.Fatalf("first ConsumeFinalTurn() error = %v", err)
	}
	duplicateEventID := event
	duplicateEventID.TurnID = "vt_02"
	if err := service.ConsumeFinalTurn(context.Background(), duplicateEventID); err != nil {
		t.Fatalf("duplicate event ID ConsumeFinalTurn() error = %v", err)
	}
	duplicateTurnID := event
	duplicateTurnID.EventID = "evt_02"
	if err := service.ConsumeFinalTurn(context.Background(), duplicateTurnID); err != nil {
		t.Fatalf("duplicate turn ID ConsumeFinalTurn() error = %v", err)
	}

	if got := len(repository.events); got != 1 {
		t.Fatalf("stored events = %d, want 1", got)
	}
	if got := repository.events[0]; got != event {
		t.Fatalf("stored event = %#v, want %#v", got, event)
	}
}

func TestConsumeFinalTurnAllowsPendingWithoutParticipant(t *testing.T) {
	repository := &fakeRepository{}
	service := NewService(repository, fakeSessionOwners{}, nil)
	event := validEvent()
	event.ParticipantID = nil
	event.AttributionStatus = recordsv1.AttributionPending

	if err := service.ConsumeFinalTurn(context.Background(), event); err != nil {
		t.Fatalf("ConsumeFinalTurn() error = %v", err)
	}
}

func TestConsumeFinalTurnRejectsUnknownAttributionStatus(t *testing.T) {
	service := NewService(&fakeRepository{}, fakeSessionOwners{}, nil)
	event := validEvent()
	event.AttributionStatus = "unknown"

	if err := service.ConsumeFinalTurn(context.Background(), event); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("ConsumeFinalTurn() error = %v, want invalid request", err)
	}
}

func TestCorrectAttributionPreservesImmutableTurnFields(t *testing.T) {
	now := time.Date(2026, 7, 24, 9, 0, 0, 0, time.UTC)
	participantID := "p_02"
	confidence := 0.88
	original := recordsv1.VoiceTurn{
		ID:                    "vt_01",
		SessionID:             "vs_01",
		SequenceNo:            3,
		SourceLanguage:        "en-US",
		TargetLanguage:        "zh-CN",
		LanguageConfigVersion: 7,
		SourceText:            "immutable source",
		TranslatedText:        "immutable translation",
		AttributionStatus:     recordsv1.AttributionPending,
	}
	repository := &fakeRepository{turn: original, participantInSession: true}
	service := NewService(repository, fakeSessionOwners{ownerID: "acct_01"}, func() time.Time { return now })

	updated, err := service.CorrectAttribution(context.Background(), "acct_01", "vt_01", recordsv1.UpdateAttributionRequest{
		ParticipantID:     participantID,
		AttributionStatus: recordsv1.AttributionCorrected,
		SpeakerConfidence: &confidence,
	})
	if err != nil {
		t.Fatalf("CorrectAttribution() error = %v", err)
	}
	if updated.SourceText != original.SourceText || updated.TranslatedText != original.TranslatedText || updated.SourceLanguage != original.SourceLanguage || updated.TargetLanguage != original.TargetLanguage || updated.LanguageConfigVersion != original.LanguageConfigVersion {
		t.Fatalf("CorrectAttribution() changed immutable fields: %#v", updated)
	}
	if updated.ParticipantID == nil || *updated.ParticipantID != participantID || updated.AttributionStatus != recordsv1.AttributionCorrected {
		t.Fatalf("CorrectAttribution() result = %#v", updated)
	}
	if updated.CorrectedBy == nil || *updated.CorrectedBy != recordsv1.CorrectedBySystem || updated.CorrectedAt == nil || !updated.CorrectedAt.Equal(now) {
		t.Fatalf("CorrectAttribution() correction fields = %#v", updated)
	}
}

func TestCorrectAttributionRejectsInvalidTarget(t *testing.T) {
	tests := []struct {
		name        string
		accountID   string
		request     recordsv1.UpdateAttributionRequest
		participant bool
		wantErr     error
	}{
		{name: "invalid status", accountID: "acct_01", request: recordsv1.UpdateAttributionRequest{ParticipantID: "p_01", AttributionStatus: recordsv1.AttributionPending}, wantErr: ErrInvalidAttribution},
		{name: "participant belongs to another session", accountID: "acct_01", request: recordsv1.UpdateAttributionRequest{ParticipantID: "p_01", AttributionStatus: recordsv1.AttributionConfirmed}, wantErr: ErrInvalidAttribution},
		{name: "cross account", accountID: "acct_02", request: recordsv1.UpdateAttributionRequest{ParticipantID: "p_01", AttributionStatus: recordsv1.AttributionConfirmed}, participant: true, wantErr: ErrForbidden},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := &fakeRepository{turn: recordsv1.VoiceTurn{ID: "vt_01", SessionID: "vs_01"}, participantInSession: test.participant}
			service := NewService(repository, fakeSessionOwners{ownerID: "acct_01"}, nil)

			_, err := service.CorrectAttribution(context.Background(), test.accountID, "vt_01", test.request)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("CorrectAttribution() error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestGetAndListOperationsEnforceOwnership(t *testing.T) {
	repository := &fakeRepository{
		turn:            recordsv1.VoiceTurn{ID: "vt_01", SessionID: "vs_01"},
		listResponse:    recordsv1.VoiceTurnListResponse{Items: []recordsv1.VoiceTurn{{ID: "vt_01"}}},
		historyResponse: recordsv1.VoiceTurnListResponse{Items: []recordsv1.VoiceTurn{{ID: "vt_01"}}},
	}
	service := NewService(repository, fakeSessionOwners{ownerID: "acct_01"}, nil)

	if _, err := service.Get(context.Background(), "acct_02", "vt_01"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("Get() error = %v, want forbidden", err)
	}
	if _, err := service.ListSession(context.Background(), "acct_02", "vs_01", recordsv1.ListTurnsQuery{}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("ListSession() error = %v, want forbidden", err)
	}
	if _, err := service.ListHistory(context.Background(), "acct_02", recordsv1.ListTurnsQuery{SessionID: "vs_01"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("ListHistory() error = %v, want forbidden", err)
	}
}

func TestReadFinalTurnsPassesAccountScope(t *testing.T) {
	repository := &fakeRepository{snapshots: []recordsv1.FinalTurnSnapshot{{TurnID: "vt_01"}}}
	service := NewService(repository, fakeSessionOwners{}, nil)

	snapshots, err := service.ReadFinalTurns(context.Background(), "acct_01", []string{"vt_01"})
	if err != nil {
		t.Fatalf("ReadFinalTurns() error = %v", err)
	}
	if repository.readAccountID != "acct_01" || len(snapshots) != 1 {
		t.Fatalf("ReadFinalTurns() account = %q, snapshots = %#v", repository.readAccountID, snapshots)
	}
}

func validEvent() recordsv1.FinalTurnEvent {
	participantID := "p_01"
	return recordsv1.FinalTurnEvent{
		EventID:           "evt_01",
		TurnID:            "vt_01",
		SessionID:         "vs_01",
		ParticipantID:     &participantID,
		SourceLanguage:    "en-US",
		TargetLanguage:    "zh-CN",
		AttributionStatus: recordsv1.AttributionProvisional,
	}
}

type fakeRepository struct {
	events               []recordsv1.FinalTurnEvent
	eventIDs             map[string]struct{}
	turnIDs              map[string]struct{}
	turn                 recordsv1.VoiceTurn
	listResponse         recordsv1.VoiceTurnListResponse
	historyResponse      recordsv1.VoiceTurnListResponse
	participantInSession bool
	lastUpdate           AttributionUpdate
	snapshots            []recordsv1.FinalTurnSnapshot
	readAccountID        string
}

func (r *fakeRepository) StoreFinalTurn(_ context.Context, event recordsv1.FinalTurnEvent) error {
	if r.eventIDs == nil {
		r.eventIDs = make(map[string]struct{})
		r.turnIDs = make(map[string]struct{})
	}
	if _, exists := r.eventIDs[event.EventID]; exists {
		return nil
	}
	if _, exists := r.turnIDs[event.TurnID]; exists {
		return nil
	}
	r.eventIDs[event.EventID] = struct{}{}
	r.turnIDs[event.TurnID] = struct{}{}
	r.events = append(r.events, event)
	return nil
}

func (r *fakeRepository) ListSession(context.Context, string, recordsv1.ListTurnsQuery) (recordsv1.VoiceTurnListResponse, error) {
	return r.listResponse, nil
}

func (r *fakeRepository) Find(context.Context, string) (recordsv1.VoiceTurn, error) {
	return r.turn, nil
}

func (r *fakeRepository) ListHistory(context.Context, string, recordsv1.ListTurnsQuery) (recordsv1.VoiceTurnListResponse, error) {
	return r.historyResponse, nil
}

func (r *fakeRepository) ParticipantBelongsToSession(context.Context, string, string) (bool, error) {
	return r.participantInSession, nil
}

func (r *fakeRepository) CorrectAttribution(_ context.Context, update AttributionUpdate) (recordsv1.VoiceTurn, error) {
	r.lastUpdate = update
	updated := r.turn
	updated.ParticipantID = &update.ParticipantID
	updated.AttributionStatus = update.AttributionStatus
	updated.SpeakerConfidence = update.SpeakerConfidence
	updated.CorrectedBy = &update.CorrectedBy
	updated.CorrectedAt = &update.CorrectedAt
	return updated, nil
}

func (r *fakeRepository) ReadFinalTurns(_ context.Context, accountID string, _ []string) ([]recordsv1.FinalTurnSnapshot, error) {
	r.readAccountID = accountID
	return r.snapshots, nil
}

type fakeSessionOwners struct {
	ownerID string
	err     error
}

func (r fakeSessionOwners) AccountIDForSession(context.Context, string) (string, error) {
	return r.ownerID, r.err
}
