package sessions

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// End persists request identity before attempting cleanup. Every failure after
// SaveEndIntent therefore remains recoverable by an idempotent replay.
func (s *Service) End(
	ctx context.Context,
	input EndInput,
) (result VoiceSession, resultErr error) {
	if err := ctx.Err(); err != nil {
		return VoiceSession{}, err
	}
	if err := validateEndInput(input); err != nil {
		return VoiceSession{}, err
	}

	unlock, err := s.locks.lock(ctx, input.SessionID)
	if err != nil {
		return VoiceSession{}, err
	}
	defer unlock()

	session, err := s.deps.Repository.GetOwned(ctx, input.AccountID, input.SessionID)
	if err != nil {
		return VoiceSession{}, fmt.Errorf("read voice session for end: %w", err)
	}
	requestedAt, err := s.nowUTC("end intent")
	if err != nil {
		return VoiceSession{}, err
	}
	requestOwner := "request:" + input.TraceID
	leaseExpiresAt := requestedAt.Add(s.deps.EndRecoveryLeaseDuration)
	leaseStartedAt := time.Now()
	intent, _, err := s.deps.Repository.SaveEndIntent(ctx, EndIntent{
		SessionID:      input.SessionID,
		AccountID:      input.AccountID,
		Reason:         input.Reason,
		IdempotencyKey: input.IdempotencyKey,
		RequestHash:    input.RequestHash,
		TraceID:        input.TraceID,
		RequestedAt:    requestedAt,
		RecoveryOwner:  &requestOwner,
		LeaseExpiresAt: &leaseExpiresAt,
	})
	if err != nil {
		return VoiceSession{}, fmt.Errorf("save voice session end intent: %w", err)
	}
	if !intent.MatchesRequest(input.IdempotencyKey, input.RequestHash) {
		return VoiceSession{}, ErrIdempotencyKeyConflict
	}
	if err := validateEndIntent(intent, session, input.Reason); err != nil {
		return VoiceSession{}, err
	}
	if intent.Completed() {
		return session, nil
	}
	if intent.RecoveryOwner == nil || *intent.RecoveryOwner != requestOwner ||
		intent.LeaseExpiresAt == nil {
		return VoiceSession{}, ErrConcurrentTransition
	}
	leaseRemaining := s.deps.EndRecoveryLeaseDuration - time.Since(leaseStartedAt)
	if leaseRemaining <= 0 {
		return VoiceSession{}, ErrConcurrentTransition
	}
	defer func() {
		if resultErr == nil {
			return
		}
		persistCtx, cancel := s.endPersistenceContext(ctx)
		defer cancel()
		err := s.deps.Repository.RetryClaimedEndIntent(
			persistCtx,
			RetryEndIntentParams{
				SessionID:  intent.SessionID,
				AccountID:  intent.AccountID,
				WorkerID:   requestOwner,
				LastError:  resultErr.Error(),
				RetryAfter: 0,
			},
		)
		if err != nil && !errors.Is(err, ErrConcurrentTransition) {
			resultErr = errors.Join(
				resultErr,
				fmt.Errorf("persist failed end request for recovery: %w", err),
			)
		}
	}()

	attemptCtx, cancel := s.endAttemptContext(ctx, leaseRemaining)
	defer cancel()

	switch session.Status {
	case StatusEnded, StatusFailed:
		return session, s.completeEndIntent(attemptCtx, session)
	case StatusCreated:
		return s.endCreated(attemptCtx, session, intent)
	case StatusActive:
		return s.stopAndEndActive(attemptCtx, session, intent, input.TraceID)
	default:
		return VoiceSession{}, ErrSessionStateConflict
	}
}

func validateEndInput(input EndInput) error {
	if err := validateIdentity(input.AccountID, input.SessionID); err != nil {
		return err
	}
	if err := validateIdempotency(input.IdempotencyKey, input.RequestHash); err != nil {
		return err
	}
	if input.TraceID == "" || !input.Reason.Valid() {
		return ErrInvalidRequest
	}
	return nil
}

