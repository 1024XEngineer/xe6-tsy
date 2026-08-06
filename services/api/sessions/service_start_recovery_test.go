package sessions

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestMapRealtimeStopErrorPreservesBoundaryAndCause(t *testing.T) {
	tests := []struct {
		name string
		ctx  context.Context
		err  error
		want error
	}{
		{name: "provider", ctx: context.Background(), err: errDependency, want: errDependency},
		{name: "deadline", ctx: context.Background(), err: context.DeadlineExceeded, want: context.DeadlineExceeded},
		{name: "cancelled", ctx: context.Background(), err: context.Canceled, want: context.Canceled},
		{name: "not implemented", ctx: context.Background(), err: ErrNotImplemented, want: ErrNotImplemented},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := mapRealtimeStopError(test.ctx, test.err)
			if !errors.Is(err, ErrRealtimeStopFailed) ||
				!errors.Is(err, test.want) {
				t.Fatalf("mapRealtimeStopError() = %v, want stop boundary and %v", err, test.want)
			}
		})
	}
}

func TestServiceStartLeavesPendingOperationWhenRealtimeStartIsUncertain(t *testing.T) {
	fixture := newStartFixture(t, StatusCreated)
	fixture.realtime.startErr = errDependency
	fixture.realtime.getErr = ErrRuntimeSnapshotNotFound

	_, err := fixture.service.Start(context.Background(), validStartInput())
	if !errors.Is(err, ErrRealtimeStartFailed) {
		t.Fatalf("Start() error = %v, want ErrRealtimeStartFailed", err)
	}
	if fixture.repository.operation == nil ||
		fixture.repository.operation.Status != StartOperationPending {
		t.Fatalf("operation = %#v, want pending", fixture.repository.operation)
	}
	if fixture.repository.claimCalls != 0 || fixture.realtime.stopCalls != 0 {
		t.Fatalf("claim calls = %d, stop calls = %d; want 0, 0",
			fixture.repository.claimCalls, fixture.realtime.stopCalls)
	}
}

func TestServiceStartInProgressRuntimeLeavesPendingOperation(t *testing.T) {
	for _, state := range []RuntimeState{RuntimeStarting, RuntimeStopping} {
		t.Run(string(state), func(t *testing.T) {
			fixture := newStartFixture(t, StatusCreated)
			fixture.realtime.startResult.RuntimeState = state

			_, err := fixture.service.Start(context.Background(), validStartInput())
			if !errors.Is(err, ErrRealtimeAlreadyRunning) {
				t.Fatalf("Start() error = %v, want ErrRealtimeAlreadyRunning", err)
			}
			if fixture.repository.operation == nil ||
				fixture.repository.operation.Status != StartOperationPending {
				t.Fatalf("operation = %#v, want pending", fixture.repository.operation)
			}
			if fixture.repository.claimCalls != 0 || fixture.realtime.stopCalls != 0 {
				t.Fatalf("claim calls = %d, stop calls = %d; want 0, 0",
					fixture.repository.claimCalls, fixture.realtime.stopCalls)
			}
		})
	}
}

func TestServiceStartDoesNotStopWhenCompensationClaimFails(t *testing.T) {
	fixture := newStartFixture(t, StatusCreated)
	claimErr := errors.New("claim persistence failed")
	fixture.repository.transitionErr = errDependency
	fixture.repository.claimErr = claimErr

	_, err := fixture.service.Start(context.Background(), validStartInput())
	if !errors.Is(err, errDependency) || !errors.Is(err, claimErr) {
		t.Fatalf("Start() error = %v, want transition and claim errors", err)
	}
	if fixture.realtime.stopCalls != 0 {
		t.Fatalf("Realtime.Stop calls = %d, want 0", fixture.realtime.stopCalls)
	}
	if fixture.repository.operation == nil ||
		fixture.repository.operation.Status != StartOperationPending {
		t.Fatalf("operation = %#v, want pending", fixture.repository.operation)
	}
}

