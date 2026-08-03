// Package participants owns session-scoped temporary speaker attribution and mapping updates.
package participants

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
	ErrParticipantNotFound = errors.New("participant not found")
	ErrForbidden           = errors.New("voice session belongs to another account")
	ErrInvalidRequest      = errors.New("invalid participant request")
	ErrConflict            = errors.New("participant mapping conflicts with existing data")
)

// Repository persists session participants. Implementations must reject a participant ID that
// does not belong to the supplied session and must not mutate voice turns when mapping changes.
type Repository interface {
	List(ctx context.Context, accountID, sessionID string, query recordsv1.ListParticipantsQuery) (recordsv1.ParticipantListResponse, error)
	Update(ctx context.Context, sessionID, participantID string, update Update) (recordsv1.Participant, error)
	FindOrCreate(ctx context.Context, observation recordsv1.SpeakerObservation) (recordsv1.Participant, error)
}

// Update records which optional fields were present in a system mapping request. A nil Value with
// its Set flag true means the request explicitly cleared that nullable field.
type Update struct {
	DisplayName          *string
	DisplayNameSet       bool
	ProviderSpeakerID    *string
	ProviderSpeakerIDSet bool
	VoiceProfileID       *string
	VoiceProfileIDSet    bool
	UpdatedAt            time.Time
}

func (u Update) Empty() bool {
	return !u.DisplayNameSet && !u.ProviderSpeakerIDSet && !u.VoiceProfileIDSet
}

// Service enforces account ownership for public operations while keeping realtime attribution
// independent of account authentication. Realtime callers are trusted internal producers.
type Service struct {
	repository Repository
	sessions   recordsv1.SessionOwnerReader
	now        func() time.Time
}

func NewService(repository Repository, sessions recordsv1.SessionOwnerReader, now func() time.Time) *Service {
	if repository == nil {
		panic("participants repository is required")
	}
	if sessions == nil {
		panic("participants session owner reader is required")
	}
	if now == nil {
		now = time.Now
	}
	return &Service{repository: repository, sessions: sessions, now: now}
}

func (s *Service) List(ctx context.Context, accountID, sessionID string, query recordsv1.ListParticipantsQuery) (recordsv1.ParticipantListResponse, error) {
	if err := s.requireOwner(ctx, accountID, sessionID); err != nil {
		return recordsv1.ParticipantListResponse{}, err
	}
	return s.repository.List(ctx, accountID, sessionID, query)
}

func (s *Service) Update(ctx context.Context, accountID, sessionID, participantID string, update Update) (recordsv1.Participant, error) {
	if update.Empty() || participantID == "" {
		return recordsv1.Participant{}, ErrInvalidRequest
	}
	if err := s.requireOwner(ctx, accountID, sessionID); err != nil {
		return recordsv1.Participant{}, err
	}
	update.UpdatedAt = s.now().UTC()
	return s.repository.Update(ctx, sessionID, participantID, update)
}

// GetProvisionalAttribution implements the contracts port used by the realtime module. An absent
// provider speaker ID intentionally yields pending attribution so realtime translation can finish
// without waiting for a stable participant mapping.
func (s *Service) GetProvisionalAttribution(ctx context.Context, observation recordsv1.SpeakerObservation) (recordsv1.SpeakerAttribution, error) {
	if observation.SessionID == "" || observation.TurnID == "" {
		return recordsv1.SpeakerAttribution{}, ErrInvalidRequest
	}
	if observation.ProviderSpeakerID == "" {
		return recordsv1.SpeakerAttribution{AttributionStatus: recordsv1.AttributionPending}, nil
	}

	participant, err := s.repository.FindOrCreate(ctx, observation)
	if err != nil {
		return recordsv1.SpeakerAttribution{}, err
	}
	if participant.ID == "" || participant.SpeakerCode == "" {
		return recordsv1.SpeakerAttribution{}, fmt.Errorf("participant repository returned an incomplete participant: %w", ErrInvalidRequest)
	}

	id := participant.ID
	return recordsv1.SpeakerAttribution{
		ParticipantID:     &id,
		SpeakerCode:       participant.SpeakerCode,
		DisplayName:       participant.DisplayName,
		Confidence:        participant.Confidence,
		AttributionStatus: recordsv1.AttributionProvisional,
	}, nil
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

var _ recordsv1.SpeakerAttributionReader = (*Service)(nil)
