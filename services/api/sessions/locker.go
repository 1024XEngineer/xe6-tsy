package sessions

import "sync"

// keyedLocker serializes lifecycle operations within one process. Repository
// conditional transitions remain the cross-process consistency boundary.
type keyedLocker struct {
	mu    sync.Mutex
	locks map[string]*keyedLockEntry
}

type keyedLockEntry struct {
	mutex      sync.Mutex
	references int
}

func newKeyedLocker() keyedLocker {
	return keyedLocker{locks: make(map[string]*keyedLockEntry)}
}

func (l *keyedLocker) lock(key string) func() {
	l.mu.Lock()
	entry := l.locks[key]
	if entry == nil {
		entry = &keyedLockEntry{}
		l.locks[key] = entry
	}
	entry.references++
	l.mu.Unlock()

	entry.mutex.Lock()
	return func() {
		entry.mutex.Unlock()

		// Waiters increment references before blocking, so zero references makes
		// the entry safe to reclaim without racing a queued operation.
		l.mu.Lock()
		entry.references--
		if entry.references == 0 && l.locks[key] == entry {
			delete(l.locks, key)
		}
		l.mu.Unlock()
	}
}
