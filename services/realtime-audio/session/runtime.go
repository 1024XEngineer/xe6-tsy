package session

import (
	"context"
	"fmt"
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
	if !validRuntimeProgressTransition(current.RuntimeState, update.RuntimeState) {
		return fmt.Errorf("%w: %s -> %s", ErrInvalidRuntimeTransition, current.RuntimeState, update.RuntimeState)
	}
	if conflictingIdentity(current.CurrentTurnID, update.CurrentTurnID) ||
		conflictingIdentity(current.CurrentPlaybackID, update.CurrentPlaybackID) {
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
func (s *LifecycleService) SetRuntimeFailed(ctx context.Context, sessionID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if sessionID == "" {
		return ErrSessionIDRequired
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
	current.LastErrorCode = nil
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
	turnRequired := func() bool { return update.CurrentTurnID != nil && *update.CurrentTurnID != "" }
	playbackRequired := func() bool { return update.CurrentPlaybackID != nil && *update.CurrentPlaybackID != "" }

	switch update.RuntimeState {
	case RuntimeListening:
		if update.CurrentTurnID == nil && update.CurrentPlaybackID == nil {
			return nil
		}
	case RuntimeASRProcessing, RuntimeTranslating:
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

func validRuntimeProgressTransition(current, next RuntimeState) bool {
	if current == next {
		return true
	}
	switch current {
	case RuntimeListening:
		return next == RuntimeASRProcessing || next == RuntimeTranslating
	case RuntimeASRProcessing:
		return next == RuntimeTranslating || next == RuntimeListening
	case RuntimeTranslating:
		return next == RuntimeTTSProcessing || next == RuntimeListening
	case RuntimeTTSProcessing:
		return next == RuntimePlaying || next == RuntimeListening
	case RuntimePlaying:
		return next == RuntimeListening
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
