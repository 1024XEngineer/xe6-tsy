package sessions

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type endRepository struct {
	*startRepository

	endMu sync.Mutex

	intent          *EndIntent
	completeErr     error
	completeCalls   int
	transitionCalls int
	transitionHook  func()
}

func (r *endRepository) BeginStartOperation(
	ctx context.Context,
	params BeginStartOperationParams,
) (BeginStartOperationResult, error) {
	r.endMu.Lock()
	endInProgress := r.intent != nil && !r.intent.Completed()
	r.endMu.Unlock()
	if endInProgress {
		return BeginStartOperationResult{}, ErrConcurrentTransition
	}
	return r.startRepository.BeginStartOperation(ctx, params)
}

func (r *endRepository) SaveEndIntent(
	_ context.Context,
	intent EndIntent,
) (EndIntent, bool, error) {
	r.endMu.Lock()
	defer r.endMu.Unlock()

	r.mu.Lock()
	var operation StartOperation
	hasOperation := r.operation != nil
	if hasOperation {
		operation = *r.operation
	}
	r.mu.Unlock()
	if hasOperation {
		switch operation.Status {
		case StartOperationPending,
			StartOperationCompensating,
			StartOperationCompensationFailed:
			return EndIntent{}, false, ErrSessionStartInProgress
		}
	}

	if r.intent != nil {
		if !r.intent.MatchesRequest(intent.IdempotencyKey, intent.RequestHash) {
			return EndIntent{}, false, ErrIdempotencyKeyConflict
		}
		return *r.intent, true, nil
	}
	saved := intent
	r.intent = &saved
	return saved, false, nil
}

func (r *endRepository) GetEndIntent(
	_ context.Context,
	accountID string,
	sessionID string,
) (EndIntent, error) {
	r.endMu.Lock()
	defer r.endMu.Unlock()
	if r.intent == nil ||
		r.intent.AccountID != accountID ||
		r.intent.SessionID != sessionID {
		return EndIntent{}, ErrEndIntentNotFound
	}
	return *r.intent, nil
}

func (r *endRepository) CompleteEndIntent(
	ctx context.Context,
	accountID string,
	sessionID string,
	completedAt time.Time,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.endMu.Lock()
	defer r.endMu.Unlock()
	r.completeCalls++
	if r.completeErr != nil {
		return r.completeErr
	}
	if r.intent == nil ||
		r.intent.AccountID != accountID ||
		r.intent.SessionID != sessionID {
		return ErrEndIntentNotFound
	}
	if r.intent.CompletedAt == nil {
		r.intent.CompletedAt = &completedAt
	}
	return nil
}

