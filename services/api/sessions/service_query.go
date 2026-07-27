package sessions

import (
	"context"
	"errors"
	"fmt"
)

const (
	defaultListLimit = 20
	maxListLimit     = 100
)

// GetDetail combines one account-scoped persistent read with one runtime
// snapshot. Missing snapshots are synthesized as stopped only when the
// persistent business state makes their absence valid.
func (s *Service) GetDetail(ctx context.Context, input DetailInput) (VoiceSessionDetail, error) {
	session, runtime, err := s.readOwnedWithRuntime(ctx, input)
	if err != nil {
		return VoiceSessionDetail{}, err
	}
	return VoiceSessionDetail{
		VoiceSession:      session,
		RuntimeState:      runtime.RuntimeState,
		CurrentTurnID:     runtime.CurrentTurnID,
		CurrentPlaybackID: runtime.CurrentPlaybackID,
		LastErrorCode:     runtime.LastErrorCode,
		Retryable:         Retryable(session.Status, runtime.RuntimeState),
		RuntimeUpdatedAt:  runtime.UpdatedAt,
	}, nil
}

// GetState returns the compact polling projection from the same authoritative
// sources and applies the same business-state rules for missing snapshots.
func (s *Service) GetState(ctx context.Context, input DetailInput) (StateSnapshot, error) {
	session, runtime, err := s.readOwnedWithRuntime(ctx, input)
	if err != nil {
		return StateSnapshot{}, err
	}
	return StateSnapshot{
		SessionID:         session.ID,
		Status:            session.Status,
		RuntimeState:      runtime.RuntimeState,
		CurrentTurnID:     runtime.CurrentTurnID,
		CurrentPlaybackID: runtime.CurrentPlaybackID,
		LastErrorCode:     runtime.LastErrorCode,
		Retryable:         Retryable(session.Status, runtime.RuntimeState),
		RuntimeUpdatedAt:  runtime.UpdatedAt,
	}, nil
}

// List reads persistent projections only. Realtime is deliberately absent from
// this path so list queries cannot produce cross-service N+1 calls.
func (s *Service) List(ctx context.Context, input ListInput) (ListPage, error) {
	if err := ctx.Err(); err != nil {
		return ListPage{}, err
	}
	if input.AccountID == "" {
		return ListPage{}, ErrUnauthorized
	}
	if input.Status != nil && !input.Status.Valid() {
		return ListPage{}, ErrInvalidRequest
	}
	if input.Limit == 0 {
		input.Limit = defaultListLimit
	}
	if input.Limit < 1 || input.Limit > maxListLimit {
		return ListPage{}, ErrInvalidRequest
	}

	page, err := s.deps.Repository.List(ctx, ListFilter{
		AccountID: input.AccountID,
		Status:    input.Status,
		Cursor:    input.Cursor,
		Limit:     input.Limit,
	})
	if err != nil {
		return ListPage{}, fmt.Errorf("list voice sessions: %w", err)
	}
	return page, nil
}

func (s *Service) readOwnedWithRuntime(
	ctx context.Context,
	input DetailInput,
) (VoiceSession, RuntimeSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return VoiceSession{}, RuntimeSnapshot{}, err
	}
	if err := validateIdentity(input.AccountID, input.SessionID); err != nil {
		return VoiceSession{}, RuntimeSnapshot{}, err
	}

	session, err := s.deps.Repository.GetOwned(ctx, input.AccountID, input.SessionID)
	if err != nil {
		return VoiceSession{}, RuntimeSnapshot{}, fmt.Errorf("read owned voice session: %w", err)
	}

	runtime, err := s.readRuntimeForSession(ctx, session)
	if err != nil {
		return VoiceSession{}, RuntimeSnapshot{}, err
	}
	return session, runtime, nil
}

func (s *Service) readRuntimeForSession(
	ctx context.Context,
	session VoiceSession,
) (RuntimeSnapshot, error) {
	snapshot, err := s.deps.Realtime.GetRuntimeState(ctx, session.ID)
	if errors.Is(err, ErrRuntimeSnapshotNotFound) {
		return synthesizeMissingRuntime(session)
	}
	if err != nil {
		return RuntimeSnapshot{}, mapDependencyError(ctx, err, ErrRuntimeUnavailable)
	}
	if err := validateRuntimeSnapshot(snapshot, session.ID); err != nil {
		return RuntimeSnapshot{}, err
	}
	return snapshot, nil
}

// synthesizeMissingRuntime interprets an explicitly absent runtime record only
// for business states where no active media resources are expected.
func synthesizeMissingRuntime(session VoiceSession) (RuntimeSnapshot, error) {
	switch session.Status {
	case StatusCreated:
		if session.CreatedAt.IsZero() {
			return RuntimeSnapshot{}, ErrRuntimeUnavailable
		}
		return RuntimeSnapshot{
			SessionID:    session.ID,
			RuntimeState: RuntimeStopped,
			UpdatedAt:    session.CreatedAt.UTC(),
		}, nil
	case StatusEnded:
		if session.EndedAt == nil || session.EndedAt.IsZero() {
			return RuntimeSnapshot{}, ErrRuntimeUnavailable
		}
		return RuntimeSnapshot{
			SessionID:    session.ID,
			RuntimeState: RuntimeStopped,
			UpdatedAt:    session.EndedAt.UTC(),
		}, nil
	default:
		return RuntimeSnapshot{}, ErrRuntimeUnavailable
	}
}
