package session

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

const defaultCleanupTimeout = 5 * time.Second

// Dependencies contains the required ports for lifecycle orchestration.
type Dependencies struct {
	Sessions       SessionReader
	Runtimes       RuntimeRepository
	Pipelines      PipelineManager
	Connections    WebRTCConnectionManager
	Now            func() time.Time
	CleanupTimeout time.Duration
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
	if deps.CleanupTimeout <= 0 {
		deps.CleanupTimeout = defaultCleanupTimeout
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
	if err == nil && current.RuntimeState == RuntimeFailed && current.LastErrorCode != nil && *current.LastErrorCode == ErrorCodeStopFailed {
		return current, ErrRuntimeCleanupRequired
	}
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
		if saveErr := s.saveRuntimeForCleanup(ctx, failed); saveErr != nil {
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
		// Start owns the pipeline but not the pre-existing WebRTC connection, so compensation stops only the pipeline.
		pipelineErr := s.stopPipelineForCleanup(ctx, command.SessionID)
		failed := failureSnapshot(command.SessionID, ErrorCodeStartFailed, s.deps.Now())
		saveErr := s.saveRuntimeForCleanup(ctx, failed)
		return failed, errors.Join(
			fmt.Errorf("save listening runtime: %w", err),
			wrapCleanupError("compensate pipeline", pipelineErr),
			wrapCleanupError("save failed runtime", saveErr),
		)
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
	pipelineErr := s.stopPipelineForCleanup(ctx, command.SessionID)
	connectionErr := s.closeConnectionForCleanup(ctx, command.SessionID)
	if pipelineErr != nil || connectionErr != nil {
		failed := failureSnapshot(command.SessionID, ErrorCodeStopFailed, s.deps.Now())
		cleanupErr := errors.Join(
			wrapCleanupError("stop pipeline", pipelineErr),
			wrapCleanupError("close WebRTC connection", connectionErr),
		)
		if saveErr := s.saveRuntimeForCleanup(ctx, failed); saveErr != nil {
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
	if err := s.saveRuntimeForCleanup(ctx, stopped); err != nil {
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

func (s *LifecycleService) stopPipelineForCleanup(parent context.Context, sessionID string) error {
	ctx, cancel := s.cleanupContext(parent)
	defer cancel()
	return s.deps.Pipelines.Stop(ctx, sessionID)
}

func (s *LifecycleService) closeConnectionForCleanup(parent context.Context, sessionID string) error {
	ctx, cancel := s.cleanupContext(parent)
	defer cancel()
	return s.deps.Connections.Close(ctx, sessionID)
}

func (s *LifecycleService) saveRuntimeForCleanup(parent context.Context, snapshot RuntimeSnapshot) error {
	ctx, cancel := s.cleanupContext(parent)
	defer cancel()
	return s.deps.Runtimes.Save(ctx, snapshot)
}

func (s *LifecycleService) cleanupContext(parent context.Context) (context.Context, context.CancelFunc) {
	// Compensation preserves request values while ignoring cancellation, then adds its own upper bound.
	return context.WithTimeout(context.WithoutCancel(parent), s.deps.CleanupTimeout)
}

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

		// Waiters increment references before blocking, so zero means the entry is safe to reclaim.
		l.mu.Lock()
		entry.references--
		if entry.references == 0 && l.locks[key] == entry {
			delete(l.locks, key)
		}
		l.mu.Unlock()
	}
}
