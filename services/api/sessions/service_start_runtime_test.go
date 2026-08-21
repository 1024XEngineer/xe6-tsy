package sessions

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestServiceStartRejectsRuntimeWithoutOwnedOperation(t *testing.T) {
	tests := []struct {
		name             string
		sessionID        string
		startOperationID string
	}{
		{name: "session mismatch", sessionID: "other", startOperationID: "op_1"},
		{name: "missing operation", sessionID: "vs_1"},
		{name: "foreign operation", sessionID: "vs_1", startOperationID: "op_other"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newStartFixture(t, StatusCreated)
			fixture.realtime.startResult.SessionID = test.sessionID
			fixture.realtime.startResult.StartOperationID = test.startOperationID

			_, err := fixture.service.Start(context.Background(), validStartInput())
			if !errors.Is(err, ErrConcurrentTransition) {
				t.Fatalf("Start() error = %v, want ErrConcurrentTransition", err)
			}
			if len(fixture.repository.transitions) != 0 ||
				fixture.repository.claimCalls != 0 ||
				fixture.realtime.stopCalls != 0 {
				t.Fatalf(
					"calls = transition %d, claim %d, stop %d; want all 0",
					len(fixture.repository.transitions),
					fixture.repository.claimCalls,
					fixture.realtime.stopCalls,
				)
			}
		})
	}
}

func TestServiceStartPendingReconcilesAlreadyRunningRuntime(t *testing.T) {
	for _, state := range []RuntimeState{
		RuntimeListening,
		RuntimeASRProcessing,
		RuntimeTranslating,
		RuntimeThinking,
		RuntimeAssistantProcessing,
		RuntimeTTSProcessing,
		RuntimePlaying,
	} {
		t.Run(string(state), func(t *testing.T) {
			fixture := newStartFixture(t, StatusCreated)
			fixture.realtime.startErr = ErrRealtimeAlreadyRunning
			fixture.realtime.getResult.RuntimeState = state

			session, err := fixture.service.Start(context.Background(), validStartInput())
			if err != nil {
				t.Fatalf("Start() error = %v", err)
			}
			if session.Status != StatusActive ||
				fixture.repository.operation.Status != StartOperationCompleted {
				t.Fatalf("session = %#v, operation = %#v; want active/completed",
					session, fixture.repository.operation)
			}
			if fixture.realtime.getCalls != 1 ||
				len(fixture.repository.transitions) != 1 ||
				fixture.realtime.stopCalls != 0 {
				t.Fatalf("calls = get %d, transition %d, stop %d; want 1, 1, 0",
					fixture.realtime.getCalls,
					len(fixture.repository.transitions),
					fixture.realtime.stopCalls)
			}
		})
	}
}

func TestServiceStartReconcilesRunningRuntimeAfterStartTimeout(t *testing.T) {
	fixture := newStartFixture(t, StatusCreated)
	fixture.realtime.startErr = context.DeadlineExceeded

	session, err := fixture.service.Start(context.Background(), validStartInput())
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	assertRecoveredActiveStart(t, fixture, session)
}

func TestServiceStartReconcilesRunningRuntimeAfterProviderError(t *testing.T) {
	fixture := newStartFixture(t, StatusCreated)
	fixture.realtime.startErr = errDependency

	session, err := fixture.service.Start(context.Background(), validStartInput())
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	assertRecoveredActiveStart(t, fixture, session)
}

func TestServiceStartKeepsPendingWhenRuntimeMissingAfterStartError(t *testing.T) {
	fixture := newStartFixture(t, StatusCreated)
	fixture.realtime.startErr = errDependency
	fixture.realtime.getErr = ErrRuntimeSnapshotNotFound

	_, err := fixture.service.Start(context.Background(), validStartInput())
	if !errors.Is(err, ErrRealtimeStartFailed) || !errors.Is(err, errDependency) {
		t.Fatalf("Start() error = %v, want start boundary and provider error", err)
	}
	assertPendingStartHasNoDestructiveSideEffects(t, fixture)
}

func TestServiceStartKeepsPendingWhenOwnedRuntimeIsInProgressAfterStartError(t *testing.T) {
	for _, state := range []RuntimeState{RuntimeStarting, RuntimeStopping} {
		t.Run(string(state), func(t *testing.T) {
			fixture := newStartFixture(t, StatusCreated)
			fixture.realtime.startErr = errDependency
			fixture.realtime.getResult.RuntimeState = state

			_, err := fixture.service.Start(context.Background(), validStartInput())
			if !errors.Is(err, ErrRealtimeAlreadyRunning) {
				t.Fatalf("Start() error = %v, want ErrRealtimeAlreadyRunning", err)
			}
			assertPendingStartHasNoDestructiveSideEffects(t, fixture)
		})
	}
}

