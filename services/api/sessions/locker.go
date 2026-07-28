package sessions

import (
	"context"
	"sync"
)

// keyedLocker serializes lifecycle operations within one process. Repository
// conditional transitions remain the cross-process consistency boundary.
type keyedLocker struct {
	mu    sync.Mutex
	locks map[string]*keyedLockEntry
}

type keyedLockEntry struct {
	token      chan struct{}
	references int
}

func newKeyedLocker() keyedLocker {
	return keyedLocker{locks: make(map[string]*keyedLockEntry)}
}

func newKeyedLockEntry() *keyedLockEntry {
	entry := &keyedLockEntry{token: make(chan struct{}, 1)}
	entry.token <- struct{}{}
	return entry
}

func (l *keyedLocker) lock(ctx context.Context, key string) (func(), error) {
	l.mu.Lock()
	entry := l.locks[key]
	if entry == nil {
		entry = newKeyedLockEntry()
		l.locks[key] = entry
	}
	entry.references++
	l.mu.Unlock()

	select {
	case <-ctx.Done():
		l.releaseReference(key, entry)
		return nil, ctx.Err()
	case <-entry.token:
		if err := ctx.Err(); err != nil {
			entry.token <- struct{}{}
			l.releaseReference(key, entry)
			return nil, err
		}
		var once sync.Once
		return func() {
			once.Do(func() {
				entry.token <- struct{}{}
				l.releaseReference(key, entry)
			})
		}, nil
	}
}

func (l *keyedLocker) releaseReference(key string, entry *keyedLockEntry) {
	// Waiters increment references before blocking, so zero references makes
	// the entry safe to reclaim without racing a queued operation.
	l.mu.Lock()
	defer l.mu.Unlock()
	entry.references--
	if entry.references == 0 && l.locks[key] == entry {
		delete(l.locks, key)
	}
}