func TestServiceStartClaimTimeoutNeverCallsStop(t *testing.T) {
	fixture := newStartFixture(t, StatusCreated)
	fixture.repository.transitionErr = errDependency
	fixture.service.deps.CompensationTimeout = 20 * time.Millisecond
	fixture.repository.claimHook = func(ctx context.Context) {
		<-ctx.Done()
	}

	_, err := fixture.service.Start(context.Background(), validStartInput())
	if !errors.Is(err, errDependency) ||
		!errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Start() error = %v, want transition and claim deadline errors", err)
	}
	if fixture.repository.claimCalls != 1 || fixture.realtime.stopCalls != 0 {
		t.Fatalf("calls = claim %d, stop %d; want 1, 0",
			fixture.repository.claimCalls, fixture.realtime.stopCalls)
	}
	if fixture.repository.operation.Status != StartOperationPending {
		t.Fatalf("operation status = %q, want pending",
			fixture.repository.operation.Status)
	}
}

func TestServiceStartStopGetsFreshContextAfterSlowClaim(t *testing.T) {
	fixture := newStartFixture(t, StatusCreated)
	fixture.repository.transitionErr = errDependency
	claimEntered := make(chan struct{})
	releaseClaim := make(chan struct{})
	var claimCtx context.Context
	var claimDeadline time.Time
	fixture.repository.claimHook = func(ctx context.Context) {
		claimCtx = ctx
		claimDeadline, _ = ctx.Deadline()
		close(claimEntered)
		select {
		case <-releaseClaim:
		case <-ctx.Done():
		}
	}
	var stopContextErr error
	var claimContextErrAtStop error
	var stopReusedClaimContext bool
	fixture.realtime.stopHook = func(ctx context.Context) {
		stopContextErr = ctx.Err()
		claimContextErrAtStop = claimCtx.Err()
		stopReusedClaimContext = ctx == claimCtx
	}

	results := make(chan error, 1)
	go func() {
		_, err := fixture.service.Start(context.Background(), validStartInput())
		results <- err
	}()
	waitForSignal(t, "compensation claim", claimEntered)
	close(releaseClaim)

	if err := waitForStartResult(t, results); !errors.Is(err, errDependency) {
		t.Fatalf("Start() error = %v, want transition error", err)
	}
	if stopContextErr != nil ||
		!errors.Is(claimContextErrAtStop, context.Canceled) ||
		stopReusedClaimContext {
		t.Fatalf(
			"stop context error = %v, claim context at stop = %v, claim deadline = %v, reused = %t",
			stopContextErr,
			claimContextErrAtStop,
			claimDeadline,
			stopReusedClaimContext,
		)
	}
	if fixture.realtime.stopCalls != 1 ||
		fixture.repository.completeCalls != 1 ||
		fixture.repository.operation.Status != StartOperationCompensated {
		t.Fatalf("stop = %d, complete = %d, operation = %#v",
			fixture.realtime.stopCalls,
			fixture.repository.completeCalls,
			fixture.repository.operation)
	}
}

func TestServiceStartClaimStopAndPersistenceRetainTraceValues(t *testing.T) {
	fixture := newStartFixture(t, StatusCreated)
	fixture.repository.transitionErr = errDependency
	var claimCtx context.Context
	var stopCtx context.Context
	var persistCtx context.Context
	fixture.repository.claimHook = func(ctx context.Context) {
		claimCtx = ctx
	}
	fixture.realtime.stopHook = func(ctx context.Context) {
		stopCtx = ctx
	}
	fixture.repository.completeHook = func(ctx context.Context) {
		persistCtx = ctx
	}
	parent := context.WithValue(
		context.Background(),
		startTraceKey{},
		"trace-value",
	)

	_, err := fixture.service.Start(parent, validStartInput())
	if !errors.Is(err, errDependency) {
		t.Fatalf("Start() error = %v, want transition error", err)
	}
	contexts := []struct {
		name string
		ctx  context.Context
	}{
		{name: "claim", ctx: claimCtx},
		{name: "stop", ctx: stopCtx},
		{name: "persist", ctx: persistCtx},
	}
	for _, phase := range contexts {
		if phase.ctx == nil {
			t.Fatalf("%s context is nil", phase.name)
		}
		if phase.ctx.Value(startTraceKey{}) != "trace-value" {
			t.Fatalf("%s trace = %#v, want trace-value",
				phase.name, phase.ctx.Value(startTraceKey{}))
		}
		if _, ok := phase.ctx.Deadline(); !ok {
			t.Fatalf("%s context has no deadline", phase.name)
		}
	}
	if claimCtx == stopCtx || claimCtx == persistCtx || stopCtx == persistCtx {
		t.Fatal("claim, stop, and persistence contexts must be independent")
	}
}

