package session

import (
	"context"
	"sync"
)

// MemoryRuntimeRepository stores snapshots for local development and tests.
// The mutex and value cloning keep concurrent callers from sharing mutable state.
type MemoryRuntimeRepository struct {
	mu        sync.RWMutex
	snapshots map[string]RuntimeSnapshot
}

// NewMemoryRuntimeRepository creates an empty repository ready for use.
func NewMemoryRuntimeRepository() *MemoryRuntimeRepository {
	return &MemoryRuntimeRepository{snapshots: make(map[string]RuntimeSnapshot)}
}

// Get returns a copy of the snapshot identified by sessionID.
func (r *MemoryRuntimeRepository) Get(ctx context.Context, sessionID string) (RuntimeSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return RuntimeSnapshot{}, err
	}
	if sessionID == "" {
		return RuntimeSnapshot{}, ErrSessionIDRequired
	}

	r.mu.RLock()
	snapshot, ok := r.snapshots[sessionID]
	r.mu.RUnlock()
	if !ok {
		return RuntimeSnapshot{}, ErrRuntimeNotFound
	}
	return cloneRuntimeSnapshot(snapshot), nil
}

// Save replaces a snapshot after validating the context and ownership key.
func (r *MemoryRuntimeRepository) Save(ctx context.Context, snapshot RuntimeSnapshot) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if snapshot.SessionID == "" {
		return ErrSessionIDRequired
	}

	r.mu.Lock()
	r.snapshots[snapshot.SessionID] = cloneRuntimeSnapshot(snapshot)
	r.mu.Unlock()
	return nil
}

func cloneRuntimeSnapshot(snapshot RuntimeSnapshot) RuntimeSnapshot {
	snapshot.CurrentTurnID = cloneString(snapshot.CurrentTurnID)
	snapshot.CurrentPlaybackID = cloneString(snapshot.CurrentPlaybackID)
	snapshot.LastErrorCode = cloneString(snapshot.LastErrorCode)
	return snapshot
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}
