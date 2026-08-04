package turns

import (
	"context"
	"errors"
	"testing"
	"time"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
)

func TestConsumeFinalTurnIsIdempotentAndPreservesEvent(t *testing.T) {
	repository := &fakeRepository{}
	service := NewService(repository, fakeSessionOwners{}, nil)
	confidence := 0.91
	event := validEvent()
	event.SequenceNo = 4
	event.LanguageConfigVersion = 8
	event.SourceText = "Hello"
	event.TranslatedText = "Ni hao"
	event.SpeakerConfidence = &confidence
	event.StartedAt = time.Date(2026, 7, 24, 8, 0, 0, 0, time.UTC)
	event.EndedAt = time.Date(2026, 7, 24, 8, 0, 2, 0, time.UTC)
	event.OccurredAt = time.Date(2026, 7, 24, 8, 0, 3, 0, time.UTC)

	if err := service.ConsumeFinalTurn(context.Background(), event); err != nil {
		t.Fatalf("first ConsumeFinalTurn() error = %v", err)
	}
	if err := service.ConsumeFinalTurn(context.Background(), event); err != nil {
		t.Fatalf("replay ConsumeFinalTurn() error = %v", err)
	}

	if got := len(repository.events); got != 1 {
		t.Fatalf("stored events = %d, want 1", got)
	}
	if got := repository.events[0]; got != event {
		t.Fatalf("stored event = %#v, want %#v", got, event)
	}
}

func TestConsumeFinalTurnSchedulesOnlyAfterStoreCommit(t *testing.T) {
	repository := &fakeRepository{}
	scheduler := &recordingFinalTurnScheduler{checkStored: func() bool { return len(repository.events) == 1 }}
	service := NewService(repository, fakeSessionOwners{ownerID: "acct_01"}, nil)
	service.SetFinalTurnScheduler(scheduler)

	if err := service.ConsumeFinalTurn(t.Context(), validEvent()); err != nil {
		t.Fatalf("ConsumeFinalTurn() error = %v", err)
	}
	if scheduler.accountID != "acct_01" || !scheduler.storedBeforeCall || scheduler.calls != 1 {
		t.Fatalf("scheduler = %#v", scheduler)
	}
}

func TestConsumeFinalTurnRejectsConflictingIdempotencyKeys(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*recordsv1.FinalTurnEvent)
	}{
		{
			name: "event ID",
			mutate: func(event *recordsv1.FinalTurnEvent) {
				event.TranslatedText = "different translation"
			},
		},
		{
			name: "turn ID",
			mutate: func(event *recordsv1.FinalTurnEvent) {
				event.EventID = "evt_02"
				event.TranslatedText = "different translation"
			},
		},
		{
			name: "session sequence number",
			mutate: func(event *recordsv1.FinalTurnEvent) {
				event.EventID = "evt_02"
				event.TurnID = "vt_02"
				event.TranslatedText = "different translation"
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := &fakeRepository{}
			service := NewService(repository, fakeSessionOwners{}, nil)
			event := validEvent()
			if err := service.ConsumeFinalTurn(context.Background(), event); err != nil {
				t.Fatalf("first ConsumeFinalTurn() error = %v", err)
			}
			test.mutate(&event)

			if err := service.ConsumeFinalTurn(context.Background(), event); !errors.Is(err, domain.ErrConflict) {
				t.Fatalf("conflicting ConsumeFinalTurn() error = %v, want conflict", err)
			}
		})
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

func TestConsumeFinalTurnRejectsResolvedAttributionWithoutParticipant(t *testing.T) {
	statuses := []recordsv1.AttributionStatus{
		recordsv1.AttributionProvisional,
		recordsv1.AttributionConfirmed,
		recordsv1.AttributionCorrected,
	}
	for _, status := range statuses {
		t.Run(string(status), func(t *testing.T) {
			service := NewService(&fakeRepository{}, fakeSessionOwners{}, nil)
			event := validEvent()
			event.ParticipantID = nil
			event.AttributionStatus = status

			if err := service.ConsumeFinalTurn(t.Context(), event); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("ConsumeFinalTurn() error = %v, want invalid request", err)
			}
		})
	}
}

func TestConsumeFinalTurnRejectsEmptyParticipantID(t *testing.T) {
	service := NewService(&fakeRepository{}, fakeSessionOwners{}, nil)
	event := validEvent()
	emptyParticipantID := ""
	event.ParticipantID = &emptyParticipantID

	if err := service.ConsumeFinalTurn(t.Context(), event); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("ConsumeFinalTurn() error = %v, want invalid request", err)
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

func TestConsumeFinalTurnRejectsEmptySpeakerCode(t *testing.T) {
	service := NewService(&fakeRepository{}, fakeSessionOwners{}, nil)
	event := validEvent()
	event.SpeakerCode = ""

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
	}, true)
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