func TestServiceStartDeniedClaimReplaysConcurrentActivation(t *testing.T) {
	fixture := newStartFixture(t, StatusCreated)
	fixture.repository.transitionErr = ErrConcurrentTransition
	fixture.repository.claimHook = func(context.Context) {
		fixture.repository.mu.Lock()
		defer fixture.repository.mu.Unlock()
		startedAt := fixture.clock.now
		fixture.repository.session.Status = StatusActive
		fixture.repository.session.StartedAt = &startedAt
		fixture.repository.operation.Status = StartOperationCompleted
		fixture.repository.operation.UpdatedAt = startedAt
	}

	got, err := fixture.service.Start(context.Background(), validStartInput())
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if got.Status != StatusActive {
		t.Fatalf("Start() = %#v, want active", got)
	}
	if fixture.realtime.stopCalls != 0 {
		t.Fatalf("Realtime.Stop calls = %d, want 0", fixture.realtime.stopCalls)
	}
	if fixture.repository.claimCalls != 1 || fixture.repository.beginCalls != 2 {
		t.Fatalf("claim calls = %d, begin calls = %d; want 1, 2",
			fixture.repository.claimCalls, fixture.repository.beginCalls)
	}
}

func TestServiceStartDeniedClaimWhileCreatedNeverStops(t *testing.T) {
	fixture := newStartFixture(t, StatusCreated)
	fixture.repository.transitionErr = errDependency
	fixture.repository.claimResult = &ClaimStartCompensationResult{
		Claimed: false,
		Reason:  StartCompensationOperationNotPending,
	}

	_, err := fixture.service.Start(context.Background(), validStartInput())
	if !errors.Is(err, errDependency) ||
		!errors.Is(err, ErrSessionStartInProgress) {
		t.Fatalf("Start() error = %v, want transition and in-progress errors", err)
	}
	if fixture.realtime.stopCalls != 0 {
		t.Fatalf("Realtime.Stop calls = %d, want 0", fixture.realtime.stopCalls)
	}
	if fixture.repository.session.Status != StatusCreated {
		t.Fatalf("session status = %q, want created", fixture.repository.session.Status)
	}
}

func TestServiceStartRejectsZeroTimestampStoppedSnapshot(t *testing.T) {
	fixture := newStartFixture(t, StatusCreated)
	fixture.repository.transitionErr = errDependency
	fixture.realtime.stopResult.UpdatedAt = time.Time{}

	_, err := fixture.service.Start(context.Background(), validStartInput())
	if !errors.Is(err, errDependency) ||
		!errors.Is(err, ErrRealtimeStopFailed) {
		t.Fatalf("Start() error = %v, want transition and stop errors", err)
	}
	if fixture.repository.failCalls != 1 || fixture.repository.completeCalls != 0 {
		t.Fatalf("fail calls = %d, complete calls = %d; want 1, 0",
			fixture.repository.failCalls, fixture.repository.completeCalls)
	}
	if fixture.repository.operation.Status != StartOperationCompensationFailed {
		t.Fatalf("operation status = %q, want compensation_failed",
			fixture.repository.operation.Status)
	}
}

