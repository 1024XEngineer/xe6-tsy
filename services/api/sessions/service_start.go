package sessions

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
)

// Start coordinates one durable operation across the control-plane and
// realtime boundaries. The repository operation, not the in-process lock, is
// the authority for cross-instance activation and compensation ownership.
func (s *Service) Start(ctx context.Context, input StartInput) (VoiceSession, error) {
	if err := validateStartInput(ctx, &input); err != nil {
		return VoiceSession{}, err
	}

	unlock := s.locks.lock(input.SessionID)
	defer unlock()
	if err := ctx.Err(); err != nil {
		return VoiceSession{}, err
	}

	session, err := s.deps.Repository.GetOwned(ctx, input.AccountID, input.SessionID)
	if err != nil {
		return VoiceSession{}, fmt.Errorf("read voice session for start: %w", err)
	}
	switch session.Status {
	case StatusActive:
		return s.replayCompletedStart(ctx, input, session)
	case StatusCreated:
		// Only a created session may cross the realtime Start boundary.
	default:
		return VoiceSession{}, ErrSessionStateConflict
	}
	if err := s.validateStartReadiness(ctx, input, session); err != nil {
		return VoiceSession{}, err
	}

	operation, err := s.beginStartOperation(ctx, input)
	if err != nil {
		return VoiceSession{}, err
	}
	return s.continueStartOperation(ctx, input, operation)
}

func validateStartInput(ctx context.Context, input *StartInput) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateIdentity(input.AccountID, input.SessionID); err != nil {
		return err
	}
	if err := validateIdempotency(input.IdempotencyKey, input.RequestHash); err != nil {
		return err
	}
	if input.TraceID == "" {
		return ErrInvalidRequest
	}
	if input.StartedBy == "" {
		input.StartedBy = input.AccountID
	}
	return nil
}

func (s *Service) validateStartReadiness(
	ctx context.Context,
	input StartInput,
	session VoiceSession,
) error {
	if err := decodeSessionReadiness(session); err != nil {
		return err
	}
	languageConfig, err := s.deps.LanguageConfigs.GetCurrentConfig(ctx, input.SessionID)
	if err != nil {
		return mapDependencyError(ctx, err, ErrLanguageConfigNotReady)
	}
	if languageConfig.SessionID != input.SessionID || !languageConfig.Ready() {
		return ErrLanguageConfigNotReady
	}

	connection, err := s.deps.WebRTCConnections.GetConnectionState(ctx, input.SessionID)
	if err != nil {
		return mapDependencyError(ctx, err, ErrWebRTCUnavailable)
	}
	if connection.SessionID != input.SessionID || !connection.ConnectionState.Valid() {
		return ErrWebRTCUnavailable
	}
	if !connection.ConnectionState.Ready() {
		return ErrWebRTCNotReady
	}
	return nil
}

func (s *Service) beginStartOperation(
	ctx context.Context,
	input StartInput,
) (StartOperation, error) {
	operationID := s.deps.IDs.NewStartOperationID()
	if operationID == "" {
		return StartOperation{}, fmt.Errorf(
			"%w: ID generator returned an empty start operation ID",
			ErrInvalidDependency,
		)
	}
	now := s.deps.Clock.Now()
	if now.IsZero() {
		return StartOperation{}, fmt.Errorf(
			"%w: clock returned a zero timestamp",
			ErrInvalidDependency,
		)
	}
	begin, err := s.deps.Repository.BeginStartOperation(ctx, BeginStartOperationParams{
		OperationID:    operationID,
		SessionID:      input.SessionID,
		AccountID:      input.AccountID,
		IdempotencyKey: input.IdempotencyKey,
		RequestHash:    input.RequestHash,
		CreatedAt:      now.UTC(),
	})
	if err != nil {
		return StartOperation{}, fmt.Errorf("begin voice session start operation: %w", err)
	}
	operation := begin.Operation
	if operation.ID == "" ||
		operation.SessionID != input.SessionID ||
		operation.AccountID != input.AccountID ||
		!operation.MatchesRequest(input.IdempotencyKey, input.RequestHash) ||
		!operation.Status.Valid() {
		return StartOperation{}, fmt.Errorf(
			"%w: invalid start operation returned by repository",
			ErrConcurrentTransition,
		)
	}
	return operation, nil
}

