package sessions

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// EndRecoveryConfig controls one API instance's durable EndIntent scanner.
// WorkerID must be unique among concurrently running instances.
type EndRecoveryConfig struct {
	WorkerID       string
	PollInterval   time.Duration
	LeaseDuration  time.Duration
	AttemptTimeout time.Duration
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
}

// EndRecoveryWorker resumes incomplete End requests using the owning Service
// so request and recovery paths share the same per-session process lock.
type EndRecoveryWorker struct {
	service *Service
	config  EndRecoveryConfig
}

// NewEndRecoveryWorker validates that every recovery attempt is canceled
// before its durable lease can expire and returns one single-owner loop.
func NewEndRecoveryWorker(
	service *Service,
	config EndRecoveryConfig,
) (*EndRecoveryWorker, error) {
	if service == nil || config.WorkerID == "" ||
		config.PollInterval <= 0 || config.LeaseDuration <= 0 ||
		config.AttemptTimeout <= 0 ||
		config.AttemptTimeout >= config.LeaseDuration ||
		config.InitialBackoff <= 0 ||
		config.MaxBackoff < config.InitialBackoff {
		return nil, ErrInvalidDependency
	}
	return &EndRecoveryWorker{service: service, config: config}, nil
}

// Run drains due intents, then waits for the next scan interval. Cancellation
// stops the loop and leaves any in-flight claim recoverable after lease expiry.
func (w *EndRecoveryWorker) Run(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalidDependency
	}
	for {
		processed, err := w.ProcessNext(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			w.service.deps.Logger.WarnContext(ctx, "end recovery attempt failed", "error", err)
		}
		if processed {
			continue
		}

		timer := time.NewTimer(w.config.PollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}

// ProcessNext claims and processes at most one due intent. The bool reports
// whether an intent was claimed, allowing deterministic one-step tests.
func (w *EndRecoveryWorker) ProcessNext(ctx context.Context) (bool, error) {
	if ctx == nil {
		return false, ErrInvalidRequest
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	claimedAt, err := w.service.nowUTC("end recovery claim")
	if err != nil {
		return false, err
	}
	leaseStartedAt := time.Now()
	intent, claimed, err := w.service.deps.Repository.ClaimPendingEndIntent(
		ctx,
		ClaimEndIntentParams{
			WorkerID:       w.config.WorkerID,
			ClaimedAt:      claimedAt,
			LeaseExpiresAt: claimedAt.Add(w.config.LeaseDuration),
		},
	)
	if err != nil {
		return false, fmt.Errorf("claim pending end intent: %w", err)
	}
	if !claimed {
		return false, nil
	}

	if intent.RecoveryOwner == nil || *intent.RecoveryOwner != w.config.WorkerID ||
		intent.LeaseExpiresAt == nil {
		return true, fmt.Errorf("%w: invalid end recovery claim", ErrInvalidDependency)
	}
	leaseRemaining := w.config.LeaseDuration - time.Since(leaseStartedAt)
	if leaseRemaining <= 0 {
		return true, ErrConcurrentTransition
	}
	attemptCtx, cancel := context.WithTimeout(
		ctx,
		min(w.config.AttemptTimeout, leaseRemaining),
	)
	err = w.recoverClaimed(attemptCtx, intent)
	cancel()
	if err == nil {
		return true, nil
	}
	retryAfter := endRecoveryBackoff(
		w.config.InitialBackoff,
		w.config.MaxBackoff,
		intent.RetryCount,
	)
	retryErr := w.service.deps.Repository.RetryClaimedEndIntent(
		ctx,
		RetryEndIntentParams{
			SessionID:  intent.SessionID,
			AccountID:  intent.AccountID,
			WorkerID:   w.config.WorkerID,
			LastError:  err.Error(),
			RetryAfter: retryAfter,
		},
	)
	if retryErr != nil {
		return true, errors.Join(err, fmt.Errorf("persist end recovery retry: %w", retryErr))
	}
	return true, fmt.Errorf(
		"recover end intent session=%q trace=%q retry=%d retry_after=%s: %w",
		intent.SessionID,
		intent.TraceID,
		intent.RetryCount+1,
		retryAfter,
		err,
	)
}

func (w *EndRecoveryWorker) recoverClaimed(
	ctx context.Context,
	intent EndIntent,
) error {
	unlock, err := w.service.locks.lock(ctx, intent.SessionID)
	if err != nil {
		return err
	}
	defer unlock()

	session, err := w.service.deps.Repository.GetOwned(
		ctx, intent.AccountID, intent.SessionID,
	)
	if err != nil {
		return fmt.Errorf("read voice session for end recovery: %w", err)
	}
	if err := validateRecoveryEndIntent(
		intent, session, w.config.WorkerID,
	); err != nil {
		return err
	}
	if session.Status == StatusEnded || session.Status == StatusFailed {
		return w.completeClaimed(ctx, intent)
	}

	endedAt, err := w.service.nowUTC("recovered session end")
	if err != nil {
		return err
	}
	switch session.Status {
	case StatusCreated:
		_, err = w.service.transitionCreatedToEnded(ctx, session, intent, endedAt)
	case StatusActive:
		_, err = w.service.stopAndTransitionActive(
			ctx, session, intent, intent.TraceID, endedAt,
		)
	default:
		return ErrSessionStateConflict
	}
	if err != nil {
		return w.reconcileTransition(ctx, intent, err)
	}
	return w.completeClaimed(ctx, intent)
}

func validateRecoveryEndIntent(
	intent EndIntent,
	session VoiceSession,
	workerID string,
) error {
	if intent.SessionID != session.ID || intent.AccountID != session.AccountID ||
		intent.TraceID == "" || intent.IdempotencyKey == "" ||
		intent.RequestHash == "" || !intent.Reason.Valid() ||
		intent.RequestedAt.IsZero() || intent.NextAttemptAt.IsZero() ||
		intent.RetryCount < 0 || intent.Completed() ||
		intent.RecoveryOwner == nil || *intent.RecoveryOwner != workerID ||
		intent.LeaseExpiresAt == nil {
		return fmt.Errorf("%w: invalid claimed end intent", ErrInvalidDependency)
	}
	return nil
}

func (w *EndRecoveryWorker) reconcileTransition(
	ctx context.Context,
	intent EndIntent,
	transitionErr error,
) error {
	if !errors.Is(transitionErr, ErrConcurrentTransition) {
		return transitionErr
	}
	session, err := w.service.deps.Repository.GetOwned(
		ctx, intent.AccountID, intent.SessionID,
	)
	if err != nil {
		return errors.Join(transitionErr, fmt.Errorf("reconcile end recovery: %w", err))
	}
	if session.Status == StatusEnded || session.Status == StatusFailed {
		return w.completeClaimed(ctx, intent)
	}
	return transitionErr
}

func (w *EndRecoveryWorker) completeClaimed(
	ctx context.Context,
	intent EndIntent,
) error {
	completedAt, err := w.service.nowUTC("end recovery completion")
	if err != nil {
		return err
	}
	if err := w.service.deps.Repository.CompleteClaimedEndIntent(
		ctx,
		CompleteClaimedEndIntentParams{
			SessionID:   intent.SessionID,
			AccountID:   intent.AccountID,
			WorkerID:    w.config.WorkerID,
			CompletedAt: completedAt,
		},
	); err != nil {
		return fmt.Errorf("complete recovered end intent: %w", err)
	}
	w.service.deps.Logger.InfoContext(ctx, "completed end recovery",
		"session_id", intent.SessionID,
		"trace_id", intent.TraceID,
		"retry_count", intent.RetryCount,
	)
	return nil
}

func endRecoveryBackoff(initial time.Duration, maximum time.Duration, retryCount int) time.Duration {
	delay := initial
	for range retryCount {
		if delay >= maximum/2 {
			return maximum
		}
		delay *= 2
	}
	return min(delay, maximum)
}