func TestServiceStartRejectsCompensatingOperationWithoutClaimID(t *testing.T) {
	tests := []struct {
		name    string
		claimID *string
	}{
		{name: "nil"},
		{name: "empty", claimID: stringPointer("")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newStartFixture(t, StatusCreated)
			fixture.repository.operation = &StartOperation{
				ID:                  "op_1",
				SessionID:           "vs_1",
				AccountID:           "acct_1",
				IdempotencyKey:      "start_1",
				RequestHash:         "hash_1",
				Status:              StartOperationCompensating,
				CompensationClaimID: test.claimID,
				CreatedAt:           fixture.clock.now,
				UpdatedAt:           fixture.clock.now,
			}

			_, err := fixture.service.Start(context.Background(), validStartInput())
			if !errors.Is(err, ErrConcurrentTransition) {
				t.Fatalf("Start() error = %v, want ErrConcurrentTransition", err)
			}
			if fixture.repository.claimCalls != 0 ||
				fixture.realtime.stopCalls != 0 ||
				fixture.repository.completeCalls != 0 ||
				fixture.repository.failCalls != 0 {
				t.Fatalf(
					"calls = claim %d, stop %d, complete %d, fail %d; want all 0",
					fixture.repository.claimCalls,
					fixture.realtime.stopCalls,
					fixture.repository.completeCalls,
					fixture.repository.failCalls,
				)
			}
		})
	}
}

func stringPointer(value string) *string {
	return &value
}

func TestServiceStartPersistsCompensationFailure(t *testing.T) {
	fixture := newStartFixture(t, StatusCreated)
	fixture.repository.transitionErr = errDependency
	fixture.realtime.stopErr = errors.New("stop failed")

	_, err := fixture.service.Start(context.Background(), validStartInput())
	if !errors.Is(err, errDependency) || !errors.Is(err, ErrRealtimeStopFailed) {
		t.Fatalf("Start() error = %v, want transition and stop errors", err)
	}
	if fixture.repository.failCalls != 1 || fixture.repository.completeCalls != 0 {
		t.Fatalf("fail calls = %d, complete calls = %d; want 1, 0",
			fixture.repository.failCalls, fixture.repository.completeCalls)
	}
	if fixture.repository.operation.Status != StartOperationCompensationFailed {
		t.Fatalf("operation status = %q, want compensation_failed",
			fixture.repository.operation.Status)
	}
}

func TestServiceStartReturnsFailedCompensationPersistenceError(t *testing.T) {
	fixture := newStartFixture(t, StatusCreated)
	failErr := errors.New("fail persistence failed")
	fixture.repository.transitionErr = errDependency
	fixture.repository.failErr = failErr
	fixture.realtime.stopErr = errors.New("stop failed")

	_, err := fixture.service.Start(context.Background(), validStartInput())
	if !errors.Is(err, errDependency) ||
		!errors.Is(err, ErrRealtimeStopFailed) ||
		!errors.Is(err, failErr) {
		t.Fatalf("Start() error = %v, want transition, stop, and persistence errors", err)
	}
	if fixture.repository.operation.Status != StartOperationCompensating {
		t.Fatalf("operation status = %q, want compensating after persistence failure",
			fixture.repository.operation.Status)
	}
}

func TestServiceStartReturnsCompleteCompensationPersistenceFailure(t *testing.T) {
	fixture := newStartFixture(t, StatusCreated)
	completeErr := errors.New("complete persistence failed")
	fixture.repository.transitionErr = errDependency
	fixture.repository.completeErr = completeErr

	_, err := fixture.service.Start(context.Background(), validStartInput())
	if !errors.Is(err, errDependency) || !errors.Is(err, completeErr) {
		t.Fatalf("Start() error = %v, want transition and complete errors", err)
	}
	if fixture.repository.operation.Status != StartOperationCompensating {
		t.Fatalf("operation status = %q, want compensating",
			fixture.repository.operation.Status)
	}
}

