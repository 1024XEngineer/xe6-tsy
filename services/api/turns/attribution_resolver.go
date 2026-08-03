package turns

import (
	"context"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
)

// DefaultAttributionResolver is the production no-op resolver: without a real speaker-resolution
// provider it never fabricates attribution, so a pending or provisional turn stays untouched and
// its task completes without modifying the record. Replace it with a real AI resolver when one
// exists.
type DefaultAttributionResolver struct{}

// NewDefaultAttributionResolver returns the safe fallback resolver.
func NewDefaultAttributionResolver() AttributionResolver {
	return DefaultAttributionResolver{}
}

func (DefaultAttributionResolver) Resolve(context.Context, AttributionResolutionInput) (*AttributionDecision, error) {
	return nil, nil
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
	_ AttributionReader = (*ServiceAttributionReader)(nil)
	_ AttributionApplier = (*Service)(nil)
)
