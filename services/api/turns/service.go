// Package turns owns durable final translation records and attribution corrections.
package turns

import (
	"context"
	"errors"
	"fmt"
	"time"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
)

var (
	ErrSessionNotFound     = errors.New("voice session not found")
	ErrTurnNotFound        = errors.New("voice turn not found")
	ErrParticipantNotFound = errors.New("participant not found")
	ErrForbidden           = errors.New("voice session belongs to another account")
	ErrInvalidRequest      = errors.New("invalid voice turn request")
	ErrInvalidAttribution  = errors.New("invalid voice turn attribution")
)

// Repository persists final turns. StoreFinalTurn must atomically deduplicate a FinalTurnEvent by
// event ID, turn ID, or session and sequence number. It accepts a duplicate only when
// recordsv1.FinalTurnEventPayloadHash matches the stored value; otherwise it returns a conflict.
// CorrectAttribution must change only attribution fields.
type Repository interface {
	StoreFinalTurn(ctx context.Context, event recordsv1.FinalTurnEvent) error
	ListSession(ctx context.Context, accountID, sessionID string, query recordsv1.ListTurnsQuery) (recordsv1.VoiceTurnListResponse, error)
	Find(ctx context.Context, accountID, turnID string) (recordsv1.VoiceTurn, error)
	ListHistory(ctx context.Context, accountID string, query recordsv1.ListTurnsQuery) (recordsv1.VoiceTurnListResponse, error)
	CorrectAttribution(ctx context.Context, update AttributionUpdate) (recordsv1.VoiceTurn, error)
}

// AttributionUpdate contains only the mutable part of a final turn. CorrectedBy and CorrectedAt
// are assigned by Service and cannot be supplied by a public HTTP client. AccountID authorizes
// the correction inside the repository write transaction so ownership and the write share one
// atomic boundary. SpeakerConfidenceSet distinguishes an absent field from an explicit null.
type AttributionUpdate struct {
	AccountID            string
	TurnID               string
	ParticipantID        string
	AttributionStatus    recordsv1.AttributionStatus
	SpeakerConfidence    *float64
	SpeakerConfidenceSet bool
	CorrectedBy          string
	CorrectedAt          time.Time
}

// Service implements the records module ports and public record operations. Final-turn producers
// are trusted internal callers; read and correction operations always enforce session ownership.
type Service struct {
	repository Repository
	sessions   recordsv1.SessionOwnerReader
	scheduler  FinalTurnScheduler
	now        func() time.Time
}

// FinalTurnScheduler is invoked only after StoreFinalTurn succeeds. It owns
// the asynchronous message/outbox side effect and must remain idempotent.
type FinalTurnScheduler interface {
	ScheduleFinalTurn(context.Context, string, recordsv1.FinalTurnEvent) error
}

func NewService(repository Repository, sessions recordsv1.SessionOwnerReader, now func() time.Time) *Service {
	if repository == nil {
		panic("turns repository is required")
	}
	if sessions == nil {
		panic("turns session owner reader is required")
	}
	if now == nil {
		now = time.Now
	}
	return &Service{repository: repository, sessions: sessions, now: now}
}

// SetFinalTurnScheduler wires the optional post-commit delivery hook. Keeping
// it optional preserves the records-only runtime used by local tests and
// deployments where outbound delivery is disabled.
func (s *Service) SetFinalTurnScheduler(scheduler FinalTurnScheduler) {
	if s != nil {
		s.scheduler = scheduler
	}
}

// ConsumeFinalTurn stores an immutable translation fact from the media plane.
// The service validates attribution semantics before storage; the repository
// owns atomic event/turn deduplication because at-least-once delivery can race
// across consumers. Identical payload replays are accepted, while later
// corrections may update attribution only, never the text.
func (s *Service) ConsumeFinalTurn(ctx context.Context, event recordsv1.FinalTurnEvent) error {
	if err := event.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	// nil explicitly means attribution is pending. A non-nil empty string has no
	// protocol meaning, so reject it instead of silently normalizing it.
	if event.ParticipantID != nil && *event.ParticipantID == "" {
		return fmt.Errorf("%w: participant_id cannot be empty", ErrInvalidRequest)
	}
	// A resolved attribution without a participant would make later corrections
	// ambiguous. If realtime cannot identify a speaker within its latency budget,
	// pending is the only valid state.
	if event.ParticipantID == nil && event.AttributionStatus != recordsv1.AttributionPending {
		return fmt.Errorf("%w: participant_id is required for resolved attribution", ErrInvalidRequest)
	}
	if err := s.repository.StoreFinalTurn(ctx, event); err != nil {
		return err
	}
	if s.scheduler == nil {
		return nil
	}
	accountID, err := s.sessions.AccountIDForSession(ctx, event.SessionID)
	if err != nil {
		return fmt.Errorf("resolve final turn account: %w", err)
	}
	if err := s.scheduler.ScheduleFinalTurn(ctx, accountID, event); err != nil {
		return fmt.Errorf("schedule final turn delivery: %w", err)
	}
	return nil
}

