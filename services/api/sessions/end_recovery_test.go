package sessions

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestNewEndRecoveryWorkerRejectsInvalidConfig(t *testing.T) {
	fixture := newEndRecoveryFixture(t, StatusActive)
	valid := EndRecoveryConfig{
		WorkerID:       "worker_1",
		PollInterval:   time.Second,
		LeaseDuration:  time.Minute,
		AttemptTimeout: 30 * time.Second,
		InitialBackoff: time.Second,
		MaxBackoff:     time.Minute,
	}
	tests := []struct {
		name    string
		service *Service
		edit    func(*EndRecoveryConfig)
	}{
		{name: "nil service"},
		{
			name: "empty worker ID", service: fixture.worker.service,
			edit: func(config *EndRecoveryConfig) { config.WorkerID = "" },
		},
		{
			name: "zero poll interval", service: fixture.worker.service,
			edit: func(config *EndRecoveryConfig) { config.PollInterval = 0 },
		},
		{
			name: "zero lease duration", service: fixture.worker.service,
			edit: func(config *EndRecoveryConfig) { config.LeaseDuration = 0 },
		},
		{
			name: "zero attempt timeout", service: fixture.worker.service,
			edit: func(config *EndRecoveryConfig) { config.AttemptTimeout = 0 },
		},
		{
			name: "attempt timeout equals lease", service: fixture.worker.service,
			edit: func(config *EndRecoveryConfig) { config.AttemptTimeout = config.LeaseDuration },
		},
		{
			name: "zero initial backoff", service: fixture.worker.service,
			edit: func(config *EndRecoveryConfig) { config.InitialBackoff = 0 },
		},
		{
			name: "maximum backoff below initial", service: fixture.worker.service,
			edit: func(config *EndRecoveryConfig) { config.MaxBackoff = config.InitialBackoff / 2 },
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := valid
			if test.edit != nil {
				test.edit(&config)
			}
			_, err := NewEndRecoveryWorker(test.service, config)
			if !errors.Is(err, ErrInvalidDependency) {
				t.Fatalf("NewEndRecoveryWorker() error = %v, want ErrInvalidDependency", err)
			}
		})
	}
}

func TestEndRecoveryWorkerRejectsNilContext(t *testing.T) {
	fixture := newEndRecoveryFixture(t, StatusActive)
	if err := fixture.worker.Run(nil); !errors.Is(err, ErrInvalidDependency) {
		t.Fatalf("Run() error = %v, want ErrInvalidDependency", err)
	}
	processed, err := fixture.worker.ProcessNext(nil)
	if processed || !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("ProcessNext() = %t, %v, want invalid request", processed, err)
	}
}

func TestEndRecoveryWorkerProcessNextRejectsCancelledContextWithoutSideEffects(t *testing.T) {
	fixture := newEndRecoveryFixture(t, StatusActive)
	clock := fixture.worker.service.deps.Clock.(*fakeClock)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	processed, err := fixture.worker.ProcessNext(ctx)
	if processed || !errors.Is(err, context.Canceled) {
		t.Fatalf("ProcessNext() = %t, %v, want false, context.Canceled", processed, err)
	}
	if clock.calls != 0 || fixture.repository.claimCalls != 0 ||
		fixture.repository.startRepository.getCalls != 0 ||
		fixture.repository.transitionCalls != 0 ||
		fixture.repository.retryCalls != 0 ||
		fixture.repository.completeCalls != 0 ||
		fixture.realtime.stopCalls != 0 {
		t.Fatalf(
			"side effects = clock %d, claim %d, get %d, transition %d, retry %d, complete %d, stop %d; want all 0",
			clock.calls,
			fixture.repository.claimCalls,
			fixture.repository.startRepository.getCalls,
			fixture.repository.transitionCalls,
			fixture.repository.retryCalls,
			fixture.repository.completeCalls,
			fixture.realtime.stopCalls,
		)
	}
}