func TestCorrectAttributionConfidenceThreeState(t *testing.T) {
	now := time.Date(2026, 7, 24, 9, 0, 0, 0, time.UTC)
	tests := []struct {
		name           string
		set            bool
		value          *float64
		wantSet        bool
		wantConfidence *float64
	}{
		{name: "absent preserves existing", set: false, wantSet: false, wantConfidence: nil},
		{name: "explicit null clears", set: true, wantSet: true, wantConfidence: nil},
		{name: "number updates", set: true, value: ptrFloat64(0.91), wantSet: true, wantConfidence: ptrFloat64(0.91)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := &fakeRepository{
				turn:                 recordsv1.VoiceTurn{ID: "vt_01", SessionID: "vs_01"},
				participantInSession: true,
			}
			service := NewService(repository, fakeSessionOwners{ownerID: "acct_01"}, func() time.Time { return now })

			_, err := service.CorrectAttribution(context.Background(), "acct_01", "vt_01", recordsv1.UpdateAttributionRequest{
				ParticipantID:     "p_01",
				AttributionStatus: recordsv1.AttributionConfirmed,
				SpeakerConfidence: test.value,
			}, test.set)
			if err != nil {
				t.Fatalf("CorrectAttribution() error = %v", err)
			}
			if repository.lastUpdate.SpeakerConfidenceSet != test.wantSet {
				t.Fatalf("SpeakerConfidenceSet = %v, want %v", repository.lastUpdate.SpeakerConfidenceSet, test.wantSet)
			}
			if !equalFloat(repository.lastUpdate.SpeakerConfidence, test.wantConfidence) {
				t.Fatalf("SpeakerConfidence = %v, want %v", repository.lastUpdate.SpeakerConfidence, test.wantConfidence)
			}
		})
	}
}

func TestCorrectAttributionIfUnresolvedSetsConditionalUpdate(t *testing.T) {
	repository := &fakeRepository{
		turn:                 recordsv1.VoiceTurn{ID: "vt_01", SessionID: "vs_01"},
		participantInSession: true,
	}
	service := NewService(repository, fakeSessionOwners{ownerID: "acct_01"}, nil)

	_, err := service.CorrectAttributionIfUnresolved(t.Context(), "acct_01", "vt_01", recordsv1.UpdateAttributionRequest{
		ParticipantID:     "p_01",
		AttributionStatus: recordsv1.AttributionConfirmed,
	}, false)
	if err != nil {
		t.Fatalf("CorrectAttributionIfUnresolved() error = %v", err)
	}
	if !repository.lastUpdate.OnlyIfUnresolved {
		t.Fatal("OnlyIfUnresolved = false, want true")
	}
}

func TestCorrectAttributionRejectsOutOfRangeConfidence(t *testing.T) {
	outOfRange := 1.5
	repository := &fakeRepository{turn: recordsv1.VoiceTurn{ID: "vt_01", SessionID: "vs_01"}, participantInSession: true}
	service := NewService(repository, fakeSessionOwners{ownerID: "acct_01"}, nil)

	_, err := service.CorrectAttribution(context.Background(), "acct_01", "vt_01", recordsv1.UpdateAttributionRequest{
		ParticipantID:     "p_01",
		AttributionStatus: recordsv1.AttributionConfirmed,
		SpeakerConfidence: &outOfRange,
	}, true)
	if !errors.Is(err, ErrInvalidAttribution) {
		t.Fatalf("CorrectAttribution() error = %v, want invalid attribution", err)
	}
}

