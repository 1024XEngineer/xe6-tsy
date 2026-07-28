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

func TestServiceStartPendingRetriesStoppedOrFailedRuntime(t *testing.T) {
	for _, state := range []RuntimeState{RuntimeStopped, RuntimeFailed} {
		t.Run(string(state), func(t *testing.T) {
			fixture := newStartFixture(t, StatusCreated)
			fixture.realtime.startErr = ErrRealtimeAlreadyRunning
			fixture.realtime.getResult.RuntimeState = state

			_, err := fixture.service.Start(context.Background(), validStartInput())
			if !errors.Is(err, ErrRealtimeStartFailed) {
				t.Fatalf("Start() error = %v, want ErrRealtimeStartFailed", err)
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
