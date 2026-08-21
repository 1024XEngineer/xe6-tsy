package session

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
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
	if command.OperationID == "" {
		return RuntimeSnapshot{}, ErrStartOperationIDRequired
	}

	unlock := s.locks.lock(command.SessionID)
	defer unlock()

	current, err := s.deps.Runtimes.Get(ctx, command.SessionID)
	if err == nil && current.RuntimeState == RuntimeFailed && current.LastErrorCode != nil && *current.LastErrorCode == string(ErrorCodeStopFailed) {
		return current, ErrRuntimeCleanupRequired
	}
	if err == nil && current.RuntimeState != RuntimeStopped && current.RuntimeState != RuntimeFailed {
		if current.StartOperationID != command.OperationID {
			return current, ErrRuntimeOperationConflict
		}
		processingState := current.RuntimeState == RuntimeListening ||
			current.RuntimeState == RuntimeASRProcessing || current.RuntimeState == RuntimeTranslating || current.RuntimeState == RuntimeThinking || current.RuntimeState == RuntimeAssistantProcessing ||
			current.RuntimeState == RuntimeTTSProcessing || current.RuntimeState == RuntimePlaying
		if !processingState {
			return current, nil
		}
		if health, ok := s.deps.Pipelines.(PipelineHealthReader); !ok || health.PipelineActive(command.SessionID) {
			return current, nil
		}
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
	// Carry request tracing into the media graph without persisting it as
	// business session state.
	business.StartOperationID = command.OperationID
	business.TraceID = command.TraceID
	business.InitialMode = command.InitialMode
	if business.TraceID == "" {
		business.TraceID = command.OperationID
	}

	starting := RuntimeSnapshot{
		SessionID:        command.SessionID,
		StartOperationID: command.OperationID,
		RuntimeState:     RuntimeStarting,
		UpdatedAt:        s.deps.Now(),
	}
	if err := s.deps.Runtimes.Save(ctx, starting); err != nil {
		return RuntimeSnapshot{}, fmt.Errorf("save starting runtime: %w", err)
	}

	if err := s.deps.Pipelines.Start(ctx, business); err != nil {
		failed := failureSnapshot(command.SessionID, command.OperationID, ErrorCodeStartFailed, s.deps.Now())
		if saveErr := s.saveRuntimeForCleanup(ctx, failed); saveErr != nil {
			return failed, errors.Join(fmt.Errorf("start pipeline: %w", err), fmt.Errorf("save failed runtime: %w", saveErr))
		}
		return failed, fmt.Errorf("start pipeline: %w", err)
	}

	listening := RuntimeSnapshot{
		SessionID:        command.SessionID,
		StartOperationID: command.OperationID,
		RuntimeState:     RuntimeListening,
		UpdatedAt:        s.deps.Now(),
	}
	if err := s.deps.Runtimes.Save(ctx, listening); err != nil {
		// Start owns the pipeline but not the pre-existing WebRTC connection, so compensation stops only the pipeline.
		pipelineErr := s.stopPipelineForCleanup(ctx, command.SessionID)
		failed := failureSnapshot(command.SessionID, command.OperationID, ErrorCodeStartFailed, s.deps.Now())
		saveErr := s.saveRuntimeForCleanup(ctx, failed)
		return failed, errors.Join(
			fmt.Errorf("save listening runtime: %w", err),
			wrapCleanupError("compensate pipeline", pipelineErr),
			wrapCleanupError("save failed runtime", saveErr),
		)
	}
	if activator, ok := s.deps.Pipelines.(PipelineActivator); ok {
		if err := activator.Activate(ctx, command.SessionID, command.OperationID); err != nil {
			pipelineErr := s.stopPipelineForCleanup(ctx, command.SessionID)
			failed := failureSnapshot(command.SessionID, command.OperationID, ErrorCodeStartFailed, s.deps.Now())
			saveErr := s.saveRuntimeForCleanup(ctx, failed)
			return failed, errors.Join(
				fmt.Errorf("activate pipeline: %w", err),
				wrapCleanupError("compensate pipeline", pipelineErr),
				wrapCleanupError("save failed runtime", saveErr),
			)
		}
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
		failed := failureSnapshot(command.SessionID, current.StartOperationID, ErrorCodeStopFailed, s.deps.Now())
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

func failureSnapshot(sessionID string, operationID string, errorCode realtimev1.RuntimeErrorCode, now time.Time) RuntimeSnapshot {
	code := string(errorCode)
	return RuntimeSnapshot{
		SessionID:        sessionID,
		StartOperationID: operationID,
		RuntimeState:     RuntimeFailed,
		LastErrorCode:    &code,
		UpdatedAt:        now,
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
