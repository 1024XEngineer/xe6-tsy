package participants

import (
	"context"
	"errors"
	"testing"
	"time"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
)

func TestListChecksSessionOwnership(t *testing.T) {
	tests := []struct {
		name      string
		accountID string
		ownerID   string
		ownerErr  error
		wantErr   error
	}{
		{name: "owner", accountID: "acct_01", ownerID: "acct_01"},
		{name: "another account", accountID: "acct_02", ownerID: "acct_01", wantErr: ErrForbidden},
		{name: "missing session", accountID: "acct_01", ownerErr: ErrSessionNotFound, wantErr: ErrSessionNotFound},
		{name: "storage missing session", accountID: "acct_01", ownerErr: domain.ErrNotFound, wantErr: ErrSessionNotFound},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := &fakeRepository{listResponse: recordsv1.ParticipantListResponse{Items: []recordsv1.Participant{{ID: "p_01"}}}}
			service := NewService(repository, fakeSessionOwners{ownerID: test.ownerID, err: test.ownerErr}, nil)

			response, err := service.List(context.Background(), test.accountID, "vs_01", recordsv1.ListParticipantsQuery{Limit: 20})
			if test.wantErr != nil {
				if !errors.Is(err, test.wantErr) {
					t.Fatalf("List() error = %v, want %v", err, test.wantErr)
				}
				if repository.listCalls != 0 {
					t.Fatalf("List() repository calls = %d, want 0", repository.listCalls)
				}
				return
			}
			if err != nil {
				t.Fatalf("List() error = %v", err)
			}
			if len(response.Items) != 1 || response.Items[0].ID != "p_01" {
				t.Fatalf("List() response = %#v", response)
			}
			if repository.listAccountID != test.accountID {
				t.Fatalf("List() repository account = %q, want %q", repository.listAccountID, test.accountID)
			}
		})
	}
}

func TestUpdatePassesExplicitClearWithoutTouchingTurns(t *testing.T) {
	now := time.Date(2026, 7, 24, 9, 0, 0, 0, time.UTC)
	repository := &fakeRepository{updated: recordsv1.Participant{ID: "p_01", SessionID: "vs_01"}}
	service := NewService(repository, fakeSessionOwners{ownerID: "acct_01"}, func() time.Time { return now })

	_, err := service.Update(context.Background(), "acct_01", "vs_01", "p_01", Update{
		VoiceProfileIDSet: true,
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if !repository.update.VoiceProfileIDSet || repository.update.VoiceProfileID != nil {
		t.Fatalf("Update() did not preserve explicit null: %#v", repository.update)
	}
	if !repository.update.UpdatedAt.Equal(now) {
		t.Fatalf("Update() updated_at = %v, want %v", repository.update.UpdatedAt, now)
	}
	if repository.turnMutations != 0 {
		t.Fatalf("Update() changed %d turns, want 0", repository.turnMutations)
	}
}

func TestUpdateDoesNotCallRepositoryForAnotherAccount(t *testing.T) {
	repository := &fakeRepository{}
	service := NewService(repository, fakeSessionOwners{ownerID: "acct_owner"}, nil)

	_, err := service.Update(context.Background(), "acct_other", "vs_01", "p_01", Update{DisplayNameSet: true})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("Update() error = %v, want ErrForbidden", err)
	}
	if repository.updateCalls != 0 {
		t.Fatalf("Update() repository calls = %d, want 0", repository.updateCalls)
	}
}

func TestUpdateReportsTheMissingBodyField(t *testing.T) {
	repository := &fakeRepository{}
	service := NewService(repository, fakeSessionOwners{ownerID: "acct_01"}, nil)

	_, err := service.Update(context.Background(), "acct_01", "vs_01", "p_01", Update{})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("Update() error = %v, want ErrInvalidRequest", err)
	}
	if got := domain.FieldName(err); got != "body" {
		t.Fatalf("FieldName() = %q, want body", got)
	}
	if repository.updateCalls != 0 {
		t.Fatalf("Update() repository calls = %d, want 0", repository.updateCalls)
	}
}

