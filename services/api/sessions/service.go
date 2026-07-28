package sessions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"
)

const (
	defaultCompensationTimeout        = 5 * time.Second
	defaultStartReconciliationTimeout = 5 * time.Second
)

// Dependencies contains the boundaries required by Create, Query, and Start.
// Logger is optional and defaults to a discard sink.
type Dependencies struct {
	Repository                 Repository
	LanguageConfigs            LanguageConfigReader
	WebRTCConnections          WebRTCConnectionReader
	Realtime                   RealtimeLifecycle
	IDs                        IDGenerator
	Clock                      Clock
	Logger                     *slog.Logger
	CompensationTimeout        time.Duration
	StartReconciliationTimeout time.Duration
}

// Service owns voice-session use cases without depending on HTTP or
// constructing infrastructure adapters.
type Service struct {
	deps  Dependencies
	locks keyedLocker
}

// NewService rejects a partially wired session service.
func NewService(deps Dependencies) (*Service, error) {
	if deps.Repository == nil {
		return nil, fmt.Errorf("%w: repository is required", ErrInvalidDependency)
	}
	if deps.LanguageConfigs == nil {
		return nil, fmt.Errorf("%w: language config reader is required", ErrInvalidDependency)
	}
	if deps.WebRTCConnections == nil {
		return nil, fmt.Errorf("%w: WebRTC connection reader is required", ErrInvalidDependency)
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
	if deps.Logger == nil {
		deps.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if deps.CompensationTimeout <= 0 {
		deps.CompensationTimeout = defaultCompensationTimeout
	}
	if deps.StartReconciliationTimeout <= 0 {
		deps.StartReconciliationTimeout = defaultStartReconciliationTimeout
	}
	return &Service{deps: deps, locks: newKeyedLocker()}, nil
}

// CreateInput carries authenticated ownership and canonical request identity.
type CreateInput struct {
	AccountID      string
	AudioConfig    *AudioConfig
	Capabilities   Capabilities
	IdempotencyKey string
	RequestHash    string
}

// StartInput carries authenticated ownership, idempotency, and audit metadata.
type StartInput struct {
	AccountID      string
	SessionID      string
	IdempotencyKey string
	RequestHash    string
	TraceID        string
	StartedBy      string
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
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) {
		return err
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

func decodeSessionReadiness(session VoiceSession) error {
	var audio AudioConfig
	if err := json.Unmarshal(session.AudioConfig, &audio); err != nil {
		return fmt.Errorf("%w: decode persisted audio config: %v", ErrUnsupportedAudio, err)
	}
	if err := validateAudioConfig(audio); err != nil {
		return err
	}

	var capabilities Capabilities
	if err := json.Unmarshal(session.Capabilities, &capabilities); err != nil {
		return fmt.Errorf("%w: decode persisted capabilities: %v", ErrInvalidRequest, err)
	}
	return validateCapabilities(capabilities)
}

func (s *Service) compensationContext(parent context.Context) (context.Context, context.CancelFunc) {
	// Compensation retains trace values but ignores client cancellation. Its
	// independent timeout prevents a disconnected request from leaking cleanup.
	return context.WithTimeout(context.WithoutCancel(parent), s.deps.CompensationTimeout)
}

func (s *Service) startReconciliationContext(
	parent context.Context,
) (context.Context, context.CancelFunc) {
	// Reconciliation must outlive an uncertain Start request long enough to
	// determine whether that operation owns a running media pipeline.
	return context.WithTimeout(
		context.WithoutCancel(parent),
		s.deps.StartReconciliationTimeout,
	)
}
