package sessions

import (
	"context"
	"fmt"
)

// Create persists one business session and never creates or reads realtime
// state. Repository.Create owns the atomic idempotency result.
func (s *Service) Create(ctx context.Context, input CreateInput) (VoiceSession, error) {
	if err := ctx.Err(); err != nil {
		return VoiceSession{}, err
	}
	if input.AccountID == "" {
		return VoiceSession{}, ErrUnauthorized
	}
	if err := validateIdempotency(input.IdempotencyKey, input.RequestHash); err != nil {
		return VoiceSession{}, err
	}
	if err := validateCapabilities(input.Capabilities); err != nil {
		return VoiceSession{}, err
	}

	audioConfig := DefaultAudioConfig()
	if input.AudioConfig != nil {
		audioConfig = *input.AudioConfig
	}
	if err := validateAudioConfig(audioConfig); err != nil {
		return VoiceSession{}, err
	}

	sessionID := s.deps.IDs.NewVoiceSessionID()
	if sessionID == "" {
		return VoiceSession{}, fmt.Errorf("%w: ID generator returned an empty session ID", ErrInvalidDependency)
	}
	now := s.deps.Clock.Now()
	if now.IsZero() {
		return VoiceSession{}, fmt.Errorf("%w: clock returned a zero timestamp", ErrInvalidDependency)
	}
	session, _, err := s.deps.Repository.Create(ctx, CreateParams{
		ID:             sessionID,
		AccountID:      input.AccountID,
		DeviceID:       input.DeviceID,
		AudioConfig:    audioConfig,
		Capabilities:   input.Capabilities,
		IdempotencyKey: input.IdempotencyKey,
		RequestHash:    input.RequestHash,
		CreatedAt:      now.UTC(),
	})
	if err != nil {
		return VoiceSession{}, fmt.Errorf("create voice session: %w", err)
	}
	return session, nil
}
