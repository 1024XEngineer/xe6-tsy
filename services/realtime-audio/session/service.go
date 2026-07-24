package session

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Dependencies contains the required ports for lifecycle orchestration.
type Dependencies struct {
	Sessions    SessionReader
	Runtimes    RuntimeRepository
	Pipelines   PipelineManager
	Connections WebRTCConnectionManager
	Now         func() time.Time
}

// LifecycleService coordinates media resources without changing business state.
type LifecycleService struct {
	deps  Dependencies
	locks keyedLocker
}

// NewLifecycleService validates dependencies before exposing lifecycle methods.
func NewLifecycleService(deps Dependencies) (*LifecycleService, error) {
	if deps.Sessions == nil || deps.Runtimes == nil || deps.Pipelines == nil || deps.Connections == nil || deps.Now == nil {
		return nil, ErrInvalidDependency
	}
	return &LifecycleService{deps: deps, locks: newKeyedLocker()}, nil
}

// Start creates one pipeline for a created business session and publishes listening state.
// A per-session lock closes the read-before-write race between concurrent start requests.
func (s *LifecycleService) Start(ctx context.Context, command StartRealtimeCommand) (RuntimeSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return RuntimeSnapshot{}, err
	}
	if command.SessionID == "" {
		return RuntimeSnapshot{}, ErrSessionIDRequired
	}

	unlock := s.locks.lock(command.SessionID)
	defer unlock()

	current, err := s.deps.Runtimes.Get(ctx, command.SessionID)
	if err == nil && current.RuntimeState != RuntimeStopped && current.RuntimeState != RuntimeFailed {
		return current, nil
	}
	if err != nil && !errors.Is(err, ErrRuntimeNotFound) {
		return RuntimeSnapshot{}, fmt.Errorf("read runtime: %w", err)
	}

	business, err := s.deps.Sessions.GetSession(ctx, command.SessionID)
	if err != nil {
		return RuntimeSnapshot{}, fmt.Errorf("read session: %w", err)
	}
	if business.Status != "created" {
		return RuntimeSnapshot{}, ErrSessionNotCreated
	}

	starting := RuntimeSnapshot{
		SessionID:    command.SessionID,
		RuntimeState: RuntimeStarting,
		UpdatedAt:    s.deps.Now(),
	}
	if err := s.deps.Runtimes.Save(ctx, starting); err != nil {
		return RuntimeSnapshot{}, fmt.Errorf("save starting runtime: %w", err)
	}

	if err := s.deps.Pipelines.Start(ctx, business); err != nil {
		failed := failureSnapshot(command.SessionID, ErrorCodeStartFailed, s.deps.Now())
		if saveErr := s.deps.Runtimes.Save(ctx, failed); saveErr != nil {
			return failed, errors.Join(fmt.Errorf("start pipeline: %w", err), fmt.Errorf("save failed runtime: %w", saveErr))
		}
		return failed, fmt.Errorf("start pipeline: %w", err)
	}

	listening := RuntimeSnapshot{
		SessionID:    command.SessionID,
		RuntimeState: RuntimeListening,
		UpdatedAt:    s.deps.Now(),
	}
	if err := s.deps.Runtimes.Save(ctx, listening); err != nil {
		return listening, fmt.Errorf("save listening runtime: %w", err)
	}
	return listening, nil
}

// GetRuntimeState returns the repository snapshot without synthesizing business state.
func (s *LifecycleService) GetRuntimeState(ctx context.Context, sessionID string) (RuntimeSnapshot, error) {
	return s.deps.Runtimes.Get(ctx, sessionID)
}

func failureSnapshot(sessionID string, errorCode string, now time.Time) RuntimeSnapshot {
	return RuntimeSnapshot{
		SessionID:     sessionID,
		RuntimeState:  RuntimeFailed,
		LastErrorCode: &errorCode,
		UpdatedAt:     now,
	}
}

type keyedLocker struct {
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

func newKeyedLocker() keyedLocker {
	return keyedLocker{locks: make(map[string]*sync.Mutex)}
}

func (l *keyedLocker) lock(key string) func() {
	l.mu.Lock()
	mutex := l.locks[key]
	if mutex == nil {
		mutex = &sync.Mutex{}
		l.locks[key] = mutex
	}
	l.mu.Unlock()

	mutex.Lock()
	return mutex.Unlock
}
