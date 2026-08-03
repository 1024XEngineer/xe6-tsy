// Package participants owns session-scoped temporary speaker attribution and mapping updates.
package participants

import (
	"context"
	"errors"
	"fmt"
	"strings"
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

// ResolveProviderMapping maps a session-scoped provider speaker key to the stable participant for
// the session. It is the internal service boundary used by the async attribution worker: it enforces
// account ownership and never fabricates a mapping when the provider key is absent. The mapping is
// deterministic and reusable across turns; it does not rewrite existing voice turns.
func (s *Service) ResolveProviderMapping(ctx context.Context, accountID string, observation recordsv1.SpeakerObservation) (recordsv1.Participant, error) {
	if strings.TrimSpace(observation.ProviderSpeakerID) == "" {
		return recordsv1.Participant{}, ErrInvalidRequest
	}
	if err := s.requireOwner(ctx, accountID, observation.SessionID); err != nil {
		return recordsv1.Participant{}, err
	}
	return s.repository.FindOrCreate(ctx, observation)
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