func (r *endRepository) TransitionToEnded(
	ctx context.Context,
	params EndTransitionParams,
) (VoiceSession, error) {
	if err := ctx.Err(); err != nil {
		return VoiceSession{}, err
	}
	r.endMu.Lock()
	r.transitionCalls++
	hook := r.transitionHook
	r.endMu.Unlock()
	if hook != nil {
		hook()
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.session.ID != params.SessionID || r.session.AccountID != params.AccountID {
		return VoiceSession{}, ErrVoiceSessionNotFound
	}
	if r.session.Status != params.Expected {
		return VoiceSession{}, ErrConcurrentTransition
	}
	r.session.Status = StatusEnded
	r.session.EndedAt = &params.EndedAt
	return r.session, nil
}

type endFixture struct {
	service    *Service
	repository *endRepository
	realtime   *startRealtime
	clock      *fakeClock
}

func newEndFixture(t *testing.T, status Status) *endFixture {
	t.Helper()
	now := time.Date(2026, 7, 29, 8, 0, 0, 0, time.UTC)
	session := VoiceSession{
		ID:        "vs_1",
		AccountID: "acct_1",
		Status:    status,
		CreatedAt: now.Add(-time.Hour),
	}
	if status == StatusActive || status == StatusEnded || status == StatusFailed {
		startedAt := now.Add(-30 * time.Minute)
		session.StartedAt = &startedAt
	}
	if status == StatusEnded || status == StatusFailed {
		endedAt := now.Add(-time.Minute)
		session.EndedAt = &endedAt
	}

	repository := &endRepository{startRepository: &startRepository{session: session}}
	realtime := &startRealtime{stopResult: RuntimeSnapshot{
		SessionID: "vs_1", RuntimeState: RuntimeStopped, UpdatedAt: now,
	}}
	clock := &fakeClock{now: now}
	service := newSharedStartService(
		t,
		repository,
		&fakeLanguageConfigReader{},
		&fakeWebRTCConnectionReader{},
		realtime,
		clock,
	)
	return &endFixture{
		service: service, repository: repository, realtime: realtime, clock: clock,
	}
}

func validEndInput() EndInput {
	return EndInput{
		AccountID:      "acct_1",
		SessionID:      "vs_1",
		IdempotencyKey: "end_1",
		RequestHash:    "hash_1",
		TraceID:        "req_1",
		Reason:         EndReasonUserRequested,
	}
}

func TestServiceEndRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*EndInput)
		want   error
	}{
		{
			name: "missing account",
			mutate: func(input *EndInput) {
				input.AccountID = ""
			},
			want: ErrUnauthorized,
		},
		{
			name: "missing session",
			mutate: func(input *EndInput) {
				input.SessionID = ""
			},
			want: ErrInvalidRequest,
		},
		{
			name: "missing idempotency key",
			mutate: func(input *EndInput) {
				input.IdempotencyKey = ""
			},
			want: ErrInvalidRequest,
		},
		{
			name: "missing request hash",
			mutate: func(input *EndInput) {
				input.RequestHash = ""
			},
			want: ErrInvalidRequest,
		},
		{
			name: "missing trace ID",
			mutate: func(input *EndInput) {
				input.TraceID = ""
			},
			want: ErrInvalidRequest,
		},
		{
			name: "invalid reason",
			mutate: func(input *EndInput) {
				input.Reason = "unknown"
			},
			want: ErrInvalidRequest,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newEndFixture(t, StatusActive)
			input := validEndInput()
			test.mutate(&input)

			_, err := fixture.service.End(context.Background(), input)
			if !errors.Is(err, test.want) {
				t.Fatalf("End() error = %v, want %v", err, test.want)
			}
			if fixture.repository.getCalls != 0 || fixture.realtime.stopCalls != 0 {
				t.Fatalf("dependency calls = GetOwned %d, Stop %d; want 0, 0",
					fixture.repository.getCalls, fixture.realtime.stopCalls)
			}
		})
	}
}

func TestServiceEndRejectsCanceledContextBeforeDependencies(t *testing.T) {
	fixture := newEndFixture(t, StatusActive)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := fixture.service.End(ctx, validEndInput())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("End() error = %v, want context.Canceled", err)
	}
	if fixture.repository.getCalls != 0 || fixture.realtime.stopCalls != 0 {
		t.Fatalf("dependency calls = GetOwned %d, Stop %d; want 0, 0",
			fixture.repository.getCalls, fixture.realtime.stopCalls)
	}
}

func TestServiceEndCreatedTransitionsWithoutRealtime(t *testing.T) {
	fixture := newEndFixture(t, StatusCreated)

	got, err := fixture.service.End(context.Background(), validEndInput())
	if err != nil {
		t.Fatalf("End() error = %v", err)
	}
	if got.Status != StatusEnded || got.StartedAt != nil || got.EndedAt == nil {
		t.Fatalf("End() = %#v", got)
	}
	if fixture.realtime.stopCalls != 0 {
		t.Fatalf("Stop() calls = %d, want 0", fixture.realtime.stopCalls)
	}
	assertEndCompleted(t, fixture)
}

