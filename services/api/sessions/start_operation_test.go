package sessions

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestStartOperationStatusValid(t *testing.T) {
	for _, status := range []StartOperationStatus{
		StartOperationPending,
		StartOperationCompensating,
		StartOperationCompleted,
		StartOperationCompensated,
		StartOperationCompensationFailed,
	} {
		t.Run(string(status), func(t *testing.T) {
			if !status.Valid() {
				t.Fatalf("%q.Valid() = false", status)
			}
		})
	}
	if StartOperationStatus("unknown").Valid() {
		t.Fatal("unknown StartOperationStatus is valid")
	}
}

func TestStartOperationMatchesRequest(t *testing.T) {
	operation := StartOperation{IdempotencyKey: "start_1", RequestHash: "hash_1"}
	if !operation.MatchesRequest("start_1", "hash_1") {
		t.Fatal("matching request was rejected")
	}
	if operation.MatchesRequest("start_1", "other") {
		t.Fatal("different request hash matched")
	}
}

func TestStartOperationRepositoryBeginsIdempotently(t *testing.T) {
	repository := newStartOperationRepository()
	params := validBeginStartOperationParams()

	first, err := repository.BeginStartOperation(context.Background(), params)
	if err != nil {
		t.Fatalf("BeginStartOperation() error = %v", err)
	}
	if first.Replayed || first.Operation.Status != StartOperationPending {
		t.Fatalf("first result = %#v", first)
	}

	replayParams := params
	replayParams.OperationID = "op_unused"
	second, err := repository.BeginStartOperation(context.Background(), replayParams)
	if err != nil {
		t.Fatalf("replayed BeginStartOperation() error = %v", err)
	}
	if !second.Replayed || second.Operation.ID != params.OperationID {
		t.Fatalf("replay result = %#v", second)
	}
}

