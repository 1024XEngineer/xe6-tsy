// Package turns owns durable final translation records and attribution corrections.
package turns

import (
	"context"
	"errors"
	"fmt"
	"time"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
)

var (
	ErrSessionNotFound     = errors.New("voice session not found")
	ErrTurnNotFound        = errors.New("voice turn not found")
	ErrParticipantNotFound = errors.New("participant not found")
	ErrForbidden           = errors.New("voice session belongs to another account")
	ErrInvalidRequest      = errors.New("invalid voice turn request")
	ErrInvalidAttribution  = errors.New("invalid voice turn attribution")
)

// Repository persists final turns. StoreFinalTurn must atomically deduplicate a FinalTurnEvent
// by event ID or turn ID, and CorrectAttribution must change only attribution fields.
type Repository interface {
	StoreFinalTurn(ctx context.Context, event recordsv1.FinalTurnEvent) error
	ListSession(ctx context.Context, sessionID string, query recordsv1.ListTurnsQuery) (recordsv1.VoiceTurnListResponse, error)
	Find(ctx context.Context, turnID string) (recordsv1.VoiceTurn, error)
	ListHistory(ctx context.Context, accountID string, query recordsv1.ListTurnsQuery) (recordsv1.VoiceTurnListResponse, error)
	ParticipantBelongsToSession(ctx context.Context, participantID, sessionID string) (bool, error)
	CorrectAttribution(ctx context.Context, update AttributionUpdate) (recordsv1.VoiceTurn, error)
	ReadFinalTurns(ctx context.Context, accountID string, turnIDs []string) ([]recordsv1.FinalTurnSnapshot, error)
}

// AttributionUpdate contains only the mutable part of a final turn. CorrectedBy and CorrectedAt
// are assigned by Service and cannot be supplied by a public HTTP client.
type AttributionUpdate struct {
	TurnID            string
	ParticipantID     string
	AttributionStatus recordsv1.AttributionStatus
	SpeakerConfidence *float64
	CorrectedBy       string
	CorrectedAt       time.Time
}

// Service implements the records module ports and public record operations. Final-turn producers
// are trusted internal callers; read and correction operations always enforce session ownership.
type Service struct {
	repository Repository
	sessions   recordsv1.SessionOwnerReader
	now        func() time.Time
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

// ConsumeFinalTurn stores one final realtime event. The repository owns the atomic event/turn
// deduplication transaction because at-least-once delivery can race across consumer instances.
func (s *Service) ConsumeFinalTurn(ctx context.Context, event recordsv1.FinalTurnEvent) error {
	if !validFinalTurnEvent(event) {
		return ErrInvalidRequest
	}
	return s.repository.StoreFinalTurn(ctx, event)
}

func (s *Service) ListSession(ctx context.Context, accountID, sessionID string, query recordsv1.ListTurnsQuery) (recordsv1.VoiceTurnListResponse, error) {
	if err := s.requireOwner(ctx, accountID, sessionID); err != nil {
		return recordsv1.VoiceTurnListResponse{}, err
	}
	query.SessionID = sessionID
	return s.repository.ListSession(ctx, sessionID, query)
}

func (s *Service) Get(ctx context.Context, accountID, turnID string) (recordsv1.VoiceTurn, error) {
	if accountID == "" || turnID == "" {
		return recordsv1.VoiceTurn{}, ErrInvalidRequest
	}
	turn, err := s.repository.Find(ctx, turnID)
	if err != nil {
		return recordsv1.VoiceTurn{}, err
	}
	if err := s.requireOwner(ctx, accountID, turn.SessionID); err != nil {
		return recordsv1.VoiceTurn{}, err
	}
	return turn, nil
}

func (s *Service) CorrectAttribution(ctx context.Context, accountID, turnID string, request recordsv1.UpdateAttributionRequest) (recordsv1.VoiceTurn, error) {
	if !validAttributionRequest(turnID, request) {
		return recordsv1.VoiceTurn{}, ErrInvalidAttribution
	}
	turn, err := s.Get(ctx, accountID, turnID)
	if err != nil {
		return recordsv1.VoiceTurn{}, err
	}
	belongs, err := s.repository.ParticipantBelongsToSession(ctx, request.ParticipantID, turn.SessionID)
	if err != nil {
		return recordsv1.VoiceTurn{}, err
	}
	if !belongs {
		return recordsv1.VoiceTurn{}, ErrInvalidAttribution
	}
	return s.repository.CorrectAttribution(ctx, AttributionUpdate{
		TurnID:            turnID,
		ParticipantID:     request.ParticipantID,
		AttributionStatus: request.AttributionStatus,
		SpeakerConfidence: request.SpeakerConfidence,
		CorrectedBy:       recordsv1.CorrectedBySystem,
		CorrectedAt:       s.now().UTC(),
	})
}

func (s *Service) ListHistory(ctx context.Context, accountID string, query recordsv1.ListTurnsQuery) (recordsv1.VoiceTurnListResponse, error) {
	if accountID == "" {
		return recordsv1.VoiceTurnListResponse{}, ErrInvalidRequest
	}
	if query.SessionID != "" {
		if err := s.requireOwner(ctx, accountID, query.SessionID); err != nil {
			return recordsv1.VoiceTurnListResponse{}, err
		}
	}
	return s.repository.ListHistory(ctx, accountID, query)
}

// ReadFinalTurns implements the account-scoped contract used by outbound message creation.
func (s *Service) ReadFinalTurns(ctx context.Context, accountID string, turnIDs []string) ([]recordsv1.FinalTurnSnapshot, error) {
	if accountID == "" {
		return nil, ErrInvalidRequest
	}
	return s.repository.ReadFinalTurns(ctx, accountID, turnIDs)
}

func (s *Service) requireOwner(ctx context.Context, accountID, sessionID string) error {
	if accountID == "" || sessionID == "" {
		return ErrInvalidRequest
	}
	ownerID, err := s.sessions.AccountIDForSession(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("read session owner: %w", err)
	}
	if ownerID != accountID {
		return ErrForbidden
	}
	return nil
}

func validFinalTurnEvent(event recordsv1.FinalTurnEvent) bool {
	if event.EventID == "" || event.TurnID == "" || event.SessionID == "" || event.SourceLanguage == "" || event.TargetLanguage == "" {
		return false
	}
	switch event.AttributionStatus {
	case recordsv1.AttributionPending, recordsv1.AttributionProvisional, recordsv1.AttributionConfirmed, recordsv1.AttributionCorrected:
	default:
		return false
	}
	return event.ParticipantID != nil || event.AttributionStatus == recordsv1.AttributionPending
}

func validAttributionRequest(turnID string, request recordsv1.UpdateAttributionRequest) bool {
	if turnID == "" || request.ParticipantID == "" {
		return false
	}
	return request.AttributionStatus == recordsv1.AttributionConfirmed || request.AttributionStatus == recordsv1.AttributionCorrected
}

var _ recordsv1.FinalTurnConsumer = (*Service)(nil)
var _ recordsv1.TurnReader = (*Service)(nil)
