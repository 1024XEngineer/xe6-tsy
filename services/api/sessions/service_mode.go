package sessions

import (
	"context"
	"errors"
	"fmt"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
)

// GetMode returns the authoritative realtime snapshot after account ownership
// and active-session checks. The API does not cache or reconstruct this state.
func (s *Service) GetMode(ctx context.Context, input DetailInput) (ModeSnapshot, error) {
	if err := s.validateModeRequest(ctx, input.AccountID, input.SessionID); err != nil {
		return ModeSnapshot{}, err
	}
	snapshot, err := s.deps.Modes.GetModeState(ctx, input.SessionID)
	if err != nil {
		return ModeSnapshot{}, mapModeDependencyError(ctx, err)
	}
	if !validModeSnapshot(snapshot, input.SessionID) {
		return ModeSnapshot{}, ErrModeUnavailable
	}
	return snapshot, nil
}

// SwitchMode authorizes and forwards one idempotent compare-and-switch. All
// replay and mode mutation semantics remain owned by the realtime coordinator.
func (s *Service) SwitchMode(ctx context.Context, input SwitchModeInput) (ModeSwitchResult, error) {
	if err := ctx.Err(); err != nil {
		return ModeSwitchResult{}, err
	}
	if err := validateSwitchModeInput(input); err != nil {
		return ModeSwitchResult{}, err
	}
	if err := s.validateModeRequest(ctx, input.AccountID, input.SessionID); err != nil {
		return ModeSwitchResult{}, err
	}
	result, err := s.deps.Modes.SwitchMode(ctx, SwitchModeCommand{
		SessionID:          input.SessionID,
		RuntimeInstanceID:  input.RuntimeInstanceID,
		OperationID:        input.OperationID,
		TraceID:            input.TraceID,
		ExpectedGeneration: input.ExpectedGeneration,
		TargetMode:         input.TargetMode,
	})
	if err != nil {
		return ModeSwitchResult{}, mapModeDependencyError(ctx, err)
	}
	if !validModeSwitchResult(result, input) {
		return ModeSwitchResult{}, ErrModeUnavailable
	}
	return result, nil
}

func (s *Service) validateModeRequest(ctx context.Context, accountID, sessionID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateIdentity(accountID, sessionID); err != nil {
		return err
	}
	if s.deps.Modes == nil {
		return ErrNotImplemented
	}
	session, err := s.deps.Repository.GetOwned(ctx, accountID, sessionID)
	if err != nil {
		return fmt.Errorf("read owned voice session for mode control: %w", err)
	}
	if session.Status != StatusActive {
		return ErrSessionStateConflict
	}
	return nil
}

func validateSwitchModeInput(input SwitchModeInput) error {
	if err := validateIdentity(input.AccountID, input.SessionID); err != nil {
		return err
	}
	if input.RuntimeInstanceID == "" || input.OperationID == "" ||
		len(input.OperationID) > maxIdempotencyKeyLength || input.TraceID == "" ||
		input.ExpectedGeneration < 1 || !input.TargetMode.Valid() {
		return ErrInvalidRequest
	}
	return nil
}

func validModeSnapshot(snapshot ModeSnapshot, sessionID string) bool {
	return snapshot.SessionID == sessionID && snapshot.RuntimeInstanceID != "" &&
		snapshot.ActiveMode.Valid() && snapshot.Generation >= 1 &&
		snapshot.Phase.Valid() && !snapshot.UpdatedAt.IsZero()
}

func validModeSwitchResult(result ModeSwitchResult, input SwitchModeInput) bool {
	if result.OperationID != input.OperationID || !result.Status.Valid() ||
		!validModeSnapshot(result.State, input.SessionID) ||
		result.State.RuntimeInstanceID != input.RuntimeInstanceID ||
		result.State.ActiveMode != input.TargetMode ||
		result.State.Phase != realtimev1.ModePhaseActive ||
		result.State.LastOperationID == nil ||
		*result.State.LastOperationID != input.OperationID {
		return false
	}
	switch result.Status {
	case realtimev1.ModeSwitchApplied:
		return result.State.Generation == input.ExpectedGeneration+1
	case realtimev1.ModeSwitchUnchanged:
		return result.State.Generation == input.ExpectedGeneration
	default:
		return false
	}
}

func mapModeDependencyError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	for _, known := range []error{
		context.Canceled,
		context.DeadlineExceeded,
		ErrInvalidRequest,
		ErrSessionStateConflict,
		ErrModeNotAvailable,
		ErrModeGenerationConflict,
		ErrModeRuntimeMismatch,
		ErrModeOperationConflict,
		ErrModeUnavailable,
		ErrNotImplemented,
	} {
		if errors.Is(err, known) {
			return err
		}
	}
	return fmt.Errorf("%w: %v", ErrModeUnavailable, err)
}
