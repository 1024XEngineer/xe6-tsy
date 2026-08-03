package turns

import (
	"context"
	"errors"
	"testing"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
)

func TestProviderAttributionResolverMapsProviderKey(t *testing.T) {
	resolver := NewProviderAttributionResolver(&participantMapperStub{participant: recordsv1.Participant{ID: "p_01", SpeakerCode: "speaker_01"}})

	decision, err := resolver.Resolve(context.Background(), AttributionResolutionInput{
		AccountID: "acct_01", SessionID: "vs_01", TurnID: "vt_01",
		Turn: recordsv1.VoiceTurn{
			ID: "vt_01", SessionID: "vs_01", AttributionStatus: recordsv1.AttributionPending,
			ProviderSpeakerID: strPtr("diar_01"),
		},
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if decision == nil || decision.ParticipantID != "p_01" || decision.AttributionStatus != recordsv1.AttributionConfirmed {
		t.Fatalf("decision = %#v", decision)
	}
}

func TestProviderAttributionResolverCorrectsDifferentParticipant(t *testing.T) {
	resolver := NewProviderAttributionResolver(&participantMapperStub{participant: recordsv1.Participant{ID: "p_02", SpeakerCode: "speaker_02"}})

	decision, err := resolver.Resolve(context.Background(), AttributionResolutionInput{
		AccountID: "acct_01", SessionID: "vs_01", TurnID: "vt_01",
		Turn: recordsv1.VoiceTurn{
			ID: "vt_01", SessionID: "vs_01", AttributionStatus: recordsv1.AttributionProvisional,
			ParticipantID: strPtr("p_01"), ProviderSpeakerID: strPtr("diar_02"),
		},
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if decision == nil || decision.AttributionStatus != recordsv1.AttributionCorrected {
		t.Fatalf("decision = %#v, want corrected", decision)
	}
}

func TestProviderAttributionResolverKeepsFinalizedTurn(t *testing.T) {
	resolver := NewProviderAttributionResolver(&participantMapperStub{participant: recordsv1.Participant{ID: "p_02"}})

	for _, status := range []recordsv1.AttributionStatus{recordsv1.AttributionConfirmed, recordsv1.AttributionCorrected} {
		decision, err := resolver.Resolve(context.Background(), AttributionResolutionInput{
			AccountID: "acct_01", SessionID: "vs_01", TurnID: "vt_01",
			Turn: recordsv1.VoiceTurn{
				ID: "vt_01", SessionID: "vs_01", AttributionStatus: status,
				ParticipantID: strPtr("p_01"), ProviderSpeakerID: strPtr("diar_02"),
			},
		})
		if err != nil {
			t.Fatalf("Resolve(%q) error = %v", status, err)
		}
		if decision != nil {
			t.Fatalf("Resolve(%q) decision = %#v, want nil to keep final attribution", status, decision)
		}
	}
}

func TestProviderAttributionResolverRequiresEvidence(t *testing.T) {
	resolver := NewProviderAttributionResolver(&participantMapperStub{participant: recordsv1.Participant{ID: "p_01"}})

	decision, err := resolver.Resolve(context.Background(), AttributionResolutionInput{
		AccountID: "acct_01", SessionID: "vs_01", TurnID: "vt_01",
		Turn: recordsv1.VoiceTurn{ID: "vt_01", SessionID: "vs_01", AttributionStatus: recordsv1.AttributionPending},
	})
	if !errors.Is(err, ErrAttributionNoEvidence) {
		t.Fatalf("Resolve() error = %v, want ErrAttributionNoEvidence", err)
	}
	if decision != nil {
		t.Fatalf("decision = %#v, want nil", decision)
	}
}

func TestProviderAttributionResolverPropagatesMappingError(t *testing.T) {
	resolver := NewProviderAttributionResolver(&participantMapperStub{err: errors.New("mapping failed")})

	_, err := resolver.Resolve(context.Background(), AttributionResolutionInput{
		AccountID: "acct_01", SessionID: "vs_01", TurnID: "vt_01",
		Turn: recordsv1.VoiceTurn{
			ID: "vt_01", SessionID: "vs_01", AttributionStatus: recordsv1.AttributionPending,
			ProviderSpeakerID: strPtr("diar_01"),
		},
	})
	if err == nil {
		t.Fatal("Resolve() error = nil, want mapping error")
	}
}

func TestServiceAttributionReaderGetTurn(t *testing.T) {
	repository := &fakeRepository{turn: recordsv1.VoiceTurn{ID: "vt_01", SessionID: "vs_01"}}
	service := NewService(repository, fakeSessionOwners{}, nil)
	reader := NewServiceAttributionReader(service, participantListerStub{})

	turn, err := reader.GetTurn(context.Background(), "acct_01", "vt_01")
	if err != nil {
		t.Fatalf("GetTurn() error = %v", err)
	}
	if turn.ID != "vt_01" || turn.SessionID != "vs_01" {
		t.Fatalf("GetTurn() = %#v", turn)
	}
	if repository.findAccountID != "acct_01" {
		t.Fatalf("GetTurn() account = %q, want acct_01", repository.findAccountID)
	}
}

func TestServiceAttributionReaderGetTurnPropagatesError(t *testing.T) {
	repository := &fakeRepository{ownedAccountID: "acct_01"}
	service := NewService(repository, fakeSessionOwners{}, nil)
	reader := NewServiceAttributionReader(service, participantListerStub{})

	if _, err := reader.GetTurn(context.Background(), "acct_02", "vt_01"); !errors.Is(err, ErrTurnNotFound) {
		t.Fatalf("GetTurn() error = %v, want ErrTurnNotFound", err)
	}
}

func TestServiceAttributionReaderListParticipants(t *testing.T) {
	service := NewService(&fakeRepository{}, fakeSessionOwners{}, nil)
	reader := NewServiceAttributionReader(service, participantListerStub{
		response: recordsv1.ParticipantListResponse{
			Items: []recordsv1.Participant{{ID: "p_01", SpeakerCode: "speaker_01"}},
		},
	})

	participants, err := reader.ListParticipants(context.Background(), "acct_01", "vs_01")
	if err != nil {
		t.Fatalf("ListParticipants() error = %v", err)
	}
	if len(participants) != 1 || participants[0].ID != "p_01" {
		t.Fatalf("ListParticipants() = %#v", participants)
	}
}

func TestServiceAttributionReaderListParticipantsPropagatesError(t *testing.T) {
	service := NewService(&fakeRepository{}, fakeSessionOwners{}, nil)
	reader := NewServiceAttributionReader(service, participantListerStub{err: errors.New("list failed")})

	if _, err := reader.ListParticipants(context.Background(), "acct_01", "vs_01"); err == nil {
		t.Fatal("ListParticipants() error = nil, want list error")
	}
}

type participantListerStub struct {
	response recordsv1.ParticipantListResponse
	err      error
}

func (s participantListerStub) List(context.Context, string, string, recordsv1.ListParticipantsQuery) (recordsv1.ParticipantListResponse, error) {
	return s.response, s.err
}

type participantMapperStub struct {
	participant recordsv1.Participant
	err         error
}

func (s *participantMapperStub) ResolveProviderMapping(_ context.Context, _ string, _ recordsv1.SpeakerObservation) (recordsv1.Participant, error) {
	return s.participant, s.err
}

func strPtr(value string) *string {
	return &value
}
