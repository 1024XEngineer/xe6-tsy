package reception

import (
	"context"
	"sort"
	"sync"
)

type MemoryStore struct {
	mu          sync.RWMutex
	sessions    map[string]ReceptionSession
	bindings    map[string]MediaTrackBinding
	idempotency map[string]IdempotencyRecord
	events      []DomainEvent
	audits      []AuditEntry
	failCommit  bool
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		sessions: make(map[string]ReceptionSession), bindings: make(map[string]MediaTrackBinding),
		idempotency: make(map[string]IdempotencyRecord),
	}
}

func (s *MemoryStore) GetSession(_ context.Context, id string) (ReceptionSession, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	session, ok := s.sessions[id]
	if !ok {
		return ReceptionSession{}, businessError(CodeSessionNotFound, "接待会话不存在。")
	}
	return session, nil
}

func (s *MemoryStore) GetBinding(_ context.Context, id string) (MediaTrackBinding, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	binding, ok := s.bindings[id]
	if !ok {
		return MediaTrackBinding{}, businessError(CodeBindingNotFound, "媒体绑定不存在。")
	}
	return binding, nil
}

func (s *MemoryStore) BindingsBySession(_ context.Context, sessionID string) ([]MediaTrackBinding, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	bindings := make([]MediaTrackBinding, 0)
	for _, binding := range s.bindings {
		if binding.SessionID == sessionID {
			bindings = append(bindings, binding)
		}
	}
	sort.Slice(bindings, func(i, j int) bool { return bindings[i].BindingID < bindings[j].BindingID })
	return bindings, nil
}

func (s *MemoryStore) FindActiveBinding(_ context.Context, sessionID string) (*MediaTrackBinding, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, binding := range s.bindings {
		if binding.SessionID == sessionID && (binding.Status == MediaTrackPending || binding.Status == MediaTrackAttached) {
			copy := binding
			return &copy, nil
		}
	}
	return nil, nil
}

func (s *MemoryStore) Replay(_ context.Context, operation, key string) (*IdempotencyRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.idempotency[operation+":"+key]
	if !ok {
		return nil, nil
	}
	copy := record
	return &copy, nil
}

func (s *MemoryStore) Commit(_ context.Context, mutation Mutation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failCommit {
		return businessError(CodeInternalError, "内存事务提交失败。")
	}
	if mutation.Session != nil {
		s.sessions[mutation.Session.SessionID] = *mutation.Session
	}
	if mutation.Binding != nil {
		s.bindings[mutation.Binding.BindingID] = *mutation.Binding
	}
	s.events = append(s.events, mutation.Events...)
	s.audits = append(s.audits, mutation.Audits...)
	if mutation.Idempotency != nil {
		s.idempotency[mutation.Idempotency.Operation+":"+mutation.Idempotency.Key] = *mutation.Idempotency
	}
	return nil
}

func (s *MemoryStore) Events() []DomainEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]DomainEvent(nil), s.events...)
}

func (s *MemoryStore) Audits() []AuditEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]AuditEntry(nil), s.audits...)
}

func (s *MemoryStore) SessionCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.sessions)
}

func (s *MemoryStore) BindingCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.bindings)
}

func (s *MemoryStore) SetCommitFailure(fail bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failCommit = fail
}