func TestServiceEndCreatedHonorsStartOperationInterlock(t *testing.T) {
	tests := []struct {
		name       string
		status     StartOperationStatus
		wantErr    error
		wantStatus Status
	}{
		{
			name:       "pending blocks end",
			status:     StartOperationPending,
			wantErr:    ErrSessionStartInProgress,
			wantStatus: StatusCreated,
		},
		{
			name:       "compensated allows end",
			status:     StartOperationCompensated,
			wantStatus: StatusEnded,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newEndFixture(t, StatusCreated)
			fixture.repository.operation = &StartOperation{
				ID:             "op_1",
				SessionID:      "vs_1",
				AccountID:      "acct_1",
				IdempotencyKey: "start_1",
				RequestHash:    "start_hash",
				Status:         test.status,
				CreatedAt:      fixture.clock.now,
				UpdatedAt:      fixture.clock.now,
			}

			got, err := fixture.service.End(context.Background(), validEndInput())
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("End() error = %v, want %v", err, test.wantErr)
			}
			if err == nil && got.Status != test.wantStatus {
				t.Fatalf("End() status = %q, want %q", got.Status, test.wantStatus)
			}
			fixture.repository.mu.Lock()
			persistedStatus := fixture.repository.session.Status
			fixture.repository.mu.Unlock()
			if persistedStatus != test.wantStatus {
				t.Fatalf("persisted status = %q, want %q", persistedStatus, test.wantStatus)
			}
			if fixture.realtime.stopCalls != 0 {
				t.Fatalf("Stop() calls = %d, want 0", fixture.realtime.stopCalls)
			}
		})
	}
}

func TestEndRepositoryUnfinishedIntentBlocksStartOperation(t *testing.T) {
	fixture := newEndFixture(t, StatusCreated)
	fixture.repository.intent = &EndIntent{}

	_, err := fixture.repository.BeginStartOperation(
		context.Background(),
		BeginStartOperationParams{},
	)
	if !errors.Is(err, ErrConcurrentTransition) {
		t.Fatalf("BeginStartOperation() error = %v, want ErrConcurrentTransition", err)
	}
}

func TestServiceEndActiveStopsBeforeTransition(t *testing.T) {
	fixture := newEndFixture(t, StatusActive)
	stopReturned := false
	fixture.realtime.stopHook = func(context.Context) {
		stopReturned = true
	}
	fixture.repository.transitionHook = func() {
		if !stopReturned {
			t.Fatal("TransitionToEnded() ran before Stop() returned")
		}
	}

	got, err := fixture.service.End(context.Background(), validEndInput())
	if err != nil {
		t.Fatalf("End() error = %v", err)
	}
	if got.Status != StatusEnded || got.EndedAt == nil {
		t.Fatalf("End() = %#v", got)
	}
	if fixture.realtime.stopCalls != 1 {
		t.Fatalf("Stop() calls = %d, want 1", fixture.realtime.stopCalls)
	}
	command := fixture.realtime.stopCommand
	if command.SessionID != "vs_1" ||
		command.TraceID != "req_1" ||
		command.Reason != EndReasonUserRequested ||
		command.EndedAt.IsZero() {
		t.Fatalf("Stop() command = %#v", command)
	}
	assertEndCompleted(t, fixture)
}

func TestServiceEndActiveStopErrorPreservesSession(t *testing.T) {
	fixture := newEndFixture(t, StatusActive)
	fixture.realtime.stopErr = errDependency

	_, err := fixture.service.End(context.Background(), validEndInput())
	if !errors.Is(err, ErrRealtimeStopFailed) || !errors.Is(err, errDependency) {
		t.Fatalf("End() error = %v, want realtime and dependency errors", err)
	}
	assertActiveEndIncomplete(t, fixture)
}

func TestServiceEndActiveStopTimeoutPreservesSession(t *testing.T) {
	fixture := newEndFixture(t, StatusActive)
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	fixture.realtime.stopHook = func(ctx context.Context) {
		<-ctx.Done()
		fixture.realtime.mu.Lock()
		fixture.realtime.stopErr = ctx.Err()
		fixture.realtime.mu.Unlock()
	}

	_, err := fixture.service.End(ctx, validEndInput())
	if !errors.Is(err, ErrRealtimeStopFailed) ||
		!errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("End() error = %v, want realtime stop and deadline errors", err)
	}
	assertActiveEndIncomplete(t, fixture)
}

