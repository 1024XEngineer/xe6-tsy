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
