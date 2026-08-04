package sessions

import (
	"context"
	"fmt"
)

// ConsumeRuntimeFailure records a cleaned-up, unrecoverable media failure as
// the terminal business state. Realtime owns the cleanup confirmation before
// invoking this trusted internal boundary.
func (s *Service) ConsumeRuntimeFailure(
	ctx context.Context,
	failure RuntimeFailure,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if failure.SessionID == "" || failure.TraceID == "" ||
		failure.ErrorCode == "" || failure.OccurredAt.IsZero() {
		return ErrInvalidRequest
	}

	unlock, err := s.locks.lock(ctx, failure.SessionID)
	if err != nil {
		return err
	}
	defer unlock()

	session, err := s.deps.Repository.GetSession(ctx, failure.SessionID)
	if err != nil {
		return fmt.Errorf("read voice session for runtime failure: %w", err)
	}
	switch session.Status {
	case StatusEnded, StatusFailed:
		return nil
	case StatusActive:
		// Continue below.
	default:
		return ErrSessionStateConflict
	}

	failed, err := s.deps.Repository.TransitionToFailed(ctx, FailureTransitionParams{
		SessionID: failure.SessionID,
		AccountID: session.AccountID,
		Expected:  StatusActive,
		FailedAt:  failure.OccurredAt.UTC(),
		ErrorCode: failure.ErrorCode,
	})
	if err != nil {
		return fmt.Errorf("transition active voice session to failed: %w", err)
	}
	s.deps.Logger.InfoContext(ctx, "recorded cleaned runtime failure",
		"trace_id", failure.TraceID,
		"session_id", failure.SessionID,
		"error_code", failure.ErrorCode,
		"status", failed.Status,
	)
	return nil
}

var _ RuntimeFailureConsumer = (*Service)(nil)
