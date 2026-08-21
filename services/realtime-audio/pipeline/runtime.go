package pipeline

import (
	"context"
	"errors"
	"fmt"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/session"
)

func (s *PipelineService) reportRuntime(ctx context.Context, turn TurnContext, state session.RuntimeState, playbackID string) error {
	turnID := turn.ID
	update := session.ProcessingStateUpdate{
		SessionID: turn.SessionID, RuntimeState: state, CurrentTurnID: &turnID, ExpectedTurnID: &turnID,
	}
	if playbackID != "" {
		update.CurrentPlaybackID = &playbackID
	}
	return s.runtime.SetProcessingState(ctx, update)
}

// claimASRRuntime is the only progress update that can replace the owner of a
// still-active prior Turn. Every later stage must retain its own Turn owner.
func (s *PipelineService) claimASRRuntime(ctx context.Context, turn TurnContext) error {
	turnID := turn.ID
	return s.runtime.SetProcessingState(ctx, session.ProcessingStateUpdate{
		SessionID: turn.SessionID, RuntimeState: session.RuntimeASRProcessing, CurrentTurnID: &turnID,
	})
}

func (s *PipelineService) reportListening(ctx context.Context, turn TurnContext) error {
	turnID := turn.ID
	err := s.runtime.SetProcessingState(ctx, session.ProcessingStateUpdate{
		SessionID: turn.SessionID, RuntimeState: session.RuntimeListening,
		ExpectedTurnID: &turnID,
	})
	if errors.Is(err, session.ErrRuntimeIdentityConflict) {
		return nil
	}
	return err
}

func (s *PipelineService) finishASRWithError(ctx context.Context, turn TurnContext, processingErr error) error {
	if err := s.reportListening(ctx, turn); err != nil {
		return errors.Join(processingErr, fmt.Errorf("restore listening runtime: %w", err))
	}
	return processingErr
}

func runtimeUpdateSuperseded(err error) bool {
	return errors.Is(err, session.ErrRuntimeIdentityConflict) || errors.Is(err, session.ErrInvalidRuntimeTransition)
}