func TestServiceStartCompletesCompensationWithFreshContextAfterStopDeadline(t *testing.T) {
	fixture := newStartFixture(t, StatusCreated)
	fixture.repository.transitionErr = errDependency
	fixture.repository.requireLiveCompleteContext = true
	fixture.service.deps.CompensationTimeout = 20 * time.Millisecond

	var stopContextErr error
	fixture.realtime.stopHook = func(ctx context.Context) {
		<-ctx.Done()
		stopContextErr = ctx.Err()
	}
	var completeTrace any
	var completeHasDeadline bool
	fixture.repository.completeHook = func(ctx context.Context) {
		completeTrace = ctx.Value(startTraceKey{})
		_, completeHasDeadline = ctx.Deadline()
	}
	ctx := context.WithValue(context.Background(), startTraceKey{}, "trace-value")

	_, err := fixture.service.Start(ctx, validStartInput())
	if !errors.Is(err, errDependency) {
		t.Fatalf("Start() error = %v, want transition error", err)
	}
	if errors.Is(err, ErrRealtimeStopFailed) ||
		errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Start() error = %v, want only the transition failure", err)
	}
	if !errors.Is(stopContextErr, context.DeadlineExceeded) {
		t.Fatalf("Stop context error = %v, want deadline exceeded", stopContextErr)
	}
	if fixture.repository.completeCalls != 1 || fixture.repository.failCalls != 0 {
		t.Fatalf("complete calls = %d, fail calls = %d; want 1, 0",
			fixture.repository.completeCalls, fixture.repository.failCalls)
	}
	fixture.repository.mu.Lock()
	completeContextErr := fixture.repository.completeContextErr
	fixture.repository.mu.Unlock()
	if completeContextErr != nil ||
		!completeHasDeadline ||
		completeTrace != "trace-value" {
		t.Fatalf("complete context error = %v, deadline = %t, trace = %#v",
			completeContextErr, completeHasDeadline, completeTrace)
	}
	if fixture.repository.operation.Status != StartOperationCompensated {
		t.Fatalf("operation status = %q, want compensated",
			fixture.repository.operation.Status)
	}
}

func TestServiceStartLeavesCompensatingWhenTerminalPersistenceTimesOut(t *testing.T) {
	fixture := newStartFixture(t, StatusCreated)
	fixture.repository.transitionErr = errDependency
	fixture.realtime.stopErr = errors.New("stop failed")
	fixture.repository.requireLiveFailContext = true
	fixture.service.deps.CompensationTimeout = 20 * time.Millisecond
	fixture.repository.failHook = func(ctx context.Context) {
		<-ctx.Done()
	}

	_, err := fixture.service.Start(context.Background(), validStartInput())
	if !errors.Is(err, errDependency) ||
		!errors.Is(err, ErrRealtimeStopFailed) ||
		!errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Start() error = %v, want transition, stop, and deadline errors", err)
	}
	if fixture.repository.failCalls != 1 || fixture.repository.completeCalls != 0 {
		t.Fatalf("fail calls = %d, complete calls = %d; want 1, 0",
			fixture.repository.failCalls, fixture.repository.completeCalls)
	}
	fixture.repository.mu.Lock()
	failContextErr := fixture.repository.failContextErr
	fixture.repository.mu.Unlock()
	if !errors.Is(failContextErr, context.DeadlineExceeded) {
		t.Fatalf("FailStartCompensation context error = %v, want deadline exceeded",
			failContextErr)
	}
	if fixture.repository.operation.Status != StartOperationCompensating {
		t.Fatalf("operation status = %q, want compensating",
			fixture.repository.operation.Status)
	}
}