func TestServiceStartKeepsOriginalErrorForStoppedRuntimeAfterUncertainStart(t *testing.T) {
	for _, state := range []RuntimeState{RuntimeStopped, RuntimeFailed} {
		t.Run(string(state), func(t *testing.T) {
			fixture := newStartFixture(t, StatusCreated)
			fixture.realtime.startErr = errDependency
			fixture.realtime.getResult.RuntimeState = state

			_, err := fixture.service.Start(context.Background(), validStartInput())
			if !errors.Is(err, ErrRealtimeStartFailed) || !errors.Is(err, errDependency) {
				t.Fatalf("Start() error = %v, want original start failure", err)
			}
			assertPendingStartHasNoDestructiveSideEffects(t, fixture)
		})
	}
}

func TestServiceStartRejectsForeignRuntimeAfterUncertainStart(t *testing.T) {
	fixture := newStartFixture(t, StatusCreated)
	fixture.realtime.startErr = errDependency
	fixture.realtime.getResult.StartOperationID = "op_foreign"

	_, err := fixture.service.Start(context.Background(), validStartInput())
	if !errors.Is(err, ErrConcurrentTransition) {
		t.Fatalf("Start() error = %v, want ErrConcurrentTransition", err)
	}
	assertPendingStartHasNoDestructiveSideEffects(t, fixture)
}

func TestServiceStartJoinsStartAndReconciliationFailures(t *testing.T) {
	fixture := newStartFixture(t, StatusCreated)
	reconciliationErr := errors.New("runtime state read failed")
	fixture.realtime.startErr = errDependency
	fixture.realtime.getErr = reconciliationErr

	_, err := fixture.service.Start(context.Background(), validStartInput())
	if !errors.Is(err, ErrRealtimeStartFailed) ||
		!errors.Is(err, errDependency) ||
		!errors.Is(err, ErrRuntimeUnavailable) ||
		!errors.Is(err, reconciliationErr) {
		t.Fatalf("Start() error = %v, want start and reconciliation failures", err)
	}
	assertPendingStartHasNoDestructiveSideEffects(t, fixture)
}

func TestServiceStartReconciliationTimeoutKeepsOperationPending(t *testing.T) {
	fixture := newStartFixture(t, StatusCreated)
	fixture.service.deps.StartReconciliationTimeout = 20 * time.Millisecond
	fixture.realtime.startErr = errDependency
	fixture.realtime.getHook = func(ctx context.Context) {
		<-ctx.Done()
	}

	_, err := fixture.service.Start(context.Background(), validStartInput())
	if !errors.Is(err, ErrRealtimeStartFailed) ||
		!errors.Is(err, errDependency) ||
		!errors.Is(err, ErrRuntimeUnavailable) ||
		!errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Start() error = %v, want start and reconciliation timeout failures", err)
	}
	assertPendingStartHasNoDestructiveSideEffects(t, fixture)
}

func TestServiceStartReconciliationUsesFreshContextAfterRequestCancellation(t *testing.T) {
	fixture := newStartFixture(t, StatusCreated)
	fixture.service.deps.StartReconciliationTimeout = time.Second
	requestCtx, cancelRequest := context.WithCancel(
		context.WithValue(context.Background(), startTraceKey{}, "trace-value"),
	)
	fixture.realtime.startErr = context.Canceled
	fixture.realtime.startHook = func(context.Context) {
		cancelRequest()
	}
	var reconciliationErr error
	var hasDeadline bool
	var retainedTrace any
	fixture.realtime.getHook = func(ctx context.Context) {
		reconciliationErr = ctx.Err()
		_, hasDeadline = ctx.Deadline()
		retainedTrace = ctx.Value(startTraceKey{})
	}

	session, err := fixture.service.Start(requestCtx, validStartInput())
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if reconciliationErr != nil || !hasDeadline || retainedTrace != "trace-value" {
		t.Fatalf("reconciliation context error = %v, deadline = %t, trace = %#v",
			reconciliationErr, hasDeadline, retainedTrace)
	}
	assertRecoveredActiveStart(t, fixture, session)
}

func TestServiceStartPendingKeepsInProgressRuntimePending(t *testing.T) {
	for _, state := range []RuntimeState{RuntimeStarting, RuntimeStopping} {
		t.Run(string(state), func(t *testing.T) {
			fixture := newStartFixture(t, StatusCreated)
			fixture.realtime.startErr = ErrRealtimeAlreadyRunning
			fixture.realtime.getResult.RuntimeState = state

			_, err := fixture.service.Start(context.Background(), validStartInput())
			if !errors.Is(err, ErrRealtimeAlreadyRunning) {
				t.Fatalf("Start() error = %v, want ErrRealtimeAlreadyRunning", err)
			}
			assertPendingStartHasNoDestructiveSideEffects(t, fixture)
		})
	}
}

func TestServiceStartPendingKeepsAlreadyRunningErrorForStoppedOrFailedRuntime(t *testing.T) {
	for _, state := range []RuntimeState{RuntimeStopped, RuntimeFailed} {
		t.Run(string(state), func(t *testing.T) {
			fixture := newStartFixture(t, StatusCreated)
			fixture.realtime.startErr = ErrRealtimeAlreadyRunning
			fixture.realtime.getResult.RuntimeState = state

			_, err := fixture.service.Start(context.Background(), validStartInput())
			if !errors.Is(err, ErrRealtimeAlreadyRunning) {
				t.Fatalf("Start() error = %v, want ErrRealtimeAlreadyRunning", err)
			}
			assertPendingStartHasNoDestructiveSideEffects(t, fixture)
		})
	}
}

