package sessions

import "testing"

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
