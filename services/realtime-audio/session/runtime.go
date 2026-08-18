package session

import (
	"context"
	"fmt"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
)

// SetProcessingState serializes pipeline progress with lifecycle Start and Stop.
func (s *LifecycleService) SetProcessingState(ctx context.Context, update ProcessingStateUpdate) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateRuntimeUpdate(update); err != nil {
		return err
	}

	unlock := s.locks.lock(update.SessionID)
	defer unlock()

	current, err := s.deps.Runtimes.Get(ctx, update.SessionID)
	if err != nil {
		return fmt.Errorf("read runtime for progress update: %w", err)
	}
	if current.SessionID != update.SessionID {
		return fmt.Errorf("%w: runtime belongs to %q", ErrRuntimeIdentityConflict, current.SessionID)
	}
	if !matchesExpectedIdentity(current.CurrentTurnID, update.ExpectedTurnID) ||
		!matchesExpectedIdentity(current.CurrentPlaybackID, update.ExpectedPlaybackID) {
		return ErrRuntimeIdentityConflict
	}
	if !validRuntimeProgressTransition(current.RuntimeState, update.RuntimeState) {
		return fmt.Errorf("%w: %s -> %s", ErrInvalidRuntimeTransition, current.RuntimeState, update.RuntimeState)
	}
	// A new ordinary utterance may start while the previous reply is still
	// audible. Its EventOpened is the explicit barge-in boundary: replace the
	// old playback owner atomically instead of treating playing -> ASR as a
	// pipeline failure. All other owner changes remain conflicts.
	preemptingTurn := isActiveRuntimeState(current.RuntimeState) && update.RuntimeState == RuntimeASRProcessing
	if !preemptingTurn && (conflictingIdentity(current.CurrentTurnID, update.CurrentTurnID) ||
		conflictingIdentity(current.CurrentPlaybackID, update.CurrentPlaybackID)) {
		return ErrRuntimeIdentityConflict
	}
	if sameRuntimeState(current, update) {
		return nil
	}

	current.RuntimeState = update.RuntimeState
	current.CurrentTurnID = cloneString(update.CurrentTurnID)
	current.CurrentPlaybackID = cloneString(update.CurrentPlaybackID)
	current.LastErrorCode = nil
	current.UpdatedAt = s.deps.Now()
	if err := s.deps.Runtimes.Save(ctx, current); err != nil {
		return fmt.Errorf("save runtime progress: %w", err)
	}
	return nil
}

// SetRuntimeFailed records a terminal media-pipeline failure without changing
// business session state. Stop transitions win if shutdown is already active.
// Empty errorCode defaults to realtime_pipeline_failed.
func (s *LifecycleService) SetRuntimeFailed(ctx context.Context, sessionID string, errorCode realtimev1.RuntimeErrorCode) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if sessionID == "" {
		return ErrSessionIDRequired
	}
	if errorCode == "" {
		errorCode = ErrorCodePipelineFailed
	}
	if !errorCode.Valid() {
		return fmt.Errorf("%w: %q", ErrInvalidRuntimeErrorCode, errorCode)
	}

	unlock := s.locks.lock(sessionID)
	defer unlock()

	current, err := s.deps.Runtimes.Get(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("read runtime for failure update: %w", err)
	}
	if current.SessionID != sessionID {
		return fmt.Errorf("%w: runtime belongs to %q", ErrRuntimeIdentityConflict, current.SessionID)
	}
	if current.RuntimeState == RuntimeStopped || current.RuntimeState == RuntimeStopping || current.RuntimeState == RuntimeFailed {
		return nil
	}
	current.RuntimeState = RuntimeFailed
	current.CurrentTurnID = nil
	current.CurrentPlaybackID = nil
	code := string(errorCode)
	current.LastErrorCode = &code
	current.UpdatedAt = s.deps.Now()
	if err := s.deps.Runtimes.Save(ctx, current); err != nil {
		return fmt.Errorf("save runtime failure: %w", err)
	}
	return nil
}

func validateRuntimeUpdate(update ProcessingStateUpdate) error {
	if update.SessionID == "" {
		return ErrSessionIDRequired
	}
	if invalidExpectedIdentity(update.ExpectedTurnID) || invalidExpectedIdentity(update.ExpectedPlaybackID) ||
		(update.ExpectedPlaybackID != nil && update.ExpectedTurnID == nil) {
		return ErrInvalidRuntimeUpdate
	}
	turnRequired := func() bool { return update.CurrentTurnID != nil && *update.CurrentTurnID != "" }
	playbackRequired := func() bool { return update.CurrentPlaybackID != nil && *update.CurrentPlaybackID != "" }

	switch update.RuntimeState {
	case RuntimeListening:
		if update.CurrentTurnID == nil && update.CurrentPlaybackID == nil {
			return nil
		}
	case RuntimeASRProcessing, RuntimeTranslating, RuntimeThinking, RuntimeAssistantProcessing:
		if turnRequired() && update.CurrentPlaybackID == nil {
			return nil
		}
	case RuntimeTTSProcessing, RuntimePlaying:
		if turnRequired() && playbackRequired() {
			return nil
		}
	}
	return ErrInvalidRuntimeUpdate
}

func invalidExpectedIdentity(identity *string) bool {
	return identity != nil && *identity == ""
}

func matchesExpectedIdentity(current, expected *string) bool {
	return expected == nil || current != nil && *current == *expected
}

func validRuntimeProgressTransition(current, next RuntimeState) bool {
	if current == next {
		return true
	}
	switch current {
	case RuntimeListening:
		return next == RuntimeASRProcessing || next == RuntimeTranslating || next == RuntimeThinking || next == RuntimeAssistantProcessing || next == RuntimeTTSProcessing
	case RuntimeASRProcessing:
		return next == RuntimeASRProcessing || next == RuntimeTranslating || next == RuntimeThinking || next == RuntimeAssistantProcessing || next == RuntimeListening
	case RuntimeTranslating:
		return next == RuntimeASRProcessing || next == RuntimeTTSProcessing || next == RuntimeListening
	case RuntimeAssistantProcessing:
		return next == RuntimeASRProcessing || next == RuntimeTTSProcessing || next == RuntimeListening
	case RuntimeThinking:
		return next == RuntimeASRProcessing || next == RuntimeTTSProcessing || next == RuntimeListening
	case RuntimeTTSProcessing:
		return next == RuntimePlaying || next == RuntimeListening || next == RuntimeASRProcessing
	case RuntimePlaying:
		return next == RuntimeListening || next == RuntimeASRProcessing
	default:
		return false
	}
}

func isActiveRuntimeState(state RuntimeState) bool {
	switch state {
	case RuntimeASRProcessing, RuntimeTranslating, RuntimeThinking, RuntimeAssistantProcessing, RuntimeTTSProcessing, RuntimePlaying:
		return true
	default:
		return false
	}
}

func conflictingIdentity(current, next *string) bool {
	return current != nil && next != nil && *current != *next
}

func sameRuntimeState(current RuntimeSnapshot, update ProcessingStateUpdate) bool {
	return current.RuntimeState == update.RuntimeState &&
		equalString(current.CurrentTurnID, update.CurrentTurnID) &&
		equalString(current.CurrentPlaybackID, update.CurrentPlaybackID) &&
		current.LastErrorCode == nil
}

func equalString(left, right *string) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}

var _ RuntimeStateReporter = (*LifecycleService)(nil)
var _ RuntimeFailureReporter = (*LifecycleService)(nil)