func (s *Service) ListSession(ctx context.Context, accountID, sessionID string, query recordsv1.ListTurnsQuery) (recordsv1.VoiceTurnListResponse, error) {
	if err := s.requireOwner(ctx, accountID, sessionID); err != nil {
		return recordsv1.VoiceTurnListResponse{}, err
	}
	query.SessionID = sessionID
	return s.repository.ListSession(ctx, accountID, sessionID, query)
}

func (s *Service) Get(ctx context.Context, accountID, turnID string) (recordsv1.VoiceTurn, error) {
	if accountID == "" || turnID == "" {
		return recordsv1.VoiceTurn{}, ErrInvalidRequest
	}
	turn, err := s.repository.Find(ctx, accountID, turnID)
	if err != nil {
		return recordsv1.VoiceTurn{}, err
	}
	return turn, nil
}

func (s *Service) CorrectAttribution(ctx context.Context, accountID, turnID string, request recordsv1.UpdateAttributionRequest, speakerConfidenceSet bool) (recordsv1.VoiceTurn, error) {
	if !validAttributionRequest(turnID, request) {
		return recordsv1.VoiceTurn{}, ErrInvalidAttribution
	}
	if request.SpeakerConfidence != nil && (*request.SpeakerConfidence < 0 || *request.SpeakerConfidence > 1) {
		return recordsv1.VoiceTurn{}, ErrInvalidAttribution
	}
	// Account ownership is enforced inside the repository write transaction so the
	// authorization check and the attribution update share one atomic boundary.
	return s.repository.CorrectAttribution(ctx, AttributionUpdate{
		AccountID:            accountID,
		TurnID:               turnID,
		ParticipantID:        request.ParticipantID,
		AttributionStatus:    request.AttributionStatus,
		SpeakerConfidence:    request.SpeakerConfidence,
		SpeakerConfidenceSet: speakerConfidenceSet,
		CorrectedBy:          recordsv1.CorrectedBySystem,
		CorrectedAt:          s.now().UTC(),
	})
}

func (s *Service) ListHistory(ctx context.Context, accountID string, query recordsv1.ListTurnsQuery) (recordsv1.VoiceTurnListResponse, error) {
	if accountID == "" || (query.CreatedFrom != nil && query.CreatedTo != nil && query.CreatedFrom.After(*query.CreatedTo)) {
		return recordsv1.VoiceTurnListResponse{}, ErrInvalidRequest
	}
	if query.SessionID != "" {
		if err := s.requireOwner(ctx, accountID, query.SessionID); err != nil {
			return recordsv1.VoiceTurnListResponse{}, err
		}
	}
	return s.repository.ListHistory(ctx, accountID, query)
}

func (s *Service) requireOwner(ctx context.Context, accountID, sessionID string) error {
	if accountID == "" || sessionID == "" {
		return ErrInvalidRequest
	}
	ownerID, err := s.sessions.AccountIDForSession(ctx, sessionID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return ErrSessionNotFound
		}
		return fmt.Errorf("read session owner: %w", err)
	}
	if ownerID != accountID {
		return ErrForbidden
	}
	return nil
}

func validAttributionRequest(turnID string, request recordsv1.UpdateAttributionRequest) bool {
	if turnID == "" || request.ParticipantID == "" {
		return false
	}
	return request.AttributionStatus == recordsv1.AttributionConfirmed || request.AttributionStatus == recordsv1.AttributionCorrected
}

var _ recordsv1.FinalTurnConsumer = (*Service)(nil)