func TestEndRecoveryWorkerEndsActiveSessionAfterConfirmedStop(t *testing.T) {
	fixture := newEndRecoveryFixture(t, StatusActive)

	processed, err := fixture.worker.ProcessNext(t.Context())
	if err != nil {
		t.Fatalf("ProcessNext() error = %v", err)
	}
	if !processed {
		t.Fatal("ProcessNext() processed = false, want true")
	}
	if fixture.repository.session.Status != StatusEnded ||
		fixture.repository.intent.CompletedAt == nil {
		t.Fatalf(
			"session status = %q, intent = %#v; want ended and completed",
			fixture.repository.session.Status,
			fixture.repository.intent,
		)
	}
	if fixture.realtime.stopCalls != 1 {
		t.Fatalf("Stop() calls = %d, want 1", fixture.realtime.stopCalls)
	}
}

func TestEndRecoveryWorkerRetriesWithoutEndingOnInvalidStop(t *testing.T) {
	fixture := newEndRecoveryFixture(t, StatusActive)
	fixture.realtime.stopResult.RuntimeState = RuntimeStopping

	processed, err := fixture.worker.ProcessNext(t.Context())
	if !processed || !errors.Is(err, ErrRealtimeStopFailed) {
		t.Fatalf("ProcessNext() = %t, %v; want processed stop failure", processed, err)
	}
	if fixture.repository.session.Status != StatusActive ||
		fixture.repository.session.EndedAt != nil {
		t.Fatalf(
			"session = %#v, want active without ended_at",
			fixture.repository.session,
		)
	}
	intent := fixture.repository.intent
	if intent.RetryCount != 1 || intent.LastError == nil ||
		!intent.NextAttemptAt.Equal(fixture.now.Add(time.Second)) ||
		fixture.repository.retryAfter != time.Second ||
		intent.RecoveryOwner != nil || intent.LeaseExpiresAt != nil {
		t.Fatalf("retry intent = %#v", intent)
	}
}

func TestEndRecoveryWorkerCompletesTerminalIntentWithoutStop(t *testing.T) {
	fixture := newEndRecoveryFixture(t, StatusEnded)

	processed, err := fixture.worker.ProcessNext(t.Context())
	if err != nil || !processed {
		t.Fatalf("ProcessNext() = %t, %v, want true, nil", processed, err)
	}
	if fixture.repository.intent.CompletedAt == nil {
		t.Fatal("intent remains incomplete")
	}
	if fixture.realtime.stopCalls != 0 {
		t.Fatalf("Stop() calls = %d, want 0", fixture.realtime.stopCalls)
	}
}

func TestEndRecoveryWorkerEndsCreatedSessionWithoutStop(t *testing.T) {
	fixture := newEndRecoveryFixture(t, StatusCreated)

	processed, err := fixture.worker.ProcessNext(t.Context())
	if err != nil || !processed {
		t.Fatalf("ProcessNext() = %t, %v, want true, nil", processed, err)
	}
	if fixture.repository.session.Status != StatusEnded ||
		fixture.repository.session.StartedAt != nil ||
		fixture.repository.intent.CompletedAt == nil {
		t.Fatalf("session = %#v, intent = %#v", fixture.repository.session, fixture.repository.intent)
	}
	if fixture.realtime.stopCalls != 0 {
		t.Fatalf("Stop() calls = %d, want 0", fixture.realtime.stopCalls)
	}
}

func TestEndRecoveryWorkerRetriesTransitionFailureThenCompletes(t *testing.T) {
	fixture := newEndRecoveryFixture(t, StatusActive)
	fixture.repository.transitionErr = errDependency

	processed, err := fixture.worker.ProcessNext(t.Context())
	if !processed || !errors.Is(err, errDependency) {
		t.Fatalf("first ProcessNext() = %t, %v", processed, err)
	}
	if fixture.repository.session.Status != StatusActive ||
		fixture.repository.intent.RetryCount != 1 {
		t.Fatalf(
			"session = %#v, intent = %#v",
			fixture.repository.session,
			fixture.repository.intent,
		)
	}

	fixture.repository.transitionErr = nil
	fixture.repository.intent.NextAttemptAt = fixture.now
	restarted := newRestartedEndRecoveryWorker(t, fixture, "worker_2")
	processed, err = restarted.ProcessNext(t.Context())
	if err != nil || !processed {
		t.Fatalf("second ProcessNext() = %t, %v", processed, err)
	}
	if fixture.repository.session.Status != StatusEnded ||
		fixture.repository.intent.CompletedAt == nil ||
		fixture.realtime.stopCalls != 2 {
		t.Fatalf(
			"session = %#v, intent = %#v, Stop calls = %d",
			fixture.repository.session,
			fixture.repository.intent,
			fixture.realtime.stopCalls,
		)
	}
}