func (s *Service) replayCompletedStart(
	ctx context.Context,
	input StartInput,
	current VoiceSession,
) (VoiceSession, error) {
	operation, err := s.beginStartOperation(ctx, input)
	if err != nil {
		return VoiceSession{}, err
	}
	if operation.Status != StartOperationCompleted {
		return VoiceSession{}, ErrConcurrentTransition
	}
	return current, nil
}

func (s *Service) continueStartOperation(
	ctx context.Context,
	input StartInput,
	operation StartOperation,
) (VoiceSession, error) {
	switch operation.Status {
	case StartOperationPending:
		return s.startPendingOperation(ctx, input, operation)
	case StartOperationCompensating:
		if operation.CompensationClaimID == nil || *operation.CompensationClaimID == "" {
			return VoiceSession{}, ErrConcurrentTransition
		}
		return s.compensateStartedOperation(
			ctx,
			input,
			operation,
			*operation.CompensationClaimID,
			ErrRealtimeStartFailed,
		)
	case StartOperationCompleted:
		session, err := s.deps.Repository.GetOwned(ctx, input.AccountID, input.SessionID)
		if err != nil {
			return VoiceSession{}, fmt.Errorf("read completed voice session start: %w", err)
		}
		if session.Status != StatusActive {
			return VoiceSession{}, ErrConcurrentTransition
		}
		return session, nil
	case StartOperationCompensated:
		return VoiceSession{}, ErrIdempotencyKeyConflict
	case StartOperationCompensationFailed:
		return VoiceSession{}, ErrSessionStartInProgress
	default:
		return VoiceSession{}, ErrConcurrentTransition
	}
}