func TestServiceStartResumesCompensationWithPersistedClaimID(t *testing.T) {
	fixture := newStartFixture(t, StatusCreated)
	persistedClaimID := "claim_1"
	fixture.repository.operation = &StartOperation{
		ID:                  "op_1",
		SessionID:           "vs_1",
		AccountID:           "acct_1",
		IdempotencyKey:      "start_1",
		RequestHash:         "hash_1",
		Status:              StartOperationCompensating,
		CompensationClaimID: &persistedClaimID,
		CreatedAt:           fixture.clock.now,
		UpdatedAt:           fixture.clock.now,
	}
	input := validStartInput()
	input.TraceID = "trace_2"

	_, err := fixture.service.Start(context.Background(), input)
	if !errors.Is(err, ErrRealtimeStartFailed) {
		t.Fatalf("Start() error = %v, want ErrRealtimeStartFailed", err)
	}
	if fixture.repository.claimCalls != 1 ||
		fixture.repository.claimParams[0].ClaimID != persistedClaimID {
		t.Fatalf("claim params = %#v, want persisted ClaimID %q",
			fixture.repository.claimParams, persistedClaimID)
	}
	if fixture.repository.completeParams[0].ClaimID != persistedClaimID {
		t.Fatalf("complete ClaimID = %q, want persisted ClaimID %q",
			fixture.repository.completeParams[0].ClaimID, persistedClaimID)
	}
	if fixture.realtime.stopCalls != 1 || fixture.repository.completeCalls != 1 {
		t.Fatalf("stop calls = %d, complete calls = %d; want 1, 1",
			fixture.realtime.stopCalls, fixture.repository.completeCalls)
	}
	if fixture.languages.calls != 0 || fixture.connections.calls != 0 ||
		fixture.realtime.startCalls != 0 || fixture.repository.beginCalls != 0 {
		t.Fatalf(
			"recovery prerequisites = language %d, WebRTC %d, start %d, begin %d; want all 0",
			fixture.languages.calls,
			fixture.connections.calls,
			fixture.realtime.startCalls,
			fixture.repository.beginCalls,
		)
	}
	if fixture.repository.operation.Status != StartOperationCompensated {
		t.Fatalf("operation status = %q, want compensated",
			fixture.repository.operation.Status)
	}
}

func TestServiceStartCompensationRecoveryIgnoresReadinessFailures(t *testing.T) {
	fixture := newStartFixture(t, StatusCreated)
	persistedClaimID := "claim_1"
	fixture.repository.operation = &StartOperation{
		ID:                  "op_1",
		SessionID:           "vs_1",
		AccountID:           "acct_1",
		IdempotencyKey:      "start_1",
		RequestHash:         "hash_1",
		Status:              StartOperationCompensating,
		CompensationClaimID: &persistedClaimID,
		CreatedAt:           fixture.clock.now,
		UpdatedAt:           fixture.clock.now,
	}
	fixture.languages.err = ErrLanguageConfigNotReady
	fixture.connections.result.ConnectionState = ConnectionDisconnected

	_, err := fixture.service.Start(context.Background(), validStartInput())
	if !errors.Is(err, ErrRealtimeStartFailed) {
		t.Fatalf("Start() error = %v, want ErrRealtimeStartFailed", err)
	}
	if fixture.languages.calls != 0 || fixture.connections.calls != 0 {
		t.Fatalf("readiness calls = language %d, WebRTC %d; want 0, 0",
			fixture.languages.calls, fixture.connections.calls)
	}
	if fixture.realtime.startCalls != 0 || fixture.realtime.stopCalls != 1 {
		t.Fatalf("realtime calls = start %d, stop %d; want 0, 1",
			fixture.realtime.startCalls, fixture.realtime.stopCalls)
	}
}

func TestServiceStartExistingPendingOperationStillRequiresReadiness(t *testing.T) {
	fixture := newStartFixture(t, StatusCreated)
	fixture.repository.operation = &StartOperation{
		ID:             "persisted_op",
		SessionID:      "vs_1",
		AccountID:      "acct_1",
		IdempotencyKey: "start_1",
		RequestHash:    "hash_1",
		Status:         StartOperationPending,
		CreatedAt:      fixture.clock.now,
		UpdatedAt:      fixture.clock.now,
	}
	fixture.languages.err = ErrLanguageConfigNotReady

	_, err := fixture.service.Start(context.Background(), validStartInput())
	if !errors.Is(err, ErrLanguageConfigNotReady) {
		t.Fatalf("Start() error = %v, want ErrLanguageConfigNotReady", err)
	}
	if fixture.languages.calls != 1 || fixture.connections.calls != 0 {
		t.Fatalf("readiness calls = language %d, WebRTC %d; want 1, 0",
			fixture.languages.calls, fixture.connections.calls)
	}
	if fixture.repository.beginCalls != 0 || fixture.realtime.startCalls != 0 {
		t.Fatalf("begin calls = %d, realtime start calls = %d; want 0, 0",
			fixture.repository.beginCalls, fixture.realtime.startCalls)
	}
}

