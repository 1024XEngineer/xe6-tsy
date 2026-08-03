package turns

import (
	"context"
	"errors"
	"fmt"
	"strings"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
)

// ErrAttributionNoEvidence marks a task that can never be resolved because the persisted turn
// carries no provider speaker key. The worker fails the task instead of retrying or acking it.
var ErrAttributionNoEvidence = errors.New("attribution requires provider speaker evidence")

// ProviderAttributionResolver maps a persisted provider speaker key to the session's stable
// participant. It never fabricates an identity: without a key it returns ErrAttributionNoEvidence,
// and a turn already confirmed or corrected is never overwritten by a stale async decision.
type ProviderAttributionResolver struct {
	participants interface {
		ResolveProviderMapping(ctx context.Context, accountID string, observation recordsv1.SpeakerObservation) (recordsv1.Participant, error)
	}
}

// NewProviderAttributionResolver binds the account-scoped participant service as the mapping
// boundary. The participant service must enforce session ownership for every mapping.
func NewProviderAttributionResolver(participants interface {
	ResolveProviderMapping(ctx context.Context, accountID string, observation recordsv1.SpeakerObservation) (recordsv1.Participant, error)
}) AttributionResolver {
	return &ProviderAttributionResolver{participants: participants}
}

// Resolve returns the stable participant mapping for the turn's provider key. A finalized turn
// yields a nil decision so the worker acks it without changing the record.
func (r *ProviderAttributionResolver) Resolve(ctx context.Context, input AttributionResolutionInput) (*AttributionDecision, error) {
	if input.Turn.ProviderSpeakerID == nil || strings.TrimSpace(*input.Turn.ProviderSpeakerID) == "" {
		return nil, fmt.Errorf("%w: turn %s", ErrAttributionNoEvidence, input.TurnID)
	}
	switch input.Turn.AttributionStatus {
	case recordsv1.AttributionConfirmed, recordsv1.AttributionCorrected:
		return nil, nil
	}
	participant, err := r.participants.ResolveProviderMapping(ctx, input.AccountID, recordsv1.SpeakerObservation{
		SessionID:         input.SessionID,
		TurnID:            input.TurnID,
		ProviderSpeakerID: *input.Turn.ProviderSpeakerID,
	})
	if err != nil {
		return nil, fmt.Errorf("resolve provider participant for turn %s: %w", input.TurnID, err)
	}
	status := recordsv1.AttributionConfirmed
	if input.Turn.AttributionStatus == recordsv1.AttributionProvisional &&
		input.Turn.ParticipantID != nil && *input.Turn.ParticipantID != participant.ID {
		status = recordsv1.AttributionCorrected
	}
	return &AttributionDecision{
		ParticipantID:     participant.ID,
		AttributionStatus: status,
	}, nil
}

// SingleDecisionResolver returns one fixed decision and is used by tests to exercise the worker.
type SingleDecisionResolver struct {
	Decision *AttributionDecision
	Err      error
}

// Resolve returns the fixed decision or error.
func (r *SingleDecisionResolver) Resolve(context.Context, AttributionResolutionInput) (*AttributionDecision, error) {
	return r.Decision, r.Err
}

// ServiceAttributionReader adapts the records services to the turn/participant reads the
// attribution worker needs. Both reads enforce account ownership through the service boundary.
type ServiceAttributionReader struct {
	turns        *Service
	participants interface {
		List(ctx context.Context, accountID, sessionID string, query recordsv1.ListParticipantsQuery) (recordsv1.ParticipantListResponse, error)
	}
}

// NewServiceAttributionReader binds the records services as the worker's read boundary.
func NewServiceAttributionReader(turnsService *Service, participantService interface {
	List(ctx context.Context, accountID, sessionID string, query recordsv1.ListParticipantsQuery) (recordsv1.ParticipantListResponse, error)
}) *ServiceAttributionReader {
	return &ServiceAttributionReader{turns: turnsService, participants: participantService}
}

// GetTurn returns one turn owned by the account.
func (r *ServiceAttributionReader) GetTurn(ctx context.Context, accountID, turnID string) (recordsv1.VoiceTurn, error) {
	return r.turns.Get(ctx, accountID, turnID)
}

// ListParticipants returns the session participants owned by the account.
func (r *ServiceAttributionReader) ListParticipants(ctx context.Context, accountID, sessionID string) ([]recordsv1.Participant, error) {
	response, err := r.participants.List(ctx, accountID, sessionID, recordsv1.ListParticipantsQuery{Limit: 100})
	if err != nil {
		return nil, err
	}
	return response.Items, nil
}

var (
	_ AttributionReader  = (*ServiceAttributionReader)(nil)
	_ AttributionApplier = (*Service)(nil)
)