func (s *Service) startPendingOperation(
	ctx context.Context,
	input StartInput,
	operation StartOperation,
) (VoiceSession, error) {
	runtime, err := s.deps.Realtime.Start(ctx, StartRealtimeCommand{
		SessionID: input.SessionID,
		TraceID:   input.TraceID,
		StartedBy: input.StartedBy,
	})
	if err != nil {
		if errors.Is(err, ErrRealtimeAlreadyRunning) {
			return VoiceSession{}, ErrRealtimeAlreadyRunning
		}
		return VoiceSession{}, mapDependencyError(ctx, err, ErrRealtimeStartFailed)
	}

	if err := validateRuntimeSnapshot(runtime, input.SessionID); err != nil {
		startErr := fmt.Errorf("%w: invalid start snapshot", ErrRealtimeStartFailed)
		return s.compensateStartedOperation(ctx, input, operation, input.TraceID, startErr)
	}
	switch runtime.RuntimeState {
	case RuntimeStarting, RuntimeStopping:
		return VoiceSession{}, ErrRealtimeAlreadyRunning
	}
	if err := validateCompletedStartRuntime(runtime); err != nil {
		return s.compensateStartedOperation(ctx, input, operation, input.TraceID, err)
	}

	startedAt := s.deps.Clock.Now().UTC()
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

func validateCompletedStartRuntime(runtime RuntimeSnapshot) error {
	switch runtime.RuntimeState {
	case RuntimeListening,
		RuntimeASRProcessing,
		RuntimeTranslating,
		RuntimeTTSProcessing,
		RuntimePlaying:
		return nil
	default:
		return ErrRealtimeStartFailed
	}
}

func validateCompensatedRuntime(runtime RuntimeSnapshot, sessionID string) error {
	if err := validateRuntimeSnapshot(runtime, sessionID); err != nil {
		return fmt.Errorf("%w: invalid compensation snapshot", ErrRealtimeStopFailed)
	}
	if runtime.RuntimeState != RuntimeStopped {
		return ErrRealtimeStopFailed
	}
	return nil
}

// compensateStartedOperation stops realtime only after the repository grants
// this operation and ClaimID exclusive cleanup authority. A denied or
// uncertain claim is a hard prohibition on Stop.
func (s *Service) compensateStartedOperation(
	parent context.Context,
	input StartInput,
	operation StartOperation,
	claimID string,
	originalErr error,
) (VoiceSession, error) {
	ctx, cancel := s.compensationContext(parent)
	defer cancel()

	claimedAt := s.deps.Clock.Now().UTC()
	claim, claimErr := s.deps.Repository.ClaimStartCompensation(
		ctx,
		ClaimStartCompensationParams{
			SessionID:   input.SessionID,
			AccountID:   input.AccountID,
			OperationID: operation.ID,
			ClaimID:     claimID,
			ClaimedAt:   claimedAt,
		},
	)
	if claimErr != nil {
		return VoiceSession{}, errors.Join(
			originalErr,
			fmt.Errorf("claim realtime start compensation: %w", claimErr),
		)
	}
	if !claim.Claimed {
		return s.resolveDeniedStartCompensation(ctx, input, originalErr)
	}

	runtime, stopErr := s.deps.Realtime.Stop(ctx, StopRealtimeCommand{
		SessionID: input.SessionID,
		TraceID:   input.TraceID,
		Reason:    EndReasonOperatorCancelled,
		EndedAt:   s.deps.Clock.Now().UTC(),
	})
	if stopErr != nil {
		stopErr = mapDependencyError(ctx, stopErr, ErrRealtimeStopFailed)
	} else {
		stopErr = validateCompensatedRuntime(runtime, input.SessionID)
	}
	if stopErr == nil {
		completeErr := s.deps.Repository.CompleteStartCompensation(
			ctx,
			CompleteStartCompensationParams{
				SessionID:   input.SessionID,
				AccountID:   input.AccountID,
				OperationID: operation.ID,
				ClaimID:     claimID,
				CompletedAt: s.deps.Clock.Now().UTC(),
			},
		)
		if completeErr != nil {
			return VoiceSession{}, errors.Join(
				originalErr,
				fmt.Errorf("complete realtime start compensation: %w", completeErr),
			)
		}
		s.deps.Logger.WarnContext(ctx, "compensated realtime start after activation failure",
			slog.String("request_id", input.TraceID),
			slog.String("session_id", input.SessionID),
			slog.String("operation_id", operation.ID),
			slog.Any("original_error", originalErr),
		)
		return VoiceSession{}, originalErr
	}

	failErr := s.deps.Repository.FailStartCompensation(
		ctx,
		FailStartCompensationParams{
			SessionID:   input.SessionID,
			AccountID:   input.AccountID,
			OperationID: operation.ID,
			ClaimID:     claimID,
			FailedAt:    s.deps.Clock.Now().UTC(),
		},
	)
	s.deps.Logger.ErrorContext(ctx, "failed to compensate realtime start",
		slog.String("request_id", input.TraceID),
		slog.String("session_id", input.SessionID),
		slog.String("operation_id", operation.ID),
		slog.Any("original_error", originalErr),
		slog.Any("compensation_error", stopErr),
		slog.Any("persistence_error", failErr),
	)
	if failErr != nil {
		return VoiceSession{}, errors.Join(
			originalErr,
			fmt.Errorf("compensate realtime start: %w", stopErr),
			fmt.Errorf("persist failed realtime start compensation: %w", failErr),
		)
	}
	return VoiceSession{}, errors.Join(
		originalErr,
		fmt.Errorf("compensate realtime start: %w", stopErr),
	)
}

func (s *Service) resolveDeniedStartCompensation(
	ctx context.Context,
	input StartInput,
	originalErr error,
) (VoiceSession, error) {
	session, err := s.deps.Repository.GetOwned(ctx, input.AccountID, input.SessionID)
	if err != nil {
		return VoiceSession{}, errors.Join(
			originalErr,
			fmt.Errorf("read voice session after denied compensation claim: %w", err),
		)
	}
	if session.Status == StatusActive {
		replayed, replayErr := s.replayCompletedStart(ctx, input, session)
		if replayErr != nil {
			return VoiceSession{}, errors.Join(originalErr, replayErr)
		}
		return replayed, nil
	}
	return VoiceSession{}, errors.Join(originalErr, ErrSessionStartInProgress)
}
