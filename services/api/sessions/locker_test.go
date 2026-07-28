package sessions

import (
	"runtime"
	"testing"
)

func TestKeyedLockerAllowsDifferentKeysAndReclaimsEntries(t *testing.T) {
	locker := newKeyedLocker()

	unlockFirst := locker.lock("vs_1")
	unlockSecond := locker.lock("vs_2")

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
