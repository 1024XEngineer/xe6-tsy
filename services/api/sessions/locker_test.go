package sessions

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"
)

func TestKeyedLockerAllowsDifferentKeysAndReclaimsEntries(t *testing.T) {
	locker := newKeyedLocker()

	unlockFirst, err := locker.lock(context.Background(), "vs_1")
	if err != nil {
		t.Fatalf("lock(vs_1) error = %v", err)
	}
	unlockSecond, err := locker.lock(context.Background(), "vs_2")
	if err != nil {
		t.Fatalf("lock(vs_2) error = %v", err)
	}

	locker.mu.Lock()
	if len(locker.locks) != 2 {
		t.Fatalf("lock entries = %d, want 2", len(locker.locks))
	}
	locker.mu.Unlock()

	unlockSecond()
	unlockFirst()

	locker.mu.Lock()
	defer locker.mu.Unlock()
	if len(locker.locks) != 0 {
		t.Fatalf("lock entries after release = %d, want 0", len(locker.locks))
	}
}

func TestKeyedLockerWaitCanBeCancelled(t *testing.T) {
	locker := newKeyedLocker()
	unlock, err := locker.lock(context.Background(), "vs_1")
	if err != nil {
		t.Fatalf("first lock error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, lockErr := locker.lock(ctx, "vs_1")
		result <- lockErr
	}()
	waitForLockReferences(t, &locker, "vs_1", 2)
	cancel()

	select {
	case lockErr := <-result:
		if !errors.Is(lockErr, context.Canceled) {
			t.Fatalf("second lock error = %v, want context.Canceled", lockErr)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled waiter did not return before the holder released")
	}
	waitForLockReferences(t, &locker, "vs_1", 1)
	unlock()
	assertKeyedLockerEmpty(t, &locker)
}

func TestKeyedLockerWaitHonorsDeadline(t *testing.T) {
	locker := newKeyedLocker()
	unlock, err := locker.lock(context.Background(), "vs_1")
	if err != nil {
		t.Fatalf("first lock error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, lockErr := locker.lock(ctx, "vs_1")
		result <- lockErr
	}()
	waitForLockReferences(t, &locker, "vs_1", 2)

	select {
	case lockErr := <-result:
		if !errors.Is(lockErr, context.DeadlineExceeded) {
			t.Fatalf("second lock error = %v, want context.DeadlineExceeded", lockErr)
		}
	case <-time.After(time.Second):
		t.Fatal("deadline waiter did not return before the holder released")
	}
	unlock()
	assertKeyedLockerEmpty(t, &locker)
}

func TestKeyedLockerLiveWaiterProceedsAfterCancelledWaiter(t *testing.T) {
	locker := newKeyedLocker()
	unlockHolder, err := locker.lock(context.Background(), "vs_1")
	if err != nil {
		t.Fatalf("holder lock error = %v", err)
	}

	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancelledResult := make(chan error, 1)
	go func() {
		_, lockErr := locker.lock(cancelledCtx, "vs_1")
		cancelledResult <- lockErr
	}()
	liveResult := make(chan struct {
		unlock func()
		err    error
	}, 1)
	go func() {
		unlock, lockErr := locker.lock(context.Background(), "vs_1")
		liveResult <- struct {
			unlock func()
			err    error
		}{unlock: unlock, err: lockErr}
	}()
	waitForLockReferences(t, &locker, "vs_1", 3)
	cancel()

	select {
	case lockErr := <-cancelledResult:
		if !errors.Is(lockErr, context.Canceled) {
			t.Fatalf("cancelled waiter error = %v, want context.Canceled", lockErr)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled waiter did not return")
	}
	unlockHolder()

	select {
	case result := <-liveResult:
		if result.err != nil {
			t.Fatalf("live waiter error = %v", result.err)
		}
		result.unlock()
	case <-time.After(time.Second):
		t.Fatal("live waiter did not acquire the released token")
	}
	assertKeyedLockerEmpty(t, &locker)
}

func TestKeyedLockerCancelledContextCoversTokenRace(t *testing.T) {
	// Free lock + already-canceled ctx makes both select cases ready. Go picks
	// uniformly at random, so enough iterations cover the post-token ctx.Err
	// branch that otherwise flakes under CI -race scheduling (locker.go ~89%).
	for range 2_000 {
		locker := newKeyedLocker()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		unlock, err := locker.lock(ctx, "vs_race")
		if unlock != nil {
			t.Fatal("cancelled lock returned unlock func")
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("lock error = %v, want context.Canceled", err)
		}
		assertKeyedLockerEmpty(t, &locker)
	}
}

func TestKeyedLockerUnlockIsIdempotent(t *testing.T) {
	locker := newKeyedLocker()
	unlock, err := locker.lock(context.Background(), "vs_1")
	if err != nil {
		t.Fatalf("lock error = %v", err)
	}

	unlock()
	unlock()

	assertKeyedLockerEmpty(t, &locker)
	nextUnlock, err := locker.lock(context.Background(), "vs_1")
	if err != nil {
		t.Fatalf("next lock error = %v", err)
	}
	nextUnlock()
	assertKeyedLockerEmpty(t, &locker)
}

func assertKeyedLockerEmpty(t *testing.T, locker *keyedLocker) {
	t.Helper()
	locker.mu.Lock()
	defer locker.mu.Unlock()
	if len(locker.locks) != 0 {
		t.Fatalf("lock entries = %d, want 0", len(locker.locks))
	}
}

func waitForLockReferences(
	t *testing.T,
	locker *keyedLocker,
	key string,
	want int,
) {
	t.Helper()
	for range 10_000 {
		locker.mu.Lock()
		entry := locker.locks[key]
		got := 0
		if entry != nil {
			got = entry.references
		}
		locker.mu.Unlock()
		if got == want {
			return
		}
		runtime.Gosched()
	}
	t.Fatalf("lock references for %q did not reach %d", key, want)
}