func TestEndRecoveryWorkerRestartCompletesTransitionedSession(t *testing.T) {
	fixture := newEndRecoveryFixture(t, StatusActive)
	fixture.repository.completeErr = errDependency

	processed, err := fixture.worker.ProcessNext(t.Context())
	if !processed || !errors.Is(err, errDependency) {
		t.Fatalf("first ProcessNext() = %t, %v", processed, err)
	}
	if fixture.repository.session.Status != StatusEnded ||
		fixture.repository.intent.Completed() ||
		fixture.repository.intent.RetryCount != 1 {
		t.Fatalf(
			"session = %#v, intent = %#v",
			fixture.repository.session,
			fixture.repository.intent,
		)
	}

	fixture.repository.completeErr = nil
	fixture.repository.intent.NextAttemptAt = fixture.now
	restarted := newRestartedEndRecoveryWorker(t, fixture, "worker_2")
	processed, err = restarted.ProcessNext(t.Context())
	if err != nil || !processed {
		t.Fatalf("second ProcessNext() = %t, %v", processed, err)
	}
	if !fixture.repository.intent.Completed() || fixture.realtime.stopCalls != 1 {
		t.Fatalf(
			"intent = %#v, Stop calls = %d",
			fixture.repository.intent,
			fixture.realtime.stopCalls,
		)
	}
}

