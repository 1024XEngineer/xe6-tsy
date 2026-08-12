package device

import (
	"strings"
	"sync"
)

// Snapshot is the client-side read model used by a device UI or telemetry
// adapter. Each field remains owned by the corresponding realtime authority.
type Snapshot struct {
	SessionID  string
	Connection ConnectionSnapshot
	Runtime    RuntimeSnapshot
	Mode       ModeStateSnapshot
	HasConnect bool
	HasRuntime bool
	HasMode    bool
}

// StateStore accepts only monotonic observations. It prevents a delayed
// packet from an old connection or runtime instance from regressing display
// state after a reconnect.
type StateStore struct {
	mu                sync.Mutex
	session           string
	snapshot          Snapshot
	retiredConnectIDs map[string]struct{}
	retiredStartOps   map[string]struct{}
	retiredRuntimeIDs map[string]struct{}
}

func NewStateStore(sessionID string) (*StateStore, error) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, ErrInvalidConfig
	}
	return &StateStore{
		session:           sessionID,
		snapshot:          Snapshot{SessionID: sessionID},
		retiredConnectIDs: make(map[string]struct{}),
		retiredStartOps:   make(map[string]struct{}),
		retiredRuntimeIDs: make(map[string]struct{}),
	}, nil
}

func (s *StateStore) ApplyConnection(next ConnectionSnapshot) bool {
	if s == nil || next.SessionID != s.session || next.ConnectionID == "" || !next.State.Valid() {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.snapshot.HasConnect {
		previous := s.snapshot.Connection
		if previous.ConnectionID == next.ConnectionID {
			if next.Version <= previous.Version {
				return false
			}
		} else {
			if _, retired := s.retiredConnectIDs[next.ConnectionID]; retired || !next.UpdatedAt.After(previous.UpdatedAt) {
				return false
			}
			s.retiredConnectIDs[previous.ConnectionID] = struct{}{}
		}
	}
	s.snapshot.Connection, s.snapshot.HasConnect = next, true
	return true
}

func (s *StateStore) ApplyRuntime(next RuntimeSnapshot) bool {
	if s == nil || next.SessionID != s.session || strings.TrimSpace(next.StartOperationID) == "" || !next.RuntimeState.Valid() || next.UpdatedAt.IsZero() {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.snapshot.HasRuntime {
		previous := s.snapshot.Runtime
		if previous.StartOperationID == next.StartOperationID {
			if !next.UpdatedAt.After(previous.UpdatedAt) {
				return false
			}
		} else {
			if _, retired := s.retiredStartOps[next.StartOperationID]; retired || !next.UpdatedAt.After(previous.UpdatedAt) {
				return false
			}
			s.retiredStartOps[previous.StartOperationID] = struct{}{}
		}
	}
	s.snapshot.Runtime, s.snapshot.HasRuntime = next, true
	return true
}

func (s *StateStore) ApplyMode(next ModeStateSnapshot) bool {
	if s == nil || next.SessionID != s.session || !validModeState(next, s.session) {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.snapshot.HasMode {
		previous := s.snapshot.Mode
		if previous.RuntimeInstanceID == next.RuntimeInstanceID {
			if next.Generation < previous.Generation ||
				(next.Generation == previous.Generation && !next.UpdatedAt.After(previous.UpdatedAt)) {
				return false
			}
		} else {
			if _, retired := s.retiredRuntimeIDs[next.RuntimeInstanceID]; retired || !next.UpdatedAt.After(previous.UpdatedAt) {
				return false
			}
			s.retiredRuntimeIDs[previous.RuntimeInstanceID] = struct{}{}
		}
	}
	s.snapshot.Mode, s.snapshot.HasMode = next, true
	return true
}

func (s *StateStore) Snapshot() Snapshot {
	if s == nil {
		return Snapshot{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshot
}

// RuntimeInstanceChanged reports whether a fresh mode snapshot belongs to a
// different runtime. Callers should discard queued commands in that case.
func (s *StateStore) RuntimeInstanceChanged(next ModeStateSnapshot) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.snapshot.HasMode {
		return false
	}
	return s.snapshot.Mode.RuntimeInstanceID != next.RuntimeInstanceID
}