func validateEndIntent(intent EndIntent, session VoiceSession, reason EndReason) error {
	if intent.SessionID != session.ID ||
		intent.AccountID != session.AccountID ||
		intent.IdempotencyKey == "" ||
		intent.RequestHash == "" ||
		intent.TraceID == "" ||
		intent.RequestedAt.IsZero() ||
		!intent.Reason.Valid() ||
		intent.Reason != reason ||
		(intent.CompletedAt != nil && intent.CompletedAt.IsZero()) {
		return fmt.Errorf("%w: invalid persisted end intent", ErrInvalidDependency)
	}
	return nil
}

func (s *Service) endCreated(
	ctx context.Context,
	session VoiceSession,
	intent EndIntent,
) (VoiceSession, error) {
	endedAt, err := s.nowUTC("created session end")
	if err != nil {
		return VoiceSession{}, err
	}
	ended, err := s.transitionCreatedToEnded(ctx, session, intent, endedAt)
	if err != nil {
		return VoiceSession{}, err
	}
	return ended, s.completeEndIntent(ctx, ended)
}

func (s *Service) transitionCreatedToEnded(
	ctx context.Context,
	session VoiceSession,
	intent EndIntent,
	endedAt time.Time,
) (VoiceSession, error) {
	ended, err := s.deps.Repository.TransitionToEnded(ctx, EndTransitionParams{
		SessionID: session.ID,
		AccountID: session.AccountID,
		Expected:  StatusCreated,
		EndedAt:   endedAt,
		EndReason: intent.Reason,
	})
	if err != nil {
		return VoiceSession{}, fmt.Errorf("end created voice session: %w", err)
	}
	return ended, nil
}

func (s *Service) stopAndEndActive(
	ctx context.Context,
	session VoiceSession,
	intent EndIntent,
	traceID string,
) (VoiceSession, error) {
	endedAt, err := s.nowUTC("active session end")
	if err != nil {
		return VoiceSession{}, err
	}
	ended, err := s.stopAndTransitionActive(ctx, session, intent, traceID, endedAt)
	if err != nil {
		return VoiceSession{}, err
	}
	return ended, s.completeEndIntent(ctx, ended)
}

func (s *Service) stopAndTransitionActive(
	ctx context.Context,
	session VoiceSession,
	intent EndIntent,
	traceID string,
	endedAt time.Time,
) (VoiceSession, error) {
	runtime, err := s.deps.Realtime.Stop(ctx, StopRealtimeCommand{
		SessionID: session.ID,
		TraceID:   traceID,
		Reason:    intent.Reason,
		EndedAt:   endedAt,
	})
	if err != nil {
		return VoiceSession{}, mapEndStopError(ctx, err)
	}
	if err := validateStoppedRuntime(runtime, session.ID); err != nil {
		return VoiceSession{}, err
	}

	ended, err := s.deps.Repository.TransitionToEnded(ctx, EndTransitionParams{
		SessionID: session.ID,
		AccountID: session.AccountID,
		Expected:  StatusActive,
		EndedAt:   endedAt,
		EndReason: intent.Reason,
	})
	if err != nil {
		return VoiceSession{}, fmt.Errorf("transition active voice session to ended: %w", err)
	}
	return ended, nil
}

func validateStoppedRuntime(runtime RuntimeSnapshot, sessionID string) error {
	if err := validateRuntimeSnapshot(runtime, sessionID); err != nil {
		return fmt.Errorf("%w: invalid stop snapshot", ErrRealtimeStopFailed)
	}
	if runtime.RuntimeState != RuntimeStopped {
		return fmt.Errorf(
			"%w: cleanup is not confirmed in runtime state %q",
			ErrRealtimeStopFailed,
			runtime.RuntimeState,
		)
	}
	return nil
}

func mapEndStopError(ctx context.Context, err error) error {
	if errors.Is(err, ErrNotImplemented) {
		return ErrNotImplemented
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("%w: %w", ErrRealtimeStopFailed, ctxErr)
	}
	return fmt.Errorf("%w: %w", ErrRealtimeStopFailed, err)
}

func (s *Service) completeEndIntent(
	ctx context.Context,
	session VoiceSession,
) error {
	completedAt, err := s.nowUTC("end intent completion")
	if err != nil {
		return err
	}
	if err := s.deps.Repository.CompleteEndIntent(
		ctx,
		session.AccountID,
		session.ID,
		completedAt,
	); err != nil {
		return fmt.Errorf("complete voice session end intent: %w", err)
	}
	return nil
}