func TestServiceStartPendingRejectsForeignRunningRuntime(t *testing.T) {
	fixture := newStartFixture(t, StatusCreated)
	fixture.realtime.startErr = ErrRealtimeAlreadyRunning
	fixture.realtime.getResult.StartOperationID = "op_foreign"

	_, err := fixture.service.Start(context.Background(), validStartInput())
	if !errors.Is(err, ErrConcurrentTransition) {
		t.Fatalf("Start() error = %v, want ErrConcurrentTransition", err)
	}
	assertPendingStartHasNoDestructiveSideEffects(t, fixture)
}

func TestServiceStartPendingCompensatesOwnedInvalidRuntime(t *testing.T) {
	fixture := newStartFixture(t, StatusCreated)
	fixture.realtime.startErr = ErrRealtimeAlreadyRunning
	fixture.realtime.getResult.UpdatedAt = time.Time{}

	_, err := fixture.service.Start(context.Background(), validStartInput())
	if !errors.Is(err, ErrRealtimeStartFailed) {
		t.Fatalf("Start() error = %v, want ErrRealtimeStartFailed", err)
	}
	if fixture.repository.claimCalls != 1 ||
		fixture.realtime.stopCalls != 1 ||
		fixture.repository.completeCalls != 1 {
		t.Fatalf("calls = claim %d, stop %d, complete %d; want 1, 1, 1",
			fixture.repository.claimCalls,
			fixture.realtime.stopCalls,
			fixture.repository.completeCalls)
	}
}

func TestServiceStartCrossInstanceReconcilesMatchingRuntime(t *testing.T) {
	fixture := newStartFixture(t, StatusCreated)
	fixture.repository.operation = pendingStartOperationForFixture(fixture)
	fixture.realtime.startErr = ErrRealtimeAlreadyRunning
	serviceB := newSharedStartService(
		t,
		fixture.repository,
		fixture.languages,
		fixture.connections,
		fixture.realtime,
		fixture.clock,
	)

	session, err := serviceB.Start(context.Background(), validStartInput())
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if session.Status != StatusActive ||
		fixture.repository.operation.Status != StartOperationCompleted {
		t.Fatalf("session = %#v, operation = %#v; want active/completed",
			session, fixture.repository.operation)
	}
	if fixture.repository.beginCalls != 0 ||
		fixture.realtime.getCalls != 1 ||
		fixture.realtime.stopCalls != 0 {
		t.Fatalf("calls = begin %d, get %d, stop %d; want 0, 1, 0",
			fixture.repository.beginCalls,
			fixture.realtime.getCalls,
			fixture.realtime.stopCalls)
	}
}

func assertPendingStartHasNoDestructiveSideEffects(t *testing.T, fixture *startFixture) {
	t.Helper()
	if fixture.repository.operation.Status != StartOperationPending ||
		len(fixture.repository.transitions) != 0 ||
		fixture.repository.claimCalls != 0 ||
		fixture.realtime.stopCalls != 0 {
		t.Fatalf(
			"operation = %#v, transitions = %d, claim = %d, stop = %d; want pending and no side effects",
			fixture.repository.operation,
			len(fixture.repository.transitions),
			fixture.repository.claimCalls,
			fixture.realtime.stopCalls,
		)
	}
}

func assertRecoveredActiveStart(
	t *testing.T,
	fixture *startFixture,
	session VoiceSession,
) {
	t.Helper()
	if session.Status != StatusActive ||
		fixture.repository.operation.Status != StartOperationCompleted {
		t.Fatalf("session = %#v, operation = %#v; want active/completed",
			session, fixture.repository.operation)
	}
	if fixture.realtime.getCalls != 1 ||
		len(fixture.repository.transitions) != 1 ||
		fixture.repository.beginCalls != 1 ||
		fixture.repository.claimCalls != 0 ||
		fixture.realtime.stopCalls != 0 {
		t.Fatalf(
			"calls = get %d, transition %d, begin %d, claim %d, stop %d; want 1, 1, 1, 0, 0",
			fixture.realtime.getCalls,
			len(fixture.repository.transitions),
			fixture.repository.beginCalls,
			fixture.repository.claimCalls,
			fixture.realtime.stopCalls,
		)
	}
}

func pendingStartOperationForFixture(fixture *startFixture) *StartOperation {
	return &StartOperation{
		ID:             "op_1",
		SessionID:      "vs_1",
		AccountID:      "acct_1",
		IdempotencyKey: "start_1",
		RequestHash:    "hash_1",
		Status:         StartOperationPending,
		CreatedAt:      fixture.clock.now,
		UpdatedAt:      fixture.clock.now,
	}
}
