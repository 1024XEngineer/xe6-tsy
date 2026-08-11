package sessions

import (
	"context"
	"errors"
	"fmt"
)

func (s *Service) startPendingOperation(
	ctx context.Context,
	input StartInput,
	operation StartOperation,
) (VoiceSession, error) {
	runtime, startErr := s.deps.Realtime.Start(ctx, StartRealtimeCommand{
		SessionID:   input.SessionID,
		OperationID: operation.ID,
		TraceID:     input.TraceID,
		StartedBy:   input.StartedBy,
	})
	if startErr == nil {
		return s.continueOwnedStartRuntime(ctx, input, operation, runtime, nil)
	}
	if !errors.Is(startErr, ErrRealtimeAlreadyRunning) {
		startErr = mapRealtimeStartError(ctx, startErr)
	}
	return s.reconcileUncertainStart(ctx, input, operation, startErr)
}

// reconcileUncertainStart gives an uncertain Start result one bounded,
// cancellation-independent read to determine whether this operation owns a
// runtime. The fresh context also carries a confirmed activation to storage.
func (s *Service) reconcileUncertainStart(
	parent context.Context,
	input StartInput,
	operation StartOperation,
	startErr error,
) (VoiceSession, error) {
	reconcileCtx, reconcileCancel := s.startReconciliationContext(parent)
	defer reconcileCancel()

	runtime, err := s.deps.Realtime.GetRuntimeState(reconcileCtx, input.SessionID)
	if errors.Is(err, ErrRuntimeSnapshotNotFound) {
		return VoiceSession{}, startErr
	}
	if err != nil {
		return VoiceSession{}, errors.Join(
			startErr,
			mapRuntimeReconciliationError(reconcileCtx, err),
		)
	}
	return s.continueOwnedStartRuntime(
		reconcileCtx,
		input,
		operation,
		runtime,
		startErr,
	)
}

func (s *Service) continueOwnedStartRuntime(
	ctx context.Context,
	input StartInput,
	operation StartOperation,
	runtime RuntimeSnapshot,
	uncertainErr error,
) (VoiceSession, error) {
	if err := validateStartRuntimeOwnership(runtime, input.SessionID, operation.ID); err != nil {
		return VoiceSession{}, err
	}
	if err := validateStartRuntimeContent(runtime); err != nil {
		return s.compensateStartedOperation(ctx, input, operation, input.TraceID, err)
	}

	switch runtime.RuntimeState {
	case RuntimeListening,
		RuntimeASRProcessing,
		RuntimeTranslating,
		RuntimeThinking,
		RuntimeAssistantProcessing,
		RuntimeTTSProcessing,
		RuntimePlaying:
		return s.activateOwnedStartRuntime(ctx, input, operation)
	case RuntimeStarting, RuntimeStopping:
		return VoiceSession{}, ErrRealtimeAlreadyRunning
	case RuntimeStopped, RuntimeFailed:
		if uncertainErr != nil {
			return VoiceSession{}, uncertainErr
		}
		return s.compensateStartedOperation(
			ctx,
			input,
			operation,
			input.TraceID,
			ErrRealtimeStartFailed,
		)
	default:
		panic("validated runtime state was not classified")
	}
}

func mapRealtimeStartError(ctx context.Context, err error) error {
	mapped := mapDependencyError(ctx, err, ErrRealtimeStartFailed)
	if !errors.Is(mapped, ErrRealtimeStartFailed) {
		mapped = errors.Join(ErrRealtimeStartFailed, mapped)
	}
	if !errors.Is(mapped, err) {
		mapped = errors.Join(mapped, err)
	}
	return mapped
}

func mapRuntimeReconciliationError(ctx context.Context, err error) error {
	mapped := mapDependencyError(ctx, err, ErrRuntimeUnavailable)
	if !errors.Is(mapped, ErrRuntimeUnavailable) {
		mapped = errors.Join(ErrRuntimeUnavailable, mapped)
	}
	if !errors.Is(mapped, err) {
		mapped = errors.Join(mapped, err)
	}
	return fmt.Errorf("reconcile realtime start runtime: %w", mapped)
}

func (s *Service) activateOwnedStartRuntime(
	ctx context.Context,
	input StartInput,
	operation StartOperation,
) (VoiceSession, error) {
	startedAt, err := s.nowUTC("activate voice session")
	if err != nil {
		return s.compensateStartedOperation(ctx, input, operation, input.TraceID, err)
	}
	active, _, transitionErr := s.deps.Repository.TransitionToActive(ctx, StartTransitionParams{
		SessionID:      input.SessionID,
		AccountID:      input.AccountID,
		OperationID:    operation.ID,
		Expected:       StatusCreated,
		StartedAt:      startedAt,
		IdempotencyKey: input.IdempotencyKey,
		RequestHash:    input.RequestHash,
	})
	if transitionErr == nil {
		return active, nil
	}
	originalErr := fmt.Errorf("transition voice session to active: %w", transitionErr)
	return s.compensateStartedOperation(ctx, input, operation, input.TraceID, originalErr)
}

func validateStartRuntimeOwnership(
	runtime RuntimeSnapshot,
	sessionID string,
	operationID string,
) error {
	if runtime.SessionID != sessionID ||
		runtime.StartOperationID == "" ||
		runtime.StartOperationID != operationID {
		return fmt.Errorf(
			"%w: invalid runtime ownership for start operation",
			ErrConcurrentTransition,
		)
	}
	return nil
}

func validateStartRuntimeContent(runtime RuntimeSnapshot) error {
	if !runtime.RuntimeState.Valid() || runtime.UpdatedAt.IsZero() {
		return fmt.Errorf("%w: invalid start snapshot", ErrRealtimeStartFailed)
	}
	return nil
}
