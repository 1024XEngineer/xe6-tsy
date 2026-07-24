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

// Stop releases pipeline and WebRTC resources before publishing stopped state.
// Both cleanup calls run after stopping begins so a partial failure cannot leak the other resource.
func (s *LifecycleService) Stop(ctx context.Context, command StopRealtimeCommand) (RuntimeSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return RuntimeSnapshot{}, err
	}
	if command.SessionID == "" {
		return RuntimeSnapshot{}, ErrSessionIDRequired
	}

	unlock := s.locks.lock(command.SessionID)
	defer unlock()

	current, err := s.deps.Runtimes.Get(ctx, command.SessionID)
	if errors.Is(err, ErrRuntimeNotFound) {
		return RuntimeSnapshot{
			SessionID:    command.SessionID,
			RuntimeState: RuntimeStopped,
			UpdatedAt:    stopTime(command, s.deps.Now()),
		}, nil
	}
	if err != nil {
		return RuntimeSnapshot{}, fmt.Errorf("read runtime: %w", err)
	}
	if current.RuntimeState == RuntimeStopped {
		return current, nil
	}

	stopping := current
	stopping.RuntimeState = RuntimeStopping
	stopping.UpdatedAt = s.deps.Now()
	if err := s.deps.Runtimes.Save(ctx, stopping); err != nil {
		return RuntimeSnapshot{}, fmt.Errorf("save stopping runtime: %w", err)
	}

	// Cleanup is deliberately attempted in order, but the second resource is still closed if the first fails.
	pipelineErr := s.deps.Pipelines.Stop(ctx, command.SessionID)
	connectionErr := s.deps.Connections.Close(ctx, command.SessionID)
	if pipelineErr != nil || connectionErr != nil {
		failed := failureSnapshot(command.SessionID, ErrorCodeStopFailed, s.deps.Now())
		cleanupErr := errors.Join(
			wrapCleanupError("stop pipeline", pipelineErr),
			wrapCleanupError("close WebRTC connection", connectionErr),
		)
		if saveErr := s.deps.Runtimes.Save(ctx, failed); saveErr != nil {
			return failed, errors.Join(cleanupErr, fmt.Errorf("save failed runtime: %w", saveErr))
		}
		return failed, cleanupErr
	}

	stopped := current
	stopped.RuntimeState = RuntimeStopped
	stopped.CurrentTurnID = nil
	stopped.CurrentPlaybackID = nil
	stopped.LastErrorCode = nil
	stopped.UpdatedAt = stopTime(command, s.deps.Now())
	if err := s.deps.Runtimes.Save(ctx, stopped); err != nil {
		return stopped, fmt.Errorf("save stopped runtime: %w", err)
	}
	return stopped, nil
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

func stopTime(command StopRealtimeCommand, fallback time.Time) time.Time {
	if command.EndedAt.IsZero() {
		return fallback
	}
	return command.EndedAt
}

func wrapCleanupError(operation string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", operation, err)
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