func TestStartOperationRepositoryRejectsConflicts(t *testing.T) {
	tests := []struct {
		name string
		edit func(*BeginStartOperationParams)
		want error
	}{
		{
			name: "same key different hash",
			edit: func(params *BeginStartOperationParams) {
				params.RequestHash = "other"
			},
			want: ErrIdempotencyKeyConflict,
		},
		{
			name: "different pending operation",
			edit: func(params *BeginStartOperationParams) {
				params.OperationID = "op_2"
				params.IdempotencyKey = "start_2"
				params.RequestHash = "hash_2"
			},
			want: ErrSessionStartInProgress,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := newStartOperationRepository()
			if _, err := repository.BeginStartOperation(
				context.Background(),
				validBeginStartOperationParams(),
			); err != nil {
				t.Fatalf("initial BeginStartOperation() error = %v", err)
			}
			params := validBeginStartOperationParams()
			test.edit(&params)

			_, err := repository.BeginStartOperation(context.Background(), params)
			if !errors.Is(err, test.want) {
				t.Fatalf("BeginStartOperation() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestStartOperationRepositoryDoesNotBeginForActiveSession(t *testing.T) {
	repository := activatedStartOperationRepository(t)
	params := validBeginStartOperationParams()
	params.OperationID = "op_2"
	params.IdempotencyKey = "start_2"
	params.RequestHash = "hash_2"

	_, err := repository.BeginStartOperation(context.Background(), params)
	if !errors.Is(err, ErrConcurrentTransition) {
		t.Fatalf("BeginStartOperation() error = %v, want ErrConcurrentTransition", err)
	}
}

func TestStartOperationRepositoryCompletesActivationAtomically(t *testing.T) {
	repository := newStartOperationRepository()
	begin, err := repository.BeginStartOperation(
		context.Background(),
		validBeginStartOperationParams(),
	)
	if err != nil {
		t.Fatalf("BeginStartOperation() error = %v", err)
	}
	startedAt := begin.Operation.CreatedAt.Add(time.Second)

	session, replayed, err := repository.TransitionToActive(
		context.Background(),
		validStartOperationTransition(startedAt),
	)
	if err != nil {
		t.Fatalf("TransitionToActive() error = %v", err)
	}
	if replayed || session.Status != StatusActive {
		t.Fatalf("TransitionToActive() = %#v, replayed %t", session, replayed)
	}

	repository.mu.Lock()
	defer repository.mu.Unlock()
	if repository.operation.Status != StartOperationCompleted {
		t.Fatalf("operation status = %q, want completed", repository.operation.Status)
	}
	if repository.session.Status != StatusActive ||
		repository.session.StartedAt == nil ||
		!repository.session.StartedAt.Equal(startedAt) {
		t.Fatalf("stored session = %#v", repository.session)
	}
}

func TestStartOperationRepositoryReplaysCompletedActivation(t *testing.T) {
	repository := activatedStartOperationRepository(t)

	session, replayed, err := repository.TransitionToActive(
		context.Background(),
		validStartOperationTransition(time.Date(2026, 7, 28, 10, 2, 0, 0, time.UTC)),
	)
	if err != nil {
		t.Fatalf("TransitionToActive() error = %v", err)
	}
	if !replayed || session.Status != StatusActive {
		t.Fatalf("TransitionToActive() = %#v, replayed %t", session, replayed)
	}
}

func TestBeginStartOperationReplaysCompletedOperationForActiveSession(t *testing.T) {
	repository := activatedStartOperationRepository(t)
	params := validBeginStartOperationParams()
	params.OperationID = "op_unused"

	result, err := repository.BeginStartOperation(context.Background(), params)
	if err != nil {
		t.Fatalf("BeginStartOperation() error = %v", err)
	}
	if !result.Replayed ||
		result.Operation.ID != "op_1" ||
		result.Operation.Status != StartOperationCompleted {
		t.Fatalf("BeginStartOperation() = %#v", result)
	}
}

func TestBeginStartOperationRejectsDifferentRequestForActiveSession(t *testing.T) {
	tests := []struct {
		name string
		edit func(*BeginStartOperationParams)
		want error
	}{
		{
			name: "same key different hash",
			edit: func(params *BeginStartOperationParams) {
				params.RequestHash = "other"
			},
			want: ErrIdempotencyKeyConflict,
		},
		{
			name: "different key",
			edit: func(params *BeginStartOperationParams) {
				params.OperationID = "op_2"
				params.IdempotencyKey = "start_2"
				params.RequestHash = "hash_2"
			},
			want: ErrConcurrentTransition,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := activatedStartOperationRepository(t)
			params := validBeginStartOperationParams()
			test.edit(&params)

			_, err := repository.BeginStartOperation(context.Background(), params)
			if !errors.Is(err, test.want) {
				t.Fatalf("BeginStartOperation() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestBeginStartOperationRejectsCompensatedOperationReplay(t *testing.T) {
	repository := compensatedStartOperationRepository(t)

	result, err := repository.BeginStartOperation(
		context.Background(),
		validBeginStartOperationParams(),
	)
	if !errors.Is(err, ErrIdempotencyKeyConflict) {
		t.Fatalf("BeginStartOperation() error = %v, want ErrIdempotencyKeyConflict", err)
	}
	if result.Replayed {
		t.Fatalf("BeginStartOperation() replayed = true")
	}
}

func TestBeginStartOperationAllowsNewRequestAfterCompensation(t *testing.T) {
	repository := compensatedStartOperationRepository(t)
	params := validBeginStartOperationParams()
	params.OperationID = "op_2"
	params.IdempotencyKey = "start_2"
	params.RequestHash = "hash_2"

	result, err := repository.BeginStartOperation(context.Background(), params)
	if err != nil {
		t.Fatalf("BeginStartOperation() error = %v", err)
	}
	if result.Replayed ||
		result.Operation.ID != "op_2" ||
		result.Operation.Status != StartOperationPending {
		t.Fatalf("BeginStartOperation() = %#v", result)
	}
}

func TestBeginStartOperationRejectsCompensationFailedReplay(t *testing.T) {
	repository := compensationFailedStartOperationRepository(t)

	result, err := repository.BeginStartOperation(
		context.Background(),
		validBeginStartOperationParams(),
	)
	if !errors.Is(err, ErrSessionStartInProgress) {
		t.Fatalf("BeginStartOperation() error = %v, want ErrSessionStartInProgress", err)
	}
	if result.Replayed {
		t.Fatalf("BeginStartOperation() replayed = true")
	}
}

func TestBeginStartOperationRejectsNewRequestWhileCompensationFailed(t *testing.T) {
	repository := compensationFailedStartOperationRepository(t)
	params := validBeginStartOperationParams()
	params.OperationID = "op_2"
	params.IdempotencyKey = "start_2"
	params.RequestHash = "hash_2"

	_, err := repository.BeginStartOperation(context.Background(), params)
	if !errors.Is(err, ErrSessionStartInProgress) {
		t.Fatalf("BeginStartOperation() error = %v, want ErrSessionStartInProgress", err)
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if repository.operation.ID != "op_1" ||
		repository.operation.Status != StartOperationCompensationFailed {
		t.Fatalf("operation = %#v", repository.operation)
	}
}

func TestTransitionToActiveRejectsReplayHashConflict(t *testing.T) {
	repository := activatedStartOperationRepository(t)
	params := validStartOperationTransition(time.Date(2026, 7, 28, 10, 2, 0, 0, time.UTC))
	params.RequestHash = "other"

	_, replayed, err := repository.TransitionToActive(context.Background(), params)
	if !errors.Is(err, ErrIdempotencyKeyConflict) {
		t.Fatalf("TransitionToActive() error = %v, want ErrIdempotencyKeyConflict", err)
	}
	if replayed {
		t.Fatal("TransitionToActive() replayed = true")
	}
}

func TestStartOperationRepositoryDeniesCompensationAfterActivation(t *testing.T) {
	repository := activatedStartOperationRepository(t)

	claim, err := repository.ClaimStartCompensation(
		context.Background(),
		validClaimStartCompensationParams(),
	)
	if err != nil {
		t.Fatalf("ClaimStartCompensation() error = %v", err)
	}
	if claim.Claimed || claim.Reason != StartCompensationSessionNotCreated {
		t.Fatalf("claim = %#v", claim)
	}
}

func TestStartOperationRepositoryRejectsDifferentClaimOwner(t *testing.T) {
	repository := claimedStartOperationRepository(t)
	secondParams := validClaimStartCompensationParams()
	secondParams.ClaimID = "claim_2"
	second, err := repository.ClaimStartCompensation(context.Background(), secondParams)
	if err != nil {
		t.Fatalf("second ClaimStartCompensation() error = %v", err)
	}
	if second.Claimed || second.Reason != StartCompensationOperationNotPending {
		t.Fatalf("second claim = %#v", second)
	}
	assertStartOperationState(
		t,
		repository,
		StartOperationCompensating,
		"claim_1",
		StatusCreated,
	)
}

func TestStartOperationRepositoryAllowsClaimOwnerReentry(t *testing.T) {
	repository := claimedStartOperationRepository(t)
	firstClaimedAt := validClaimStartCompensationParams().ClaimedAt
	params := validClaimStartCompensationParams()
	params.ClaimedAt = firstClaimedAt.Add(time.Minute)

	claim, err := repository.ClaimStartCompensation(context.Background(), params)
	if err != nil {
		t.Fatalf("ClaimStartCompensation() error = %v", err)
	}
	if !claim.Claimed {
		t.Fatalf("claim = %#v", claim)
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if repository.operation.Status != StartOperationCompensating ||
		repository.operation.CompensationClaimID == nil ||
		*repository.operation.CompensationClaimID != "claim_1" ||
		!repository.operation.UpdatedAt.Equal(firstClaimedAt) {
		t.Fatalf("operation = %#v", repository.operation)
	}
}

func TestStartOperationRepositoryCompletesAfterClaimOwnerReentry(t *testing.T) {
	repository := reenteredStartOperationRepository(t)
	completedAt := time.Date(2026, 7, 28, 10, 3, 0, 0, time.UTC)

	err := repository.CompleteStartCompensation(
		context.Background(),
		CompleteStartCompensationParams{
			SessionID: "vs_1", AccountID: "acct_1",
			OperationID: "op_1", ClaimID: "claim_1", CompletedAt: completedAt,
		},
	)
	if err != nil {
		t.Fatalf("CompleteStartCompensation() error = %v", err)
	}
	assertStartOperationState(
		t,
		repository,
		StartOperationCompensated,
		"claim_1",
		StatusCreated,
	)
}

func TestStartOperationRepositoryFailsAfterClaimOwnerReentry(t *testing.T) {
	repository := reenteredStartOperationRepository(t)
	failedAt := time.Date(2026, 7, 28, 10, 3, 0, 0, time.UTC)

	err := repository.FailStartCompensation(
		context.Background(),
		FailStartCompensationParams{
			SessionID: "vs_1", AccountID: "acct_1",
			OperationID: "op_1", ClaimID: "claim_1", FailedAt: failedAt,
		},
	)
	if err != nil {
		t.Fatalf("FailStartCompensation() error = %v", err)
	}
	assertStartOperationState(
		t,
		repository,
		StartOperationCompensationFailed,
		"claim_1",
		StatusCreated,
	)
}

func TestStartOperationRepositoryRejectsClaimForTerminalStates(t *testing.T) {
	tests := []struct {
		name       string
		repository func(*testing.T) *startOperationRepository
		status     StartOperationStatus
		claimID    string
		session    Status
	}{
		{name: "completed", repository: activatedStartOperationRepository, status: StartOperationCompleted, session: StatusActive},
		{name: "compensated", repository: compensatedStartOperationRepository, status: StartOperationCompensated, claimID: "claim_1", session: StatusCreated},
		{name: "compensation failed", repository: compensationFailedStartOperationRepository, status: StartOperationCompensationFailed, claimID: "claim_1", session: StatusCreated},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := test.repository(t)

			claim, err := repository.ClaimStartCompensation(
				context.Background(),
				validClaimStartCompensationParams(),
			)
			if err != nil {
				t.Fatalf("ClaimStartCompensation() error = %v", err)
			}
			if claim.Claimed {
				t.Fatalf("claim = %#v", claim)
			}
			assertStartOperationState(
				t,
				repository,
				test.status,
				test.claimID,
				test.session,
			)
		})
	}
}

func TestStartOperationRepositoryRejectsMismatchedOperationClaim(t *testing.T) {
	repository := pendingStartOperationRepository(t)
	params := validClaimStartCompensationParams()
	params.OperationID = "op_other"

	claim, err := repository.ClaimStartCompensation(context.Background(), params)
	if err != nil {
		t.Fatalf("ClaimStartCompensation() error = %v", err)
	}
	if claim.Claimed || claim.Reason != StartCompensationOperationMismatch {
		t.Fatalf("claim = %#v", claim)
	}
}

func TestStartOperationRepositoryRecordsCompensationOutcome(t *testing.T) {
	tests := []struct {
		name string
		run  func(*startOperationRepository, time.Time) error
		want StartOperationStatus
	}{
		{
			name: "completed",
			run: func(repository *startOperationRepository, at time.Time) error {
				return repository.CompleteStartCompensation(
					context.Background(),
					CompleteStartCompensationParams{
						SessionID: "vs_1", AccountID: "acct_1",
						OperationID: "op_1", ClaimID: "claim_1", CompletedAt: at,
					},
				)
			},
			want: StartOperationCompensated,
		},
		{
			name: "failed",
			run: func(repository *startOperationRepository, at time.Time) error {
				return repository.FailStartCompensation(
					context.Background(),
					FailStartCompensationParams{
						SessionID: "vs_1", AccountID: "acct_1",
						OperationID: "op_1", ClaimID: "claim_1", FailedAt: at,
					},
				)
			},
			want: StartOperationCompensationFailed,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := pendingStartOperationRepository(t)
			if claim, err := repository.ClaimStartCompensation(
				context.Background(),
				validClaimStartCompensationParams(),
			); err != nil || !claim.Claimed {
				t.Fatalf("ClaimStartCompensation() = %#v, %v", claim, err)
			}
			at := time.Date(2026, 7, 28, 10, 2, 0, 0, time.UTC)

			if err := test.run(repository, at); err != nil {
				t.Fatalf("record compensation outcome error = %v", err)
			}
			repository.mu.Lock()
			if repository.operation.Status != test.want ||
				!repository.operation.UpdatedAt.Equal(at) ||
				repository.session.Status != StatusCreated {
				t.Fatalf("operation = %#v, session = %#v",
					repository.operation, repository.session)
			}
			repository.mu.Unlock()
		})
	}
}

func TestStartOperationRepositoryRejectsCompensationOutcomeFromNonOwner(t *testing.T) {
	repository := pendingStartOperationRepository(t)
	if claim, err := repository.ClaimStartCompensation(
		context.Background(),
		validClaimStartCompensationParams(),
	); err != nil || !claim.Claimed {
		t.Fatalf("ClaimStartCompensation() = %#v, %v", claim, err)
	}

	err := repository.CompleteStartCompensation(
		context.Background(),
		CompleteStartCompensationParams{
			SessionID: "vs_1", AccountID: "acct_1",
			OperationID: "op_1", ClaimID: "claim_other",
			CompletedAt: time.Date(2026, 7, 28, 10, 2, 0, 0, time.UTC),
		},
	)
	if !errors.Is(err, ErrConcurrentTransition) {
		t.Fatalf("CompleteStartCompensation() error = %v, want ErrConcurrentTransition", err)
	}
}

func validBeginStartOperationParams() BeginStartOperationParams {
	return BeginStartOperationParams{
		OperationID: "op_1", SessionID: "vs_1", AccountID: "acct_1",
		IdempotencyKey: "start_1", RequestHash: "hash_1",
		CreatedAt: time.Date(2026, 7, 28, 10, 0, 0, 0, time.UTC),
	}
}

func validClaimStartCompensationParams() ClaimStartCompensationParams {
	return ClaimStartCompensationParams{
		SessionID: "vs_1", AccountID: "acct_1", OperationID: "op_1",
		ClaimID: "claim_1", ClaimedAt: time.Date(2026, 7, 28, 10, 1, 0, 0, time.UTC),
	}
}

func validStartOperationTransition(startedAt time.Time) StartTransitionParams {
	return StartTransitionParams{
		SessionID: "vs_1", AccountID: "acct_1", OperationID: "op_1",
		Expected: StatusCreated, StartedAt: startedAt,
		IdempotencyKey: "start_1", RequestHash: "hash_1",
	}
}

func pendingStartOperationRepository(t *testing.T) *startOperationRepository {
	t.Helper()
	repository := newStartOperationRepository()
	if _, err := repository.BeginStartOperation(
		context.Background(),
		validBeginStartOperationParams(),
	); err != nil {
		t.Fatalf("BeginStartOperation() error = %v", err)
	}
	return repository
}

func claimedStartOperationRepository(t *testing.T) *startOperationRepository {
	t.Helper()
	repository := pendingStartOperationRepository(t)
	claim, err := repository.ClaimStartCompensation(
		context.Background(),
		validClaimStartCompensationParams(),
	)
	if err != nil || !claim.Claimed {
		t.Fatalf("ClaimStartCompensation() = %#v, %v", claim, err)
	}
	return repository
}

func reenteredStartOperationRepository(t *testing.T) *startOperationRepository {
	t.Helper()
	repository := claimedStartOperationRepository(t)
	claim, err := repository.ClaimStartCompensation(
		context.Background(),
		validClaimStartCompensationParams(),
	)
	if err != nil || !claim.Claimed {
		t.Fatalf("reentered ClaimStartCompensation() = %#v, %v", claim, err)
	}
	return repository
}

func assertStartOperationState(
	t *testing.T,
	repository *startOperationRepository,
	wantStatus StartOperationStatus,
	wantClaimID string,
	wantSessionStatus Status,
) {
	t.Helper()
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if repository.operation.Status != wantStatus ||
		repository.session.Status != wantSessionStatus {
		t.Fatalf("operation = %#v, session = %#v", repository.operation, repository.session)
	}
	if wantClaimID == "" {
		if repository.operation.CompensationClaimID != nil {
			t.Fatalf("CompensationClaimID = %q, want nil",
				*repository.operation.CompensationClaimID)
		}
		return
	}
	if repository.operation.CompensationClaimID == nil ||
		*repository.operation.CompensationClaimID != wantClaimID {
		t.Fatalf("CompensationClaimID = %#v, want %q",
			repository.operation.CompensationClaimID, wantClaimID)
	}
}

func activatedStartOperationRepository(t *testing.T) *startOperationRepository {
	t.Helper()
	repository := pendingStartOperationRepository(t)
	_, _, err := repository.TransitionToActive(
		context.Background(),
		validStartOperationTransition(time.Date(2026, 7, 28, 10, 1, 0, 0, time.UTC)),
	)
	if err != nil {
		t.Fatalf("TransitionToActive() error = %v", err)
	}
	return repository
}

func compensatedStartOperationRepository(t *testing.T) *startOperationRepository {
	t.Helper()
	repository := claimedStartOperationRepository(t)
	if err := repository.CompleteStartCompensation(
		context.Background(),
		CompleteStartCompensationParams{
			SessionID: "vs_1", AccountID: "acct_1",
			OperationID: "op_1", ClaimID: "claim_1",
			CompletedAt: time.Date(2026, 7, 28, 10, 2, 0, 0, time.UTC),
		},
	); err != nil {
		t.Fatalf("CompleteStartCompensation() error = %v", err)
	}
	return repository
}

func compensationFailedStartOperationRepository(t *testing.T) *startOperationRepository {
	t.Helper()
	repository := claimedStartOperationRepository(t)
	if err := repository.FailStartCompensation(
		context.Background(),
		FailStartCompensationParams{
			SessionID: "vs_1", AccountID: "acct_1",
			OperationID: "op_1", ClaimID: "claim_1",
			FailedAt: time.Date(2026, 7, 28, 10, 2, 0, 0, time.UTC),
		},
	); err != nil {
		t.Fatalf("FailStartCompensation() error = %v", err)
	}
	return repository
}