func TestServiceEndActiveRequestCancellationStopsPersistence(t *testing.T) {
	fixture := newEndFixture(t, StatusActive)
	ctx, cancel := context.WithCancel(context.Background())
	fixture.realtime.stopHook = func(context.Context) {
		cancel()
	}

	_, err := fixture.service.End(ctx, validEndInput())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("End() error = %v, want context.Canceled", err)
	}
	assertActiveEndIncomplete(t, fixture)
}

func TestServiceEndActiveRejectsInvalidStopSnapshot(t *testing.T) {
	now := time.Date(2026, 7, 29, 8, 0, 0, 0, time.UTC)
	tests := []struct {
		name     string
		snapshot RuntimeSnapshot
	}{
		{
			name: "session mismatch",
			snapshot: RuntimeSnapshot{
				SessionID: "vs_other", RuntimeState: RuntimeStopped, UpdatedAt: now,
			},
		},
		{
			name: "stopping",
			snapshot: RuntimeSnapshot{
				SessionID: "vs_1", RuntimeState: RuntimeStopping, UpdatedAt: now,
			},
		},
		{
			name: "failed",
			snapshot: RuntimeSnapshot{
				SessionID: "vs_1", RuntimeState: RuntimeFailed, UpdatedAt: now,
			},
		},
		{
			name: "missing updated at",
			snapshot: RuntimeSnapshot{
				SessionID: "vs_1", RuntimeState: RuntimeStopped,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newEndFixture(t, StatusActive)
			fixture.realtime.stopResult = test.snapshot

			_, err := fixture.service.End(context.Background(), validEndInput())
			if !errors.Is(err, ErrRealtimeStopFailed) {
				t.Fatalf("End() error = %v, want ErrRealtimeStopFailed", err)
			}
			assertActiveEndIncomplete(t, fixture)
		})
	}
}

func TestServiceEndPreservesTerminalSession(t *testing.T) {
	for _, status := range []Status{StatusEnded, StatusFailed} {
		t.Run(string(status), func(t *testing.T) {
			fixture := newEndFixture(t, status)
			originalEndedAt := *fixture.repository.session.EndedAt

			got, err := fixture.service.End(context.Background(), validEndInput())
			if err != nil {
				t.Fatalf("End() error = %v", err)
			}
			if got.Status != status || got.EndedAt == nil ||
				!got.EndedAt.Equal(originalEndedAt) {
				t.Fatalf("End() = %#v, want immutable %q terminal session", got, status)
			}
			if fixture.realtime.stopCalls != 0 || fixture.repository.transitionCalls != 0 {
				t.Fatalf("calls = Stop %d, Transition %d; want 0, 0",
					fixture.realtime.stopCalls, fixture.repository.transitionCalls)
			}
			assertEndCompleted(t, fixture)

			fixture.repository.completeErr = errDependency
			replayed, err := fixture.service.End(context.Background(), validEndInput())
			if err != nil {
				t.Fatalf("replayed End() error = %v", err)
			}
			if replayed.Status != status || replayed.EndedAt == nil ||
				!replayed.EndedAt.Equal(originalEndedAt) {
				t.Fatalf("replayed End() = %#v, want immutable %q terminal session",
					replayed, status)
			}
			if fixture.repository.completeCalls != 1 || fixture.realtime.stopCalls != 0 {
				t.Fatalf("replay calls = CompleteEndIntent %d, Stop %d; want 1, 0",
					fixture.repository.completeCalls, fixture.realtime.stopCalls)
			}
		})
	}
}