func TestEndRecoveryLeaseExcludesAnotherWorker(t *testing.T) {
	fixture := newEndRecoveryFixture(t, StatusActive)
	_, claimed, err := fixture.repository.ClaimPendingEndIntent(
		t.Context(),
		ClaimEndIntentParams{
			WorkerID:       "worker_1",
			ClaimedAt:      fixture.now,
			LeaseExpiresAt: fixture.now.Add(time.Minute),
		},
	)
	if err != nil || !claimed {
		t.Fatalf("ClaimPendingEndIntent() = %t, %v", claimed, err)
	}
	worker, err := NewEndRecoveryWorker(fixture.worker.service, EndRecoveryConfig{
		WorkerID:       "worker_2",
		PollInterval:   time.Minute,
		LeaseDuration:  time.Minute,
		AttemptTimeout: 30 * time.Second,
		InitialBackoff: time.Second,
		MaxBackoff:     8 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewEndRecoveryWorker() error = %v", err)
	}
	processed, err := worker.ProcessNext(t.Context())
	if err != nil || processed {
		t.Fatalf("ProcessNext() = %t, %v, want false, nil", processed, err)
	}
	if fixture.realtime.stopCalls != 0 {
		t.Fatalf("Stop() calls = %d, want 0", fixture.realtime.stopCalls)
	}
}

func TestEndRecoveryWorkerRunStopsOnCancellation(t *testing.T) {
	fixture := newEndRecoveryFixture(t, StatusActive)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := fixture.worker.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestEndRecoveryWorkerRunStopsWhileWaiting(t *testing.T) {
	fixture := newEndRecoveryFixture(t, StatusActive)
	fixture.worker.config.PollInterval = time.Hour
	fixture.repository.claimErr = errDependency
	ctx, cancel := context.WithCancel(t.Context())
	fixture.worker.service.deps.Logger = slog.New(slog.NewTextHandler(
		cancelWriter{cancel: cancel},
		nil,
	))

	if err := fixture.worker.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if fixture.repository.claimCalls != 1 {
		t.Fatalf("claim calls = %d, want 1", fixture.repository.claimCalls)
	}
}

func TestEndRecoveryWorkerRunDrainsBeforePolling(t *testing.T) {
	fixture := newEndRecoveryFixture(t, StatusCreated)
	ctx, cancel := context.WithCancel(t.Context())
	fixture.repository.claimHook = func(call int) {
		if call == 2 {
			cancel()
		}
	}

	if err := fixture.worker.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if fixture.repository.claimCalls != 2 ||
		fixture.repository.intent.CompletedAt == nil {
		t.Fatalf(
			"claim calls = %d, intent = %#v",
			fixture.repository.claimCalls,
			fixture.repository.intent,
		)
	}
}

func TestEndRecoveryWorkerRejectsInvalidClaim(t *testing.T) {
	fixture := newEndRecoveryFixture(t, StatusActive)
	fixture.repository.claimResultHook = func(intent *EndIntent) {
		intent.RecoveryOwner = nil
	}

	processed, err := fixture.worker.ProcessNext(t.Context())
	if !processed || !errors.Is(err, ErrInvalidDependency) {
		t.Fatalf("ProcessNext() = %t, %v, want invalid claim", processed, err)
	}
	if fixture.repository.retryCalls != 0 || fixture.repository.completeCalls != 0 ||
		fixture.repository.transitionCalls != 0 || fixture.realtime.stopCalls != 0 {
		t.Fatalf(
			"side effects = retry %d, complete %d, transition %d, stop %d; want all 0",
			fixture.repository.retryCalls,
			fixture.repository.completeCalls,
			fixture.repository.transitionCalls,
			fixture.realtime.stopCalls,
		)
	}
	leaseExpiresAt := fixture.now.Add(fixture.worker.config.LeaseDuration)
	if fixture.repository.intent.RetryCount != 0 ||
		fixture.repository.intent.RecoveryOwner == nil ||
		*fixture.repository.intent.RecoveryOwner != fixture.worker.config.WorkerID ||
		fixture.repository.intent.LeaseExpiresAt == nil ||
		!fixture.repository.intent.LeaseExpiresAt.Equal(leaseExpiresAt) {
		t.Fatalf("intent = %#v, want unchanged claimed lease", fixture.repository.intent)
	}
}

func TestEndRecoveryWorkerRejectsExpiredLeaseWithoutRecoverySideEffects(t *testing.T) {
	fixture := newEndRecoveryFixture(t, StatusActive)
	expiredAt := time.Now().Add(-fixture.worker.config.LeaseDuration)
	fixture.worker.monotonicNow = func() time.Time { return expiredAt }

	processed, err := fixture.worker.ProcessNext(t.Context())
	if !processed || !errors.Is(err, ErrConcurrentTransition) {
		t.Fatalf("ProcessNext() = %t, %v, want true, ErrConcurrentTransition", processed, err)
	}
	if fixture.repository.claimCalls != 1 ||
		fixture.repository.startRepository.getCalls != 0 ||
		fixture.repository.transitionCalls != 0 ||
		fixture.repository.retryCalls != 0 ||
		fixture.repository.completeCalls != 0 ||
		fixture.realtime.stopCalls != 0 {
		t.Fatalf(
			"side effects = claim %d, get %d, transition %d, retry %d, complete %d, stop %d; want 1, 0, 0, 0, 0, 0",
			fixture.repository.claimCalls,
			fixture.repository.startRepository.getCalls,
			fixture.repository.transitionCalls,
			fixture.repository.retryCalls,
			fixture.repository.completeCalls,
			fixture.realtime.stopCalls,
		)
	}
}

func TestEndRecoveryWorkerDoesNotCompareLeaseAcrossClocks(t *testing.T) {
	fixture := newEndRecoveryFixture(t, StatusActive)
	fixture.worker.service.deps.Clock = &fakeClock{times: []time.Time{
		fixture.now,
		fixture.now.Add(2 * time.Minute),
	}}

	processed, err := fixture.worker.ProcessNext(t.Context())
	if err != nil || !processed {
		t.Fatalf("ProcessNext() = %t, %v, want successful recovery", processed, err)
	}
	if fixture.realtime.stopCalls != 1 ||
		fixture.repository.session.Status != StatusEnded {
		t.Fatalf(
			"Stop calls = %d, session = %#v",
			fixture.realtime.stopCalls,
			fixture.repository.session,
		)
	}
}

func TestEndRecoveryWorkerReportsRetryPersistenceFailure(t *testing.T) {
	fixture := newEndRecoveryFixture(t, StatusActive)
	fixture.realtime.stopResult.RuntimeState = RuntimeStopping
	fixture.repository.retryErr = errDependency

	processed, err := fixture.worker.ProcessNext(t.Context())
	if !processed || !errors.Is(err, ErrRealtimeStopFailed) ||
		!errors.Is(err, errDependency) {
		t.Fatalf("ProcessNext() = %t, %v, want stop and retry errors", processed, err)
	}
	if fixture.repository.intent.RetryCount != 0 ||
		fixture.repository.intent.RecoveryOwner == nil {
		t.Fatalf("intent = %#v, want original claim", fixture.repository.intent)
	}
}

func TestEndRecoveryWorkerReconcilesConcurrentTerminalTransition(t *testing.T) {
	fixture := newEndRecoveryFixture(t, StatusActive)
	fixture.repository.transitionErr = ErrConcurrentTransition
	fixture.repository.startRepository.getHook = func(context.Context) {
		fixture.repository.startRepository.mu.Lock()
		defer fixture.repository.startRepository.mu.Unlock()
		fixture.repository.session.Status = StatusEnded
		endedAt := fixture.now
		fixture.repository.session.EndedAt = &endedAt
	}

	processed, err := fixture.worker.ProcessNext(t.Context())
	if err != nil || !processed {
		t.Fatalf("ProcessNext() = %t, %v, want reconciled completion", processed, err)
	}
	if fixture.repository.intent.CompletedAt == nil {
		t.Fatal("intent remains incomplete")
	}
}

func TestEndRecoveryWorkerRejectsUnknownSessionStatus(t *testing.T) {
	fixture := newEndRecoveryFixture(t, Status("unknown"))

	processed, err := fixture.worker.ProcessNext(t.Context())
	if !processed || !errors.Is(err, ErrSessionStateConflict) {
		t.Fatalf("ProcessNext() = %t, %v, want state conflict", processed, err)
	}
	if fixture.repository.intent.RetryCount != 1 ||
		fixture.realtime.stopCalls != 0 {
		t.Fatalf(
			"intent = %#v, Stop calls = %d",
			fixture.repository.intent,
			fixture.realtime.stopCalls,
		)
	}
}

func TestEndRecoveryWorkerRejectsInvalidClaimTime(t *testing.T) {
	fixture := newEndRecoveryFixture(t, StatusActive)
	fixture.worker.service.deps.Clock = &fakeClock{times: []time.Time{{}}}

	processed, err := fixture.worker.ProcessNext(t.Context())
	if processed || !errors.Is(err, ErrInvalidDependency) {
		t.Fatalf("ProcessNext() = %t, %v, want clock failure", processed, err)
	}
}

func TestEndRecoveryWorkerRecoverClaimedRejectsInvalidState(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(*endRecoveryFixture, *EndIntent) context.Context
		want    error
	}{
		{
			name: "cancelled lock",
			prepare: func(_ *endRecoveryFixture, _ *EndIntent) context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			},
			want: context.Canceled,
		},
		{
			name: "invalid intent",
			prepare: func(_ *endRecoveryFixture, intent *EndIntent) context.Context {
				intent.TraceID = ""
				return t.Context()
			},
			want: ErrInvalidDependency,
		},
		{
			name: "zero end time",
			prepare: func(fixture *endRecoveryFixture, _ *EndIntent) context.Context {
				fixture.worker.service.deps.Clock = &fakeClock{}
				return t.Context()
			},
			want: ErrInvalidDependency,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newEndRecoveryFixture(t, StatusActive)
			owner := fixture.worker.config.WorkerID
			leaseExpiresAt := fixture.now.Add(time.Minute)
			intent := fixture.repository.intent
			intent.RecoveryOwner = &owner
			intent.LeaseExpiresAt = &leaseExpiresAt
			ctx := test.prepare(fixture, &intent)

			err := fixture.worker.recoverClaimed(ctx, intent)
			if !errors.Is(err, test.want) {
				t.Fatalf("recoverClaimed() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestEndRecoveryWorkerRecoverClaimedStopsAfterSessionReadFailure(t *testing.T) {
	fixture := newEndRecoveryFixture(t, StatusActive)
	clock := fixture.worker.service.deps.Clock.(*fakeClock)
	fixture.repository.startRepository.getErr = errDependency
	owner := fixture.worker.config.WorkerID
	leaseExpiresAt := fixture.now.Add(fixture.worker.config.LeaseDuration)
	intent := fixture.repository.intent
	intent.RecoveryOwner = &owner
	intent.LeaseExpiresAt = &leaseExpiresAt

	err := fixture.worker.recoverClaimed(t.Context(), intent)
	if !errors.Is(err, errDependency) {
		t.Fatalf("recoverClaimed() error = %v, want errDependency", err)
	}
	if fixture.repository.startRepository.getCalls != 1 || clock.calls != 0 ||
		fixture.repository.transitionCalls != 0 ||
		fixture.repository.completeCalls != 0 ||
		fixture.realtime.stopCalls != 0 {
		t.Fatalf(
			"side effects = get %d, clock %d, transition %d, complete %d, stop %d; want 1, 0, 0, 0, 0",
			fixture.repository.startRepository.getCalls,
			clock.calls,
			fixture.repository.transitionCalls,
			fixture.repository.completeCalls,
			fixture.realtime.stopCalls,
		)
	}
}

func TestEndRecoveryWorkerRecoverClaimedStopsAfterEndTimestampFailure(t *testing.T) {
	fixture := newEndRecoveryFixture(t, StatusActive)
	clock := &fakeClock{}
	fixture.worker.service.deps.Clock = clock
	owner := fixture.worker.config.WorkerID
	leaseExpiresAt := fixture.now.Add(fixture.worker.config.LeaseDuration)
	intent := fixture.repository.intent
	intent.RecoveryOwner = &owner
	intent.LeaseExpiresAt = &leaseExpiresAt

	err := fixture.worker.recoverClaimed(t.Context(), intent)
	if !errors.Is(err, ErrInvalidDependency) ||
		!strings.Contains(err.Error(), "recovered session end") {
		t.Fatalf("recoverClaimed() error = %v, want recovered-session-end clock failure", err)
	}
	if fixture.repository.startRepository.getCalls != 1 || clock.calls != 1 ||
		fixture.repository.transitionCalls != 0 ||
		fixture.repository.completeCalls != 0 ||
		fixture.realtime.stopCalls != 0 {
		t.Fatalf(
			"side effects = get %d, clock %d, transition %d, complete %d, stop %d; want 1, 1, 0, 0, 0",
			fixture.repository.startRepository.getCalls,
			clock.calls,
			fixture.repository.transitionCalls,
			fixture.repository.completeCalls,
			fixture.realtime.stopCalls,
		)
	}
}

func TestEndRecoveryWorkerReconcileReturnsReadAndTransitionErrors(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(*endRecoveryFixture)
		want    error
	}{
		{
			name: "read failure",
			prepare: func(fixture *endRecoveryFixture) {
				fixture.repository.startRepository.getErr = errDependency
			},
			want: errDependency,
		},
		{
			name:    "session remains active",
			prepare: func(*endRecoveryFixture) {},
			want:    ErrConcurrentTransition,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newEndRecoveryFixture(t, StatusActive)
			test.prepare(fixture)

			err := fixture.worker.reconcileTransition(
				t.Context(), fixture.repository.intent, ErrConcurrentTransition,
			)
			if !errors.Is(err, test.want) {
				t.Fatalf("reconcileTransition() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestEndRecoveryWorkerReconcileReturnsNonConcurrentErrorWithoutRead(t *testing.T) {
	fixture := newEndRecoveryFixture(t, StatusActive)
	transitionErr := errors.New("transition dependency failed")

	err := fixture.worker.reconcileTransition(
		t.Context(), fixture.repository.intent, transitionErr,
	)
	if err != transitionErr {
		t.Fatalf("reconcileTransition() error = %v, want original error %v", err, transitionErr)
	}
	if fixture.repository.startRepository.getCalls != 0 {
		t.Fatalf("GetOwned() calls = %d, want 0", fixture.repository.startRepository.getCalls)
	}
}

func TestEndRecoveryWorkerCompleteRejectsZeroClock(t *testing.T) {
	fixture := newEndRecoveryFixture(t, StatusEnded)
	fixture.worker.service.deps.Clock = &fakeClock{}

	err := fixture.worker.completeClaimed(t.Context(), fixture.repository.intent)
	if !errors.Is(err, ErrInvalidDependency) {
		t.Fatalf("completeClaimed() error = %v, want ErrInvalidDependency", err)
	}
}

func TestEndRecoveryWorkerCancelsAttemptBeforeLeaseExpiry(t *testing.T) {
	fixture := newEndRecoveryFixture(t, StatusActive)
	fixture.worker.config.AttemptTimeout = time.Millisecond
	fixture.worker.config.LeaseDuration = time.Second
	fixture.realtime.stopHook = func(ctx context.Context) {
		<-ctx.Done()
		fixture.realtime.mu.Lock()
		fixture.realtime.stopErr = ctx.Err()
		fixture.realtime.mu.Unlock()
	}

	processed, err := fixture.worker.ProcessNext(t.Context())
	if !processed || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ProcessNext() = %t, %v, want deadline", processed, err)
	}
	if fixture.repository.intent.RecoveryOwner != nil ||
		fixture.repository.intent.LeaseExpiresAt != nil ||
		fixture.repository.intent.RetryCount != 1 {
		t.Fatalf("retry intent = %#v", fixture.repository.intent)
	}
}

func TestEndRecoveryBackoffIsBounded(t *testing.T) {
	tests := []struct {
		name       string
		retryCount int
		want       time.Duration
	}{
		{name: "initial", retryCount: 0, want: time.Second},
		{name: "second", retryCount: 1, want: 2 * time.Second},
		{name: "third", retryCount: 2, want: 4 * time.Second},
		{name: "capped", retryCount: 20, want: 8 * time.Second},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := endRecoveryBackoff(time.Second, 8*time.Second, test.retryCount); got != test.want {
				t.Fatalf("endRecoveryBackoff(1s, 8s, %d) = %v, want %v", test.retryCount, got, test.want)
			}
		})
	}
}

type endRecoveryFixture struct {
	now        time.Time
	worker     *EndRecoveryWorker
	repository *endRecoveryRepository
	realtime   *startRealtime
}

func newEndRecoveryFixture(t *testing.T, status Status) *endRecoveryFixture {
	t.Helper()
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	session := VoiceSession{
		ID:        "vs_recovery",
		AccountID: "acct_recovery",
		Status:    status,
		CreatedAt: now.Add(-time.Hour),
	}
	if status != StatusCreated {
		startedAt := now.Add(-30 * time.Minute)
		session.StartedAt = &startedAt
	}
	if status == StatusEnded || status == StatusFailed {
		endedAt := now.Add(-time.Minute)
		session.EndedAt = &endedAt
	}
	repository := &endRecoveryRepository{
		endRepository: &endRepository{
			startRepository: &startRepository{session: session},
		},
		intent: EndIntent{
			SessionID:      session.ID,
			AccountID:      session.AccountID,
			Reason:         EndReasonUserRequested,
			IdempotencyKey: "end_recovery",
			RequestHash:    "hash_recovery",
			TraceID:        "trace_recovery",
			RequestedAt:    now.Add(-time.Minute),
			NextAttemptAt:  now,
		},
		storageNow: now,
	}
	realtime := &startRealtime{stopResult: RuntimeSnapshot{
		SessionID: session.ID, RuntimeState: RuntimeStopped, UpdatedAt: now,
	}}
	service := newSharedStartService(
		t,
		repository,
		&fakeLanguageConfigReader{},
		&fakeWebRTCConnectionReader{},
		realtime,
		&fakeClock{now: now},
	)
	worker, err := NewEndRecoveryWorker(service, EndRecoveryConfig{
		WorkerID:       "worker_1",
		PollInterval:   time.Minute,
		LeaseDuration:  time.Minute,
		AttemptTimeout: 30 * time.Second,
		InitialBackoff: time.Second,
		MaxBackoff:     8 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewEndRecoveryWorker() error = %v", err)
	}
	return &endRecoveryFixture{
		now: now, worker: worker, repository: repository, realtime: realtime,
	}
}

type endRecoveryRepository struct {
	*endRepository
	intent          EndIntent
	transitionErr   error
	transitionCalls int
	completeErr     error
	completeCalls   int
	claimErr        error
	claimCalls      int
	claimHook       func(int)
	claimResultHook func(*EndIntent)
	retryErr        error
	retryCalls      int
	retryAfter      time.Duration
	storageNow      time.Time
}

type cancelWriter struct {
	cancel context.CancelFunc
}

func (w cancelWriter) Write(p []byte) (int, error) {
	w.cancel()
	return len(p), nil
}

func (r *endRecoveryRepository) TransitionToEnded(
	ctx context.Context,
	params EndTransitionParams,
) (VoiceSession, error) {
	r.transitionCalls++
	if r.transitionErr != nil {
		return VoiceSession{}, r.transitionErr
	}
	return r.endRepository.TransitionToEnded(ctx, params)
}

func (r *endRecoveryRepository) ClaimPendingEndIntent(
	_ context.Context,
	params ClaimEndIntentParams,
) (EndIntent, bool, error) {
	r.claimCalls++
	if r.claimHook != nil {
		r.claimHook(r.claimCalls)
	}
	if r.claimErr != nil {
		return EndIntent{}, false, r.claimErr
	}
	if r.intent.Completed() || r.intent.NextAttemptAt.After(params.ClaimedAt) ||
		(r.intent.LeaseExpiresAt != nil && r.intent.LeaseExpiresAt.After(params.ClaimedAt)) {
		return EndIntent{}, false, nil
	}
	owner := params.WorkerID
	lease := params.LeaseExpiresAt
	r.intent.RecoveryOwner = &owner
	r.intent.LeaseExpiresAt = &lease
	claimed := r.intent
	if r.claimResultHook != nil {
		r.claimResultHook(&claimed)
	}
	return claimed, true, nil
}

func (r *endRecoveryRepository) RetryClaimedEndIntent(
	_ context.Context,
	params RetryEndIntentParams,
) error {
	r.retryCalls++
	if r.retryErr != nil {
		return r.retryErr
	}
	if r.intent.RecoveryOwner == nil || *r.intent.RecoveryOwner != params.WorkerID {
		return ErrConcurrentTransition
	}
	r.intent.RetryCount++
	lastError := params.LastError
	r.intent.LastError = &lastError
	r.retryAfter = params.RetryAfter
	r.intent.NextAttemptAt = r.storageNow.Add(params.RetryAfter)
	r.intent.RecoveryOwner = nil
	r.intent.LeaseExpiresAt = nil
	return nil
}

func (r *endRecoveryRepository) CompleteClaimedEndIntent(
	_ context.Context,
	params CompleteClaimedEndIntentParams,
) error {
	r.completeCalls++
	if r.completeErr != nil {
		return r.completeErr
	}
	if r.intent.Completed() {
		return nil
	}
	if r.intent.RecoveryOwner == nil || *r.intent.RecoveryOwner != params.WorkerID {
		return ErrConcurrentTransition
	}
	r.intent.CompletedAt = &params.CompletedAt
	r.intent.RecoveryOwner = nil
	r.intent.LeaseExpiresAt = nil
	return nil
}

func newRestartedEndRecoveryWorker(
	t *testing.T,
	fixture *endRecoveryFixture,
	workerID string,
) *EndRecoveryWorker {
	t.Helper()
	service := newSharedStartService(
		t,
		fixture.repository,
		&fakeLanguageConfigReader{},
		&fakeWebRTCConnectionReader{},
		fixture.realtime,
		&fakeClock{now: fixture.now},
	)
	worker, err := NewEndRecoveryWorker(service, EndRecoveryConfig{
		WorkerID:       workerID,
		PollInterval:   time.Minute,
		LeaseDuration:  time.Minute,
		AttemptTimeout: 30 * time.Second,
		InitialBackoff: time.Second,
		MaxBackoff:     8 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewEndRecoveryWorker() error = %v", err)
	}
	return worker
}
