package sessions

import (
	"context"
	"errors"
	"fmt"
)

// Dependencies contains the ports required by the implemented Create and Query
// use cases. Later lifecycle slices extend it only when they add new behavior.
type Dependencies struct {
	Repository Repository
	Realtime   RealtimeLifecycle
	IDs        IDGenerator
	Clock      Clock
}

// Service owns voice-session use cases without depending on HTTP or
// constructing infrastructure adapters.
type Service struct {
	deps Dependencies
}

// NewService rejects a partially wired Create service.
func NewService(deps Dependencies) (*Service, error) {
	if deps.Repository == nil {
		return nil, fmt.Errorf("%w: repository is required", ErrInvalidDependency)
	}
	if deps.Realtime == nil {
		return nil, fmt.Errorf("%w: realtime lifecycle is required", ErrInvalidDependency)
	}
	if deps.IDs == nil {
		return nil, fmt.Errorf("%w: ID generator is required", ErrInvalidDependency)
	}
	if deps.Clock == nil {
		return nil, fmt.Errorf("%w: clock is required", ErrInvalidDependency)
	}
	return &Service{deps: deps}, nil
}

// CreateInput carries authenticated ownership and canonical request identity.
type CreateInput struct {
	AccountID      string
	AudioConfig    *AudioConfig
	Capabilities   Capabilities
	IdempotencyKey string
	RequestHash    string
}

// DetailInput identifies an account-scoped session read.
type DetailInput struct {
	AccountID string
	SessionID string
}

// ListInput carries account-scoped persistent filters only.
type ListInput struct {
	AccountID string
	Status    *Status
	Cursor    string
	Limit     int
}

func validateIdentity(accountID string, sessionID string) error {
	if accountID == "" {
		return ErrUnauthorized
	}
	if sessionID == "" {
		return ErrInvalidRequest
	}
	return nil
}

func validateIdempotency(key string, requestHash string) error {
	if key == "" || requestHash == "" {
		return ErrInvalidRequest
	}
	return nil
}

func validateRuntimeSnapshot(snapshot RuntimeSnapshot, sessionID string) error {
	if snapshot.SessionID != sessionID ||
		!snapshot.RuntimeState.Valid() ||
		snapshot.UpdatedAt.IsZero() {
		return ErrRuntimeUnavailable
	}
	return nil
}

func mapDependencyError(ctx context.Context, err error, boundary error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, ErrNotImplemented) {
		return ErrNotImplemented
	}
	return fmt.Errorf("%w: %v", boundary, err)
}

func validateAudioConfig(config AudioConfig) error {
	if config.Codec != "opus" || config.SampleRateHz != 48000 || config.Channels != 1 {
		return ErrUnsupportedAudio
	}
	return nil
}

func validateCapabilities(capabilities Capabilities) error {
	if !capabilities.WebRTC ||
		!capabilities.DataChannel ||
		!capabilities.Microphone ||
		!capabilities.Speaker ||
		!capabilities.SpeakerDiarization {
		return ErrInvalidRequest
	}
	return nil
}