func TestServiceStartReadinessFailureDoesNotCreateOperation(t *testing.T) {
	fixture := newStartFixture(t, StatusCreated)
	fixture.connections.result.ConnectionState = ConnectionDisconnected

	_, err := fixture.service.Start(context.Background(), validStartInput())
	if !errors.Is(err, ErrWebRTCNotReady) {
		t.Fatalf("Start() error = %v, want ErrWebRTCNotReady", err)
	}
	if fixture.repository.getOperationCalls != 1 || fixture.repository.beginCalls != 0 {
		t.Fatalf("operation calls = get %d, begin %d; want 1, 0",
			fixture.repository.getOperationCalls, fixture.repository.beginCalls)
	}
	if fixture.repository.operation != nil || fixture.realtime.startCalls != 0 {
		t.Fatalf("operation = %#v, realtime start calls = %d; want nil, 0",
			fixture.repository.operation, fixture.realtime.startCalls)
	}
}

func TestServiceStartCrossInstanceCompensationRecoveryIgnoresReadiness(t *testing.T) {
	fixture := newStartFixture(t, StatusCreated)
	persistedClaimID := "owner_claim"
	fixture.repository.operation = &StartOperation{
		ID:                  "op_from_instance_a",
		SessionID:           "vs_1",
		AccountID:           "acct_1",
		IdempotencyKey:      "start_1",
		RequestHash:         "hash_1",
		Status:              StartOperationCompensating,
		CompensationClaimID: &persistedClaimID,
		CreatedAt:           fixture.clock.now,
		UpdatedAt:           fixture.clock.now,
	}
	languagesB := &fakeLanguageConfigReader{err: ErrLanguageConfigNotReady}
	connectionsB := &fakeWebRTCConnectionReader{result: WebRTCConnectionSnapshot{
		SessionID:       "vs_1",
		ConnectionState: ConnectionDisconnected,
		UpdatedAt:       fixture.clock.now,
	}}
	serviceB := newSharedStartService(
		t,
		fixture.repository,
		languagesB,
		connectionsB,
		fixture.realtime,
		fixture.clock,
	)
	input := validStartInput()
	input.TraceID = "request_from_instance_b"

	_, err := serviceB.Start(context.Background(), input)
	if !errors.Is(err, ErrRealtimeStartFailed) {
		t.Fatalf("Start() error = %v, want ErrRealtimeStartFailed", err)
	}
	if languagesB.calls != 0 || connectionsB.calls != 0 {
		t.Fatalf("instance B readiness calls = language %d, WebRTC %d; want 0, 0",
			languagesB.calls, connectionsB.calls)
	}
	if fixture.repository.claimParams[0].ClaimID != persistedClaimID {
		t.Fatalf("instance B ClaimID = %q, want persisted %q",
			fixture.repository.claimParams[0].ClaimID, persistedClaimID)
	}
	if fixture.realtime.stopCalls != 1 || fixture.realtime.startCalls != 0 {
		t.Fatalf("instance B realtime calls = start %d, stop %d; want 0, 1",
			fixture.realtime.startCalls, fixture.realtime.stopCalls)
	}
}

