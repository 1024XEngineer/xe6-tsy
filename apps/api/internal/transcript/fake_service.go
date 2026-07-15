package transcript

import (
	"context"
	"time"
)

// FakeService is deterministic in-memory scaffolding for HTTP and package
// tests. It deliberately does not persist state or invoke real providers.
type FakeService struct {
	DependencyErr error
}

// NewFakeService creates a usable default service with no external runtime
// dependency, keeping the skeleton runnable in local and CI environments.
func NewFakeService() *FakeService {
	return &FakeService{}
}

func (s *FakeService) StartRun(_ context.Context, cmd StartRunCommand) (SpeechProcessingRun, error) {
	if !cmd.valid() {
		return SpeechProcessingRun{}, ErrInvalidInput
	}
	if s.DependencyErr != nil {
		return SpeechProcessingRun{}, s.DependencyErr
	}
	return SpeechProcessingRun{
		ID:             "run-demo",
		SessionID:      cmd.SessionID,
		TrackID:        cmd.TrackID,
		ConfigVersion:  cmd.ConfigVersion,
		Direction:      cmd.Direction,
		SourceLanguage: cmd.SourceLanguage,
		TargetLanguage: cmd.TargetLanguage,
		TTSMode:        cmd.TTSMode,
		Status:         RunStatusStreaming,
		StartedAt:      time.Unix(0, 0).UTC(),
	}, nil
}

func (s *FakeService) CompleteRun(_ context.Context, runID string) (SpeechProcessingRun, error) {
	if runID == "" {
		return SpeechProcessingRun{}, ErrInvalidInput
	}
	return SpeechProcessingRun{ID: runID, Status: RunStatusCompleted}, s.DependencyErr
}

func (s *FakeService) ListUtterances(_ context.Context, sessionID string) ([]SpeechUtterance, error) {
	if sessionID == "" {
		return nil, ErrInvalidInput
	}
	return []SpeechUtterance{}, s.DependencyErr
}

func (s *FakeService) AssignSpeakerRole(_ context.Context, cmd AssignSpeakerRoleCommand) (SpeakerRoleBinding, error) {
	if cmd.SessionID == "" || cmd.SpeakerClusterID == "" || cmd.Role == "" {
		return SpeakerRoleBinding{}, ErrInvalidInput
	}
	return SpeakerRoleBinding{
		SessionID:        cmd.SessionID,
		SpeakerClusterID: cmd.SpeakerClusterID,
		Role:             cmd.Role,
		UpdatedBy:        cmd.OperatorID,
	}, s.DependencyErr
}

func (s *FakeService) RequestTTS(_ context.Context, cmd RequestTTSCommand) (TtsSynthesisRun, error) {
	if cmd.SessionID == "" || cmd.SourceRef == "" || cmd.TextHash == "" {
		return TtsSynthesisRun{}, ErrInvalidInput
	}
	return TtsSynthesisRun{
		ID:             "tts-demo",
		SessionID:      cmd.SessionID,
		SourceRef:      cmd.SourceRef,
		TextHash:       cmd.TextHash,
		TargetLanguage: cmd.TargetLanguage,
		VoiceProfileID: cmd.VoiceProfileID,
		Status:         TTSStatusPending,
	}, s.DependencyErr
}
