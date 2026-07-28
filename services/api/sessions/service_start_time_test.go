package sessions

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestServiceStartRejectsZeroBeginTimestamp(t *testing.T) {
	fixture := newStartFixture(t, StatusCreated)
	fixture.clock.now = time.Time{}

	_, err := fixture.service.Start(context.Background(), validStartInput())
	if !errors.Is(err, ErrInvalidDependency) {
		t.Fatalf("Start() error = %v, want ErrInvalidDependency", err)
	}
	if fixture.repository.beginCalls != 0 || fixture.realtime.startCalls != 0 {
		t.Fatalf("calls = begin %d, realtime start %d; want 0, 0",
			fixture.repository.beginCalls, fixture.realtime.startCalls)
	}
}

func TestServiceStartCompensatesWhenStartedTimestampIsZero(t *testing.T) {
	fixture := newStartFixture(t, StatusCreated)
	fixture.service.deps.Clock = newSequenceClock(
		fixture.clock.now,
		time.Time{},
		fixture.clock.now.Add(time.Second),
		fixture.clock.now.Add(2*time.Second),
	)

	_, err := fixture.service.Start(context.Background(), validStartInput())
	if !errors.Is(err, ErrInvalidDependency) {
		t.Fatalf("Start() error = %v, want ErrInvalidDependency", err)
	}
	if len(fixture.repository.transitions) != 0 ||
		fixture.repository.claimCalls != 1 ||
		fixture.realtime.stopCalls != 1 ||
		fixture.repository.completeCalls != 1 {
		t.Fatalf(
			"calls = transition %d, claim %d, stop %d, complete %d; want 0, 1, 1, 1",
			len(fixture.repository.transitions),
			fixture.repository.claimCalls,
			fixture.realtime.stopCalls,
			fixture.repository.completeCalls,
		)
	}
}

func TestServiceStartDoesNotClaimOrStopWhenCompensationTimestampIsZero(t *testing.T) {
	fixture := newStartFixture(t, StatusCreated)
	fixture.repository.transitionErr = errDependency
	fixture.service.deps.Clock = newSequenceClock(
		fixture.clock.now,
		fixture.clock.now.Add(time.Second),
		time.Time{},
	)

	_, err := fixture.service.Start(context.Background(), validStartInput())
	if !errors.Is(err, errDependency) || !errors.Is(err, ErrInvalidDependency) {
		t.Fatalf("Start() error = %v, want transition and invalid dependency errors", err)
	}
	if fixture.repository.claimCalls != 0 || fixture.realtime.stopCalls != 0 {
		t.Fatalf("calls = claim %d, stop %d; want 0, 0",
			fixture.repository.claimCalls, fixture.realtime.stopCalls)
	}
	if fixture.repository.operation.Status != StartOperationPending {
		t.Fatalf("operation status = %q, want pending", fixture.repository.operation.Status)
	}
}

func TestServiceStartLeavesCompensatingWhenCompletionTimestampIsZero(t *testing.T) {
	fixture := newStartFixture(t, StatusCreated)
	fixture.repository.transitionErr = errDependency
	fixture.service.deps.Clock = newSequenceClock(
		fixture.clock.now,
		fixture.clock.now.Add(time.Second),
		fixture.clock.now.Add(2*time.Second),
		time.Time{},
	)

	_, err := fixture.service.Start(context.Background(), validStartInput())
	if !errors.Is(err, errDependency) || !errors.Is(err, ErrInvalidDependency) {
		t.Fatalf("Start() error = %v, want transition and invalid dependency errors", err)
	}
	if fixture.repository.completeCalls != 0 {
		t.Fatalf("CompleteStartCompensation calls = %d, want 0", fixture.repository.completeCalls)
	}
	if fixture.repository.operation.Status != StartOperationCompensating {
		t.Fatalf("operation status = %q, want compensating", fixture.repository.operation.Status)
	}
}

func TestServiceStartLeavesCompensatingWhenFailureTimestampIsZero(t *testing.T) {
	fixture := newStartFixture(t, StatusCreated)
	fixture.repository.transitionErr = errDependency
	fixture.realtime.stopErr = errDependency
	fixture.service.deps.Clock = newSequenceClock(
		fixture.clock.now,
		fixture.clock.now.Add(time.Second),
		fixture.clock.now.Add(2*time.Second),
		time.Time{},
	)

	_, err := fixture.service.Start(context.Background(), validStartInput())
	if !errors.Is(err, errDependency) ||
		!errors.Is(err, ErrRealtimeStopFailed) ||
		!errors.Is(err, ErrInvalidDependency) {
		t.Fatalf("Start() error = %v, want transition, stop, and invalid dependency errors", err)
	}
	if fixture.repository.failCalls != 0 {
		t.Fatalf("FailStartCompensation calls = %d, want 0", fixture.repository.failCalls)
	}
	if fixture.repository.operation.Status != StartOperationCompensating {
		t.Fatalf("operation status = %q, want compensating", fixture.repository.operation.Status)
	}
}

type sequenceClock struct {
	mu    sync.Mutex
	times []time.Time
	next  int
}

func newSequenceClock(times ...time.Time) *sequenceClock {
	return &sequenceClock{times: times}
}

func (c *sequenceClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.next >= len(c.times) {
		return time.Time{}
	}
	now := c.times[c.next]
	c.next++
	return now
}