func TestUpdateReportsTheMissingParticipantField(t *testing.T) {
	service := NewService(&fakeRepository{}, fakeSessionOwners{ownerID: "acct_01"}, nil)
	_, err := service.Update(context.Background(), "acct_01", "vs_01", "", Update{DisplayNameSet: true})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("Update() error = %v, want ErrInvalidRequest", err)
	}
	if got := domain.FieldName(err); got != "participant_id" {
		t.Fatalf("FieldName() = %q, want participant_id", got)
	}
}

func TestResolveProviderMappingRequiresOwnerAndEvidence(t *testing.T) {
	tests := []struct {
		name        string
		accountID   string
		ownerID     string
		ownerErr    error
		observation recordsv1.SpeakerObservation
		wantErr     error
	}{
		{
			name:        "owner with provider key",
			accountID:   "acct_01",
			ownerID:     "acct_01",
			observation: recordsv1.SpeakerObservation{SessionID: "vs_01", TurnID: "vt_01", ProviderSpeakerID: "diar_01"},
		},
		{
			name:        "another account forbidden",
			accountID:   "acct_02",
			ownerID:     "acct_01",
			observation: recordsv1.SpeakerObservation{SessionID: "vs_01", TurnID: "vt_01", ProviderSpeakerID: "diar_01"},
			wantErr:     ErrForbidden,
		},
		{
			name:        "missing provider key invalid",
			accountID:   "acct_01",
			ownerID:     "acct_01",
			observation: recordsv1.SpeakerObservation{SessionID: "vs_01", TurnID: "vt_01"},
			wantErr:     ErrInvalidRequest,
		},
		{
			name:        "missing session invalid",
			accountID:   "acct_01",
			ownerID:     "acct_01",
			observation: recordsv1.SpeakerObservation{TurnID: "vt_01", ProviderSpeakerID: "diar_01"},
			wantErr:     ErrInvalidRequest,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := &fakeRepository{}
			service := NewService(repository, fakeSessionOwners{ownerID: test.ownerID, err: test.ownerErr}, nil)

			participant, err := service.ResolveProviderMapping(context.Background(), test.accountID, test.observation)
			if test.wantErr != nil {
				if !errors.Is(err, test.wantErr) {
					t.Fatalf("ResolveProviderMapping() error = %v, want %v", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveProviderMapping() error = %v", err)
			}
			if participant.ID == "" || participant.SpeakerCode == "" {
				t.Fatalf("ResolveProviderMapping() participant = %#v", participant)
			}
		})
	}
}

type fakeRepository struct {
	listResponse  recordsv1.ParticipantListResponse
	updated       recordsv1.Participant
	update        Update
	turnMutations int
	participants  map[string]recordsv1.Participant
	listAccountID string
	listCalls     int
	updateCalls   int
}

func (r *fakeRepository) List(_ context.Context, accountID, _ string, _ recordsv1.ListParticipantsQuery) (recordsv1.ParticipantListResponse, error) {
	r.listCalls++
	r.listAccountID = accountID
	return r.listResponse, nil
}

func (r *fakeRepository) Update(_ context.Context, _ string, _ string, update Update) (recordsv1.Participant, error) {
	r.updateCalls++
	r.update = update
	return r.updated, nil
}

func (r *fakeRepository) FindOrCreate(_ context.Context, observation recordsv1.SpeakerObservation) (recordsv1.Participant, error) {
	if r.participants == nil {
		r.participants = make(map[string]recordsv1.Participant)
	}
	key := observation.SessionID + "/" + observation.ProviderSpeakerID
	if participant, ok := r.participants[key]; ok {
		return participant, nil
	}
	participant := recordsv1.Participant{ID: "p_01", SessionID: observation.SessionID, SpeakerCode: "speaker_01"}
	r.participants[key] = participant
	return participant, nil
}

type fakeSessionOwners struct {
	ownerID string
	err     error
}

func (r fakeSessionOwners) AccountIDForSession(context.Context, string) (string, error) {
	return r.ownerID, r.err
}
