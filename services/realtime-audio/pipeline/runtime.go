package pipeline

import (
	"context"
	"errors"
	"fmt"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/session"
)

func (s *PipelineService) reportRuntime(ctx context.Context, turn TurnContext, state session.RuntimeState, playbackID string) error {
	turnID := turn.ID
	update := session.RuntimeStateUpdate{
		SessionID: turn.SessionID, RuntimeState: state, CurrentTurnID: &turnID,
	}
	if playbackID != "" {
		update.CurrentPlaybackID = &playbackID
	}
	return s.runtime.ReportRuntimeState(ctx, update)
}

func (s *PipelineService) reportListening(ctx context.Context, turn TurnContext) error {
	return s.runtime.ReportRuntimeState(ctx, session.RuntimeStateUpdate{
		SessionID: turn.SessionID, RuntimeState: session.RuntimeListening,
	})
}

func (s *PipelineService) finishASRWithError(ctx context.Context, turn TurnContext, processingErr error) error {
	if err := s.reportListening(ctx, turn); err != nil {
		return errors.Join(processingErr, fmt.Errorf("restore listening runtime: %w", err))
	}
	return processingErr
}