func TestServiceEndCompletedTerminalReplaySkipsIntentCompletion(t *testing.T) {
	fixture := newEndFixture(t, StatusActive)
	input := validEndInput()

	first, err := fixture.service.End(context.Background(), input)
	if err != nil || first.Status != StatusEnded {
		t.Fatalf("first End() = %#v, %v", first, err)
	}
	if fixture.repository.completeCalls != 1 || fixture.realtime.stopCalls != 1 {
		t.Fatalf("first calls = CompleteEndIntent %d, Stop %d; want 1, 1",
			fixture.repository.completeCalls, fixture.realtime.stopCalls)
	}
	assertEndCompleted(t, fixture)
	originalEndedAt := *first.EndedAt
	completeCallsBeforeReplay := fixture.repository.completeCalls
	stopCallsBeforeReplay := fixture.realtime.stopCalls
	fixture.repository.completeErr = errDependency

	replayed, err := fixture.service.End(context.Background(), input)
	if err != nil || replayed.Status != StatusEnded {
		t.Fatalf("replayed End() = %#v, %v", replayed, err)
	}
	if replayed.EndedAt == nil || !replayed.EndedAt.Equal(originalEndedAt) {
		t.Fatalf("replayed ended_at = %v, want %v", replayed.EndedAt, originalEndedAt)
	}
	if fixture.repository.completeCalls != completeCallsBeforeReplay {
		t.Fatalf("CompleteEndIntent() calls = %d, want %d",
			fixture.repository.completeCalls, completeCallsBeforeReplay)
	}
	if fixture.realtime.stopCalls != stopCallsBeforeReplay {
		t.Fatalf("Stop() calls = %d, want %d",
			fixture.realtime.stopCalls, stopCallsBeforeReplay)
	}
}

func TestServiceEndIncompleteTerminalIntentCompletionError(t *testing.T) {
	fixture := newEndFixture(t, StatusEnded)
	fixture.repository.completeErr = errDependency

	got, err := fixture.service.End(context.Background(), validEndInput())
	if !errors.Is(err, errDependency) {
		t.Fatalf("End() error = %v, want completion error", err)
	}
	if got.Status != StatusEnded || fixture.repository.completeCalls != 1 {
		t.Fatalf("End() = %#v, CompleteEndIntent calls = %d; want ended, 1",
			got, fixture.repository.completeCalls)
	}
	if fixture.realtime.stopCalls != 0 {
		t.Fatalf("Stop() calls = %d, want 0", fixture.realtime.stopCalls)
	}
}

func TestServiceEndRejectsIdempotencyConflict(t *testing.T) {
	fixture := newEndFixture(t, StatusCreated)
	if _, err := fixture.service.End(context.Background(), validEndInput()); err != nil {
		t.Fatalf("first End() error = %v", err)
	}

	conflict := validEndInput()
	conflict.RequestHash = "different"
	_, err := fixture.service.End(context.Background(), conflict)
	if !errors.Is(err, ErrIdempotencyKeyConflict) {
		t.Fatalf("conflicting End() error = %v, want ErrIdempotencyKeyConflict", err)
	}
}

func assertActiveEndIncomplete(t *testing.T, fixture *endFixture) {
	t.Helper()
	fixture.repository.mu.Lock()
	status := fixture.repository.session.Status
	endedAt := fixture.repository.session.EndedAt
	fixture.repository.mu.Unlock()
	fixture.repository.endMu.Lock()
	intent := fixture.repository.intent
	transitionCalls := fixture.repository.transitionCalls
	fixture.repository.endMu.Unlock()
	if status != StatusActive || endedAt != nil {
		t.Fatalf("session status = %q, ended_at = %v; want active, nil", status, endedAt)
	}
	if intent == nil || intent.Completed() {
		t.Fatalf("EndIntent = %#v, want incomplete", intent)
	}
	if transitionCalls != 0 {
		t.Fatalf("TransitionToEnded() calls = %d, want 0", transitionCalls)
	}
}

func assertEndCompleted(t *testing.T, fixture *endFixture) {
	t.Helper()
	fixture.repository.endMu.Lock()
	defer fixture.repository.endMu.Unlock()
	if fixture.repository.intent == nil || !fixture.repository.intent.Completed() {
		t.Fatalf("EndIntent = %#v, want completed", fixture.repository.intent)
	}
}