func TestCorrectAttributionRejectsInvalidTarget(t *testing.T) {
	tests := []struct {
		name        string
		accountID   string
		request     recordsv1.UpdateAttributionRequest
		participant bool
		missingTurn bool
		wantErr     error
	}{
		{name: "invalid status", accountID: "acct_01", request: recordsv1.UpdateAttributionRequest{ParticipantID: "p_01", AttributionStatus: recordsv1.AttributionPending}, wantErr: ErrInvalidAttribution},
		{name: "participant belongs to another session", accountID: "acct_01", request: recordsv1.UpdateAttributionRequest{ParticipantID: "p_01", AttributionStatus: recordsv1.AttributionConfirmed}, wantErr: ErrInvalidAttribution},
		{name: "cross account", accountID: "acct_02", request: recordsv1.UpdateAttributionRequest{ParticipantID: "p_01", AttributionStatus: recordsv1.AttributionConfirmed}, participant: true, wantErr: ErrTurnNotFound},
		{name: "missing turn", accountID: "acct_01", request: recordsv1.UpdateAttributionRequest{ParticipantID: "p_01", AttributionStatus: recordsv1.AttributionConfirmed}, participant: true, missingTurn: true, wantErr: ErrTurnNotFound},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := &fakeRepository{ownedAccountID: "acct_01", turn: recordsv1.VoiceTurn{ID: "vt_01", SessionID: "vs_01"}, participantInSession: test.participant, missingTurn: test.missingTurn}
			service := NewService(repository, fakeSessionOwners{ownerID: "acct_01"}, nil)

			_, err := service.CorrectAttribution(context.Background(), test.accountID, "vt_01", test.request, false)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("CorrectAttribution() error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestGetAndListOperationsEnforceOwnership(t *testing.T) {
	repository := &fakeRepository{
		ownedAccountID:  "acct_01",
		turn:            recordsv1.VoiceTurn{ID: "vt_01", SessionID: "vs_01"},
		listResponse:    recordsv1.VoiceTurnListResponse{Items: []recordsv1.VoiceTurn{{ID: "vt_01"}}},
		historyResponse: recordsv1.VoiceTurnListResponse{Items: []recordsv1.VoiceTurn{{ID: "vt_01"}}},
	}
	service := NewService(repository, fakeSessionOwners{ownerID: "acct_01"}, nil)

	if _, err := service.Get(context.Background(), "acct_02", "vt_01"); !errors.Is(err, ErrTurnNotFound) {
		t.Fatalf("Get() error = %v, want not found", err)
	}
	if _, err := service.ListSession(context.Background(), "acct_02", "vs_01", recordsv1.ListTurnsQuery{}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("ListSession() error = %v, want forbidden", err)
	}
	if _, err := service.ListHistory(context.Background(), "acct_02", recordsv1.ListTurnsQuery{SessionID: "vs_01"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("ListHistory() error = %v, want forbidden", err)
	}

	if _, err := service.Get(context.Background(), "acct_01", "vt_01"); err != nil {
		t.Fatalf("owner Get() error = %v", err)
	}
	if repository.findAccountID != "acct_01" {
		t.Fatalf("Get() repository account = %q, want acct_01", repository.findAccountID)
	}
	if _, err := service.ListSession(context.Background(), "acct_01", "vs_01", recordsv1.ListTurnsQuery{}); err != nil {
		t.Fatalf("owner ListSession() error = %v", err)
	}
	if repository.listAccountID != "acct_01" {
		t.Fatalf("ListSession() repository account = %q, want acct_01", repository.listAccountID)
	}
}

func TestListSessionMapsStorageNotFound(t *testing.T) {
	service := NewService(&fakeRepository{}, fakeSessionOwners{err: domain.ErrNotFound}, nil)

	_, err := service.ListSession(t.Context(), "acct_01", "vs_missing", recordsv1.ListTurnsQuery{Limit: 20})
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("ListSession() error = %v, want %v", err, ErrSessionNotFound)
	}
}

func TestListHistoryRejectsReverseTimeRange(t *testing.T) {
	repository := &fakeRepository{}
	service := NewService(repository, fakeSessionOwners{}, nil)
	from := time.Date(2026, time.July, 28, 10, 0, 0, 0, time.UTC)
	to := from.Add(-time.Second)

	_, err := service.ListHistory(context.Background(), "acct_01", recordsv1.ListTurnsQuery{
		Limit:       20,
		CreatedFrom: &from,
		CreatedTo:   &to,
	})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("ListHistory() error = %v, want invalid request", err)
	}
}
func validEvent() recordsv1.FinalTurnEvent {
	participantID := "p_01"
	return recordsv1.FinalTurnEvent{
		EventVersion:          recordsv1.FinalTurnEventVersion,
		EventID:               "evt_01",
		TraceID:               "trace_01",
		TurnID:                "vt_01",
		SessionID:             "vs_01",
		ParticipantID:         &participantID,
		SequenceNo:            1,
		SourceLanguage:        "en-US",
		TargetLanguage:        "zh-CN",
		LanguageConfigVersion: 1,
		SourceText:            "source",
		TranslatedText:        "translation",
		SpeakerCode:           "speaker_01",
		AttributionStatus:     recordsv1.AttributionProvisional,
		StartedAt:             time.Date(2026, 7, 27, 8, 0, 0, 0, time.UTC),
		EndedAt:               time.Date(2026, 7, 27, 8, 0, 1, 0, time.UTC),
		OccurredAt:            time.Date(2026, 7, 27, 8, 0, 2, 0, time.UTC),
	}
}

type fakeRepository struct {
	events               []recordsv1.FinalTurnEvent
	turn                 recordsv1.VoiceTurn
	listResponse         recordsv1.VoiceTurnListResponse
	historyResponse      recordsv1.VoiceTurnListResponse
	participantInSession bool
	missingTurn          bool
	lastUpdate           AttributionUpdate
	snapshots            []recordsv1.FinalTurnSnapshot
	readAccountID        string
	readTurnIDs          []string
	findAccountID        string
	listAccountID        string
	ownedAccountID       string
	readErr              error
	readCalls            int
	mutateReadTurnIDs    bool
}

func (r *fakeRepository) StoreFinalTurn(_ context.Context, event recordsv1.FinalTurnEvent) error {
	hash, err := recordsv1.FinalTurnEventPayloadHash(event)
	if err != nil {
		return err
	}
	duplicate := false
	for _, stored := range r.events {
		if stored.EventID != event.EventID && stored.TurnID != event.TurnID && (stored.SessionID != event.SessionID || stored.SequenceNo != event.SequenceNo) {
			continue
		}
		duplicate = true
		storedHash, err := recordsv1.FinalTurnEventPayloadHash(stored)
		if err != nil {
			return err
		}
		if storedHash != hash {
			return domain.ErrConflict
		}
	}
	if duplicate {
		return nil
	}
	r.events = append(r.events, event)
	return nil
}

func (r *fakeRepository) ListSession(_ context.Context, accountID, _ string, _ recordsv1.ListTurnsQuery) (recordsv1.VoiceTurnListResponse, error) {
	r.listAccountID = accountID
	return r.listResponse, nil
}

func (r *fakeRepository) Find(_ context.Context, accountID, _ string) (recordsv1.VoiceTurn, error) {
	r.findAccountID = accountID
	if r.ownedAccountID != "" && accountID != r.ownedAccountID {
		return recordsv1.VoiceTurn{}, ErrTurnNotFound
	}
	return r.turn, nil
}

func (r *fakeRepository) ListHistory(context.Context, string, recordsv1.ListTurnsQuery) (recordsv1.VoiceTurnListResponse, error) {
	return r.historyResponse, nil
}

func (r *fakeRepository) CorrectAttribution(_ context.Context, update AttributionUpdate) (recordsv1.VoiceTurn, error) {
	if r.missingTurn {
		return recordsv1.VoiceTurn{}, ErrTurnNotFound
	}
	if r.ownedAccountID != "" && update.AccountID != r.ownedAccountID {
		return recordsv1.VoiceTurn{}, ErrTurnNotFound
	}
	if !r.participantInSession {
		return recordsv1.VoiceTurn{}, ErrInvalidAttribution
	}
	r.lastUpdate = update
	updated := r.turn
	updated.ParticipantID = &update.ParticipantID
	updated.AttributionStatus = update.AttributionStatus
	if update.SpeakerConfidenceSet {
		updated.SpeakerConfidence = update.SpeakerConfidence
	}
	updated.CorrectedBy = &update.CorrectedBy
	updated.CorrectedAt = &update.CorrectedAt
	return updated, nil
}

func (r *fakeRepository) ReadFinalTurns(_ context.Context, accountID string, turnIDs []string) ([]recordsv1.FinalTurnSnapshot, error) {
	r.readCalls++
	r.readAccountID = accountID
	r.readTurnIDs = append([]string(nil), turnIDs...)
	if r.mutateReadTurnIDs && len(turnIDs) > 1 {
		turnIDs[0], turnIDs[len(turnIDs)-1] = turnIDs[len(turnIDs)-1], turnIDs[0]
	}
	return r.snapshots, r.readErr
}

type fakeSessionOwners struct {
	ownerID string
	err     error
}

type recordingFinalTurnScheduler struct {
	accountID        string
	calls            int
	storedBeforeCall bool
	checkStored      func() bool
}

func (s *recordingFinalTurnScheduler) ScheduleFinalTurn(_ context.Context, accountID string, _ recordsv1.FinalTurnEvent) error {
	s.accountID = accountID
	s.calls++
	s.storedBeforeCall = s.checkStored()
	return nil
}

func (r fakeSessionOwners) AccountIDForSession(context.Context, string) (string, error) {
	return r.ownerID, r.err
}

func ptrFloat64(value float64) *float64 {
	return &value
}

func equalFloat(a, b *float64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