func TestServiceStartCompensationRecoveryPersistsStopFailure(t *testing.T) {
	fixture := newStartFixture(t, StatusCreated)
	persistedClaimID := "claim_1"
	fixture.repository.operation = &StartOperation{
		ID:                  "op_1",
		SessionID:           "vs_1",
		AccountID:           "acct_1",
		IdempotencyKey:      "start_1",
		RequestHash:         "hash_1",
		Status:              StartOperationCompensating,
		CompensationClaimID: &persistedClaimID,
		CreatedAt:           fixture.clock.now,
		UpdatedAt:           fixture.clock.now,
	}
	fixture.realtime.stopErr = errors.New("stop failed")

	_, err := fixture.service.Start(context.Background(), validStartInput())
	if !errors.Is(err, ErrRealtimeStartFailed) ||
		!errors.Is(err, ErrRealtimeStopFailed) {
		t.Fatalf("Start() error = %v, want start and stop errors", err)
	}
	if fixture.repository.failCalls != 1 ||
		fixture.repository.operation.Status != StartOperationCompensationFailed {
		t.Fatalf("fail calls = %d, operation = %#v",
			fixture.repository.failCalls, fixture.repository.operation)
	}
}

func TestServiceStartCrossInstanceLoserNeverStopsActivatedRuntime(t *testing.T) {
	fixture := newStartFixture(t, StatusCreated)
	repository := fixture.repository
	realtime := fixture.realtime

	var readMu sync.Mutex
	readCount := 0
	bothRead := make(chan struct{})
	releaseReads := make(chan struct{})
	repository.getHook = func(ctx context.Context) {
		readMu.Lock()
		readCount++
		current := readCount
		if current == 2 {
			close(bothRead)
		}
		readMu.Unlock()
		if current <= 2 {
			select {
			case <-releaseReads:
			case <-ctx.Done():
			}
		}
	}

	var startMu sync.Mutex
	startCount := 0
	bothStarted := make(chan struct{})
	releaseStarts := make(chan struct{})
	realtime.startHook = func(ctx context.Context) {
		startMu.Lock()
		startCount++
		current := startCount
		if current == 2 {
			close(bothStarted)
		}
		startMu.Unlock()
		select {
		case <-releaseStarts:
		case <-ctx.Done():
		}
	}

	aTime := fixture.clock.now
	bTime := aTime.Add(time.Minute)
	languagesA := *fixture.languages
	languagesB := *fixture.languages
	connectionsA := *fixture.connections
	connectionsB := *fixture.connections
	aActivated := make(chan struct{})
	repository.transitionErrFor = func(params StartTransitionParams) error {
		if params.StartedAt.Equal(bTime) {
			select {
			case <-aActivated:
			case <-time.After(time.Second):
				return errors.New("timed out waiting for competing activation")
			}
			return ErrConcurrentTransition
		}
		return nil
	}
	repository.transitionAfter = func(params StartTransitionParams) {
		if params.StartedAt.Equal(aTime) {
			close(aActivated)
		}
	}

	serviceA := newSharedStartService(
		t,
		repository,
		&languagesA,
		&connectionsA,
		realtime,
		&fakeClock{now: aTime},
	)
	serviceB := newSharedStartService(
		t,
		repository,
		&languagesB,
		&connectionsB,
		realtime,
		&fakeClock{now: bTime},
	)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	results := make(chan error, 2)
	go func() {
		_, err := serviceA.Start(ctx, validStartInput())
		results <- err
	}()
	go func() {
		_, err := serviceB.Start(ctx, validStartInput())
		results <- err
	}()
	waitForSignal(t, "both cross-instance reads", bothRead)
	close(releaseReads)
	waitForSignal(t, "both cross-instance realtime starts", bothStarted)
	close(releaseStarts)

	for range 2 {
		if err := waitForStartResult(t, results); err != nil {
			t.Fatalf("cross-instance Start() error = %v", err)
		}
	}

	repository.mu.Lock()
	sessionStatus := repository.session.Status
	operationStatus := repository.operation.Status
	repository.mu.Unlock()
	realtime.mu.Lock()
	stopCalls := realtime.stopCalls
	realtime.mu.Unlock()
	if sessionStatus != StatusActive || operationStatus != StartOperationCompleted {
		t.Fatalf("session status = %q, operation status = %q; want active, completed",
			sessionStatus, operationStatus)
	}
	if stopCalls != 0 {
		t.Fatalf("Realtime.Stop calls = %d, want 0", stopCalls)
	}
}
