package sessions

import "fmt"

// Dependencies contains only the ports required by the current Create use case.
// Later use-case slices extend this set when they introduce new behavior.
type Dependencies struct {
	Repository Repository
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

func validateIdempotency(key string, requestHash string) error {
	if key == "" || requestHash == "" {
		return ErrInvalidRequest
	}
	return nil
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
