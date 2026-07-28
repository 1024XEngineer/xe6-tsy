package sessions

import (
	"context"
	"sync"
	"time"
)

// startOperationRepository is a deterministic contract fake. Its single mutex
// models the transaction boundary required of production implementations.
type startOperationRepository struct {
	mu        sync.Mutex
	session   VoiceSession
	operation *StartOperation
}

func newStartOperationRepository() *startOperationRepository {
	return &startOperationRepository{
		session: VoiceSession{ID: "vs_1", AccountID: "acct_1", Status: StatusCreated},
	}
}

func (*startOperationRepository) Create(
	context.Context,
	CreateParams,
) (VoiceSession, bool, error) {
	return VoiceSession{}, false, ErrNotImplemented
}

func (r *startOperationRepository) GetOwned(
	_ context.Context,
	accountID string,
	sessionID string,
) (VoiceSession, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.session.AccountID != accountID || r.session.ID != sessionID {
		return VoiceSession{}, ErrVoiceSessionNotFound
	}
	return r.session, nil
}

func (*startOperationRepository) List(context.Context, ListFilter) (ListPage, error) {
	return ListPage{}, ErrNotImplemented
}

func (r *startOperationRepository) BeginStartOperation(
	_ context.Context,
	params BeginStartOperationParams,
) (BeginStartOperationResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.session.ID != params.SessionID || r.session.AccountID != params.AccountID {
		return BeginStartOperationResult{}, ErrVoiceSessionNotFound
	}
	if r.session.Status != StatusCreated {
		return BeginStartOperationResult{}, ErrConcurrentTransition
	}
	if r.operation != nil {
		if r.operation.IdempotencyKey == params.IdempotencyKey {
			if r.operation.RequestHash != params.RequestHash {
				return BeginStartOperationResult{}, ErrIdempotencyKeyConflict
			}
			return BeginStartOperationResult{Operation: *r.operation, Replayed: true}, nil
		}
		if r.operation.Status != StartOperationCompensated {
			return BeginStartOperationResult{}, ErrSessionStartInProgress
		}
	}
	operation := StartOperation{
		ID: params.OperationID, SessionID: params.SessionID, AccountID: params.AccountID,
		IdempotencyKey: params.IdempotencyKey, RequestHash: params.RequestHash,
		Status: StartOperationPending, CreatedAt: params.CreatedAt, UpdatedAt: params.CreatedAt,
	}
	r.operation = &operation
	return BeginStartOperationResult{Operation: operation}, nil
}

func (r *startOperationRepository) ClaimStartCompensation(
	_ context.Context,
	params ClaimStartCompensationParams,
) (ClaimStartCompensationResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.session.ID != params.SessionID || r.session.AccountID != params.AccountID {
		return ClaimStartCompensationResult{}, ErrVoiceSessionNotFound
	}
	if r.session.Status != StatusCreated {
		return ClaimStartCompensationResult{
			Reason: StartCompensationSessionNotCreated,
		}, nil
	}
	if r.operation == nil || r.operation.ID != params.OperationID {
		return ClaimStartCompensationResult{
			Reason: StartCompensationOperationMismatch,
		}, nil
	}
	if r.operation.Status != StartOperationPending {
		return ClaimStartCompensationResult{
			Reason: StartCompensationOperationNotPending,
		}, nil
	}
	r.operation.Status = StartOperationCompensating
	r.operation.CompensationClaimID = &params.ClaimID
	r.operation.UpdatedAt = params.ClaimedAt
	return ClaimStartCompensationResult{Claimed: true}, nil
}

func (r *startOperationRepository) CompleteStartCompensation(
	_ context.Context,
	params CompleteStartCompensationParams,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.ownsCompensation(
		params.AccountID,
		params.SessionID,
		params.OperationID,
		params.ClaimID,
	) {
		return ErrConcurrentTransition
	}
	r.operation.Status = StartOperationCompensated
	r.operation.UpdatedAt = params.CompletedAt
	return nil
}

func (r *startOperationRepository) FailStartCompensation(
	_ context.Context,
	params FailStartCompensationParams,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.ownsCompensation(
		params.AccountID,
		params.SessionID,
		params.OperationID,
		params.ClaimID,
	) {
		return ErrConcurrentTransition
	}
	r.operation.Status = StartOperationCompensationFailed
	r.operation.UpdatedAt = params.FailedAt
	return nil
}

func (r *startOperationRepository) ownsCompensation(
	accountID string,
	sessionID string,
	operationID string,
	claimID string,
) bool {
	return r.session.AccountID == accountID &&
		r.session.ID == sessionID &&
		r.session.Status == StatusCreated &&
		r.operation != nil &&
		r.operation.ID == operationID &&
		r.operation.Status == StartOperationCompensating &&
		r.operation.CompensationClaimID != nil &&
		*r.operation.CompensationClaimID == claimID
}

func (*startOperationRepository) SaveEndIntent(
	context.Context,
	EndIntent,
) (EndIntent, bool, error) {
	return EndIntent{}, false, ErrNotImplemented
}

func (*startOperationRepository) GetEndIntent(
	context.Context,
	string,
	string,
) (EndIntent, error) {
	return EndIntent{}, ErrNotImplemented
}

func (*startOperationRepository) CompleteEndIntent(
	context.Context,
	string,
	string,
	time.Time,
) error {
	return ErrNotImplemented
}

func (r *startOperationRepository) TransitionToActive(
	_ context.Context,
	params StartTransitionParams,
) (VoiceSession, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.session.ID != params.SessionID || r.session.AccountID != params.AccountID {
		return VoiceSession{}, false, ErrVoiceSessionNotFound
	}
	if r.session.Status == StatusActive {
		if r.operation != nil &&
			r.operation.Status == StartOperationCompleted &&
			r.operation.ID == params.OperationID &&
			r.operation.MatchesRequest(params.IdempotencyKey, params.RequestHash) {
			return r.session, true, nil
		}
		return VoiceSession{}, false, ErrConcurrentTransition
	}
	if r.session.Status != params.Expected ||
		r.operation == nil ||
		r.operation.ID != params.OperationID ||
		r.operation.Status != StartOperationPending {
		return VoiceSession{}, false, ErrConcurrentTransition
	}
	if !r.operation.MatchesRequest(params.IdempotencyKey, params.RequestHash) {
		return VoiceSession{}, false, ErrIdempotencyKeyConflict
	}
	r.session.Status = StatusActive
	r.session.StartedAt = &params.StartedAt
	r.operation.Status = StartOperationCompleted
	r.operation.UpdatedAt = params.StartedAt
	return r.session, false, nil
}

func (*startOperationRepository) TransitionToEnded(
	context.Context,
	EndTransitionParams,
) (VoiceSession, error) {
	return VoiceSession{}, ErrNotImplemented
}

func (*startOperationRepository) TransitionToFailed(
	context.Context,
	FailureTransitionParams,
) (VoiceSession, error) {
	return VoiceSession{}, ErrNotImplemented
}
