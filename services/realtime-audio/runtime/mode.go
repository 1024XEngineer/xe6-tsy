package runtime

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/pipeline"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/session"
)

var (
	ErrModeCommandInvalid          = errors.New("realtime mode command is invalid")
	ErrModeNotAvailable            = errors.New("realtime mode is not available")
	ErrModeGenerationConflict      = errors.New("realtime mode generation conflict")
	ErrModeRuntimeInstanceMismatch = errors.New("realtime mode runtime instance mismatch")
	ErrModeOperationConflict       = errors.New("realtime mode operation conflict")
	ErrModeEventUnavailable        = errors.New("realtime mode event persistence is unavailable")
	ErrRuntimeInstanceIDRequired   = errors.New("realtime runtime instance id is required")
)

// modeOperationRetentionLimit bounds replay metadata for long-running connections while retaining
// enough recent results for ordinary request retries.
const modeOperationRetentionLimit = 256

// RuntimeInstanceIDFactory creates process-local runtime identities. Replacing a stopped or
// terminal entry must create a new identity so persisted Start retries cannot target old state.
type RuntimeInstanceIDFactory func() (string, error)

type modeOperationRecord struct {
	command realtimev1.SwitchModeCommand
	result  realtimev1.SwitchModeResult
}

// ModeChangedSink accepts the immutable fact before a mode transition becomes visible.
type ModeChangedSink interface {
	Publish(context.Context, realtimev1.ModeChangedEvent) error
}

// ModeCommandObserver receives only commands invoked on a concrete runtime
// coordinator. Implementations must not retain command identifiers.
type ModeCommandObserver interface {
	RecordModeCommand(realtimev1.SwitchModeResult, error)
}

type pendingModeTransition struct {
	command realtimev1.SwitchModeCommand
	result  realtimev1.SwitchModeResult
	event   realtimev1.ModeChangedEvent
}

// modeCoordinator owns the business mode for one runtime entry. It serializes HTTP and future
// DataChannel commands through one transition path.
type modeCoordinator struct {
	mu              sync.Mutex
	state           realtimev1.ModeStateSnapshot
	available       map[realtimev1.Mode]struct{}
	operations      map[string]modeOperationRecord
	operationOrder  []string
	operationCursor int
	pending         *pendingModeTransition
	modeChanges     ModeChangedSink
	now             func() time.Time
	observer        ModeCommandObserver
}

func newModeCoordinator(
	sessionID string,
	runtimeInstanceID string,
	initialMode realtimev1.Mode,
	available []realtimev1.Mode,
	modeChanges ModeChangedSink,
	now func() time.Time,
) (*modeCoordinator, error) {
	if sessionID == "" || runtimeInstanceID == "" || modeChanges == nil || now == nil {
		return nil, ErrModeCommandInvalid
	}
	registered := make(map[realtimev1.Mode]struct{}, len(available))
	for _, mode := range available {
		if !mode.Valid() {
			return nil, ErrModeCommandInvalid
		}
		registered[mode] = struct{}{}
	}
	if _, ok := registered[initialMode]; !initialMode.Valid() || !ok {
		return nil, ErrModeNotAvailable
	}
	return &modeCoordinator{
		state: realtimev1.ModeStateSnapshot{
			SessionID:         sessionID,
			RuntimeInstanceID: runtimeInstanceID,
			ActiveMode:        initialMode,
			Generation:        1,
			Phase:             realtimev1.ModePhaseActive,
			UpdatedAt:         now().UTC(),
		},
		available:      registered,
		operations:     make(map[string]modeOperationRecord, modeOperationRetentionLimit),
		operationOrder: make([]string, 0, modeOperationRetentionLimit),
		modeChanges:    modeChanges,
		now:            now,
	}, nil
}

func (c *modeCoordinator) Snapshot() realtimev1.ModeStateSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return cloneModeState(c.state)
}

// CommitFinalTurn linearizes generation validation with immutable FinalTurn
// publication. Translation and playback must stay outside this critical section.
func (c *modeCoordinator) CommitFinalTurn(
	ctx context.Context,
	turn pipeline.TurnContext,
	commit pipeline.FinalTurnCommit,
) (bool, error) {
	return c.commitTurn(ctx, turn, commit)
}

// CommitAssistantReply applies the same generation fence to assistant facts.
func (c *modeCoordinator) CommitAssistantReply(
	ctx context.Context,
	turn pipeline.TurnContext,
	commit pipeline.AssistantReplyCommit,
) (bool, error) {
	return c.commitTurn(ctx, turn, commit)
}

func (c *modeCoordinator) commitTurn(
	ctx context.Context,
	turn pipeline.TurnContext,
	commit func(context.Context) error,
) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if c == nil || commit == nil || turn.SessionID == "" || turn.Mode.SessionID != turn.SessionID ||
		turn.Mode.RuntimeInstanceID == "" || !turn.Mode.Mode.Valid() || turn.Mode.Generation < 1 {
		return false, ErrModeCommandInvalid
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if turn.SessionID != c.state.SessionID {
		return false, ErrModeCommandInvalid
	}
	if turn.Mode.RuntimeInstanceID != c.state.RuntimeInstanceID ||
		turn.Mode.Mode != c.state.ActiveMode || turn.Mode.Generation != c.state.Generation {
		return false, nil
	}
	if err := commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

// Switch serializes one runtime's mode commands and commits a changed state only after its
// immutable event receives durable acceptance. A failed or uncertain append leaves the frozen
// transition pending so only the same operation can retry the exact payload.
func (c *modeCoordinator) Switch(
	ctx context.Context,
	command realtimev1.SwitchModeCommand,
) (result realtimev1.SwitchModeResult, returnErr error) {
	if c != nil && c.observer != nil {
		defer func() { c.observer.RecordModeCommand(result, returnErr) }()
	}
	if err := ctx.Err(); err != nil {
		return realtimev1.SwitchModeResult{}, err
	}
	if c == nil || command.SessionID == "" || command.RuntimeInstanceID == "" ||
		command.OperationID == "" || command.TraceID == "" || command.ExpectedGeneration < 1 ||
		!command.TargetMode.Valid() {
		return realtimev1.SwitchModeResult{}, ErrModeCommandInvalid
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return realtimev1.SwitchModeResult{}, err
	}
	if command.SessionID != c.state.SessionID {
		return realtimev1.SwitchModeResult{}, ErrModeCommandInvalid
	}
	if previous, ok := c.operations[command.OperationID]; ok {
		if previous.command != command {
			return realtimev1.SwitchModeResult{}, ErrModeOperationConflict
		}
		return cloneModeResult(previous.result), nil
	}
	if c.pending != nil {
		if c.pending.command.OperationID == command.OperationID {
			if c.pending.command != command {
				return realtimev1.SwitchModeResult{}, ErrModeOperationConflict
			}
			return c.commitPending(ctx)
		}
		// A later command may recover a frozen transition after the outbox becomes available.
		// The recovered state is committed first; normal CAS validation below then rejects a
		// command built from the old generation instead of leaving the runtime permanently stuck.
		if _, err := c.commitPending(ctx); err != nil {
			return realtimev1.SwitchModeResult{}, err
		}
	}
	if command.RuntimeInstanceID != c.state.RuntimeInstanceID {
		return realtimev1.SwitchModeResult{}, ErrModeRuntimeInstanceMismatch
	}
	if command.ExpectedGeneration != c.state.Generation {
		return realtimev1.SwitchModeResult{}, ErrModeGenerationConflict
	}
	if _, ok := c.available[command.TargetMode]; !ok {
		return realtimev1.SwitchModeResult{}, ErrModeNotAvailable
	}

	if command.TargetMode != c.state.ActiveMode {
		c.pending = c.prepareTransition(command)
		return c.commitPending(ctx)
	}

	operationID := command.OperationID
	c.state.LastOperationID = &operationID
	c.state.UpdatedAt = c.now().UTC()
	c.state.Phase = realtimev1.ModePhaseActive
	result = realtimev1.SwitchModeResult{
		OperationID: command.OperationID,
		Status:      realtimev1.ModeSwitchUnchanged,
		State:       cloneModeState(c.state),
	}
	c.rememberOperation(command, result)
	return cloneModeResult(result), nil
}

func (c *modeCoordinator) prepareTransition(command realtimev1.SwitchModeCommand) *pendingModeTransition {
	fromMode := c.state.ActiveMode
	occurredAt := c.now().UTC()
	operationID := command.OperationID
	next := cloneModeState(c.state)
	next.ActiveMode = command.TargetMode
	next.Generation++
	next.Phase = realtimev1.ModePhaseActive
	next.LastOperationID = &operationID
	next.UpdatedAt = occurredAt
	return &pendingModeTransition{
		command: command,
		result: realtimev1.SwitchModeResult{
			OperationID: command.OperationID,
			Status:      realtimev1.ModeSwitchApplied,
			State:       next,
		},
		event: realtimev1.ModeChangedEvent{
			EventVersion:        realtimev1.ModeChangedEventVersion,
			EventID:             modeChangedEventID(command),
			TraceID:             command.TraceID,
			SessionID:           command.SessionID,
			RuntimeInstanceID:   command.RuntimeInstanceID,
			OperationID:         command.OperationID,
			FromMode:            fromMode,
			ToMode:              command.TargetMode,
			ResultingGeneration: next.Generation,
			OccurredAt:          occurredAt,
		},
	}
}

// commitPending keeps the coordinator lock held across durable acceptance. This intentionally
// blocks other commands for the same runtime so state and event order cannot diverge.
func (c *modeCoordinator) commitPending(ctx context.Context) (realtimev1.SwitchModeResult, error) {
	pending := c.pending
	if pending == nil {
		return realtimev1.SwitchModeResult{}, ErrModeCommandInvalid
	}
	if err := c.modeChanges.Publish(ctx, pending.event); err != nil {
		return realtimev1.SwitchModeResult{}, fmt.Errorf("%w: %w", ErrModeEventUnavailable, err)
	}
	c.state = cloneModeState(pending.result.State)
	c.rememberOperation(pending.command, pending.result)
	c.pending = nil
	return cloneModeResult(pending.result), nil
}

// modeChangedEventID scopes operation idempotency to one session runtime and is stable across
// uncertain delivery retries.
func modeChangedEventID(command realtimev1.SwitchModeCommand) string {
	payload := command.SessionID + "\x00" + command.RuntimeInstanceID + "\x00" + command.OperationID
	digest := sha256.Sum256([]byte(payload))
	return "mode_changed_" + hex.EncodeToString(digest[:])
}

// rememberOperation stores one successful result in a bounded replay window. The caller must hold
// the coordinator lock.
func (c *modeCoordinator) rememberOperation(
	command realtimev1.SwitchModeCommand,
	result realtimev1.SwitchModeResult,
) {
	if len(c.operationOrder) < modeOperationRetentionLimit {
		c.operationOrder = append(c.operationOrder, command.OperationID)
	} else {
		evicted := c.operationOrder[c.operationCursor]
		delete(c.operations, evicted)
		c.operationOrder[c.operationCursor] = command.OperationID
		c.operationCursor = (c.operationCursor + 1) % modeOperationRetentionLimit
	}
	c.operations[command.OperationID] = modeOperationRecord{command: command, result: result}
}

// GetModeState returns the runtime-owned business mode without duplicating media lifecycle state.
func (m *Manager) GetModeState(ctx context.Context, sessionID string) (realtimev1.ModeStateSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return realtimev1.ModeStateSnapshot{}, err
	}
	if m == nil {
		return realtimev1.ModeStateSnapshot{}, ErrDependencyRequired
	}
	if sessionID == "" {
		return realtimev1.ModeStateSnapshot{}, ErrSessionIDRequired
	}

	unlock := m.locks.lock(sessionID)
	defer unlock()
	if err := ctx.Err(); err != nil {
		return realtimev1.ModeStateSnapshot{}, err
	}
	coordinator, err := m.currentModeCoordinator(sessionID)
	if err != nil {
		return realtimev1.ModeStateSnapshot{}, err
	}
	return coordinator.Snapshot(), nil
}

// SwitchMode changes business handling on an existing runtime without stopping, starting, or
// rebuilding its WebRTC connection and media pipeline.
func (m *Manager) SwitchMode(
	ctx context.Context,
	command realtimev1.SwitchModeCommand,
) (result realtimev1.SwitchModeResult, returnErr error) {
	defer func() {
		m.logModeSwitch(command, result, returnErr)
	}()
	if err := ctx.Err(); err != nil {
		return realtimev1.SwitchModeResult{}, err
	}
	if m == nil {
		return realtimev1.SwitchModeResult{}, ErrDependencyRequired
	}
	if command.SessionID == "" {
		return realtimev1.SwitchModeResult{}, ErrSessionIDRequired
	}

	unlock := m.locks.lock(command.SessionID)
	if err := ctx.Err(); err != nil {
		unlock()
		return realtimev1.SwitchModeResult{}, err
	}
	coordinator, runCtx, err := m.currentModeRuntime(command.SessionID)
	if err != nil {
		unlock()
		return realtimev1.SwitchModeResult{}, err
	}
	if runCtx == nil {
		unlock()
		return coordinator.Switch(ctx, command)
	}
	switchCtx, cancel := context.WithCancel(ctx)
	stopCancellation := context.AfterFunc(runCtx, cancel)
	unlock()
	defer func() {
		stopCancellation()
		cancel()
	}()
	return coordinator.Switch(switchCtx, command)
}

// logModeSwitch records control-plane correlation after the coordinator has
// decided the command. It never invents a Turn ID or provider because mode
// commands are independent of media processing and provider execution.
func (m *Manager) logModeSwitch(command realtimev1.SwitchModeCommand, result realtimev1.SwitchModeResult, err error) {
	if m == nil || m.logger == nil {
		return
	}
	fields := []any{"event", "mode_switch"}
	if command.SessionID != "" {
		fields = append(fields, "session_id", command.SessionID)
	}
	if command.OperationID != "" {
		fields = append(fields, "operation_id", command.OperationID)
	}
	if command.TraceID != "" {
		fields = append(fields, "trace_id", command.TraceID)
	}
	if command.RuntimeInstanceID != "" {
		fields = append(fields, "runtime_instance_id", command.RuntimeInstanceID)
	}
	if command.ExpectedGeneration > 0 {
		fields = append(fields, "expected_generation", command.ExpectedGeneration)
	}
	if command.TargetMode.Valid() {
		fields = append(fields, "target_mode", command.TargetMode)
	}
	if result.State.SessionID != "" && result.State.ActiveMode.Valid() {
		fields = append(fields, "mode", result.State.ActiveMode)
	}
	if result.State.Generation > 0 {
		fields = append(fields, "generation", result.State.Generation)
	}
	if result.Status != "" {
		fields = append(fields, "status", result.Status)
	}
	if err != nil {
		fields = append(fields, "status", "failed", "error_class", modeSwitchErrorClass(err), "error", err)
		m.logger.Warn("realtime mode switch rejected", fields...)
		return
	}
	m.logger.Info("realtime mode switch resolved", fields...)
}

func modeSwitchErrorClass(err error) string {
	switch {
	case errors.Is(err, ErrModeCommandInvalid):
		return "invalid_command"
	case errors.Is(err, ErrModeNotAvailable):
		return "not_available"
	case errors.Is(err, ErrModeGenerationConflict):
		return "generation_conflict"
	case errors.Is(err, ErrModeRuntimeInstanceMismatch):
		return "runtime_instance_mismatch"
	case errors.Is(err, ErrModeOperationConflict):
		return "operation_conflict"
	case errors.Is(err, ErrModeEventUnavailable):
		return "event_unavailable"
	case errors.Is(err, session.ErrRuntimeNotFound):
		return "runtime_not_found"
	case errors.Is(err, ErrSessionIDRequired), errors.Is(err, ErrDependencyRequired):
		return "invalid_request"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	default:
		return "unknown"
	}
}

func (m *Manager) currentModeCoordinator(sessionID string) (*modeCoordinator, error) {
	coordinator, _, err := m.currentModeRuntime(sessionID)
	return coordinator, err
}

func (m *Manager) currentModeRuntime(sessionID string) (*modeCoordinator, context.Context, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	item := m.entries[sessionID]
	if item == nil || item.mode == nil || item.stopping || item.terminal || item.finished {
		return nil, nil, session.ErrRuntimeNotFound
	}
	return item.mode, item.ctx, nil
}

// managerTurnModeReader adapts Manager's runtime-owned coordinator to the
// narrow pipeline snapshot port without exposing coordinator mutation methods.
type managerTurnModeReader struct {
	manager *Manager
}

func (r managerTurnModeReader) GetTurnMode(ctx context.Context, sessionID string) (pipeline.TurnModeSnapshot, error) {
	state, err := r.manager.GetModeState(ctx, sessionID)
	if err != nil {
		return pipeline.TurnModeSnapshot{}, err
	}
	return pipeline.TurnModeSnapshot{
		SessionID:         state.SessionID,
		RuntimeInstanceID: state.RuntimeInstanceID,
		Mode:              state.ActiveMode,
		Generation:        state.Generation,
	}, nil
}

// managerTurnCommitGate resolves the active coordinator under the Manager
// lifecycle lock, then releases that lock before entering the coordinator.
// Event sinks are external and may block while honoring cancellation; the
// lifecycle lock must remain available so Stop can cancel the run context.
// The coordinator still serializes generation validation with publication.
type managerTurnCommitGate struct {
	manager *Manager
}

func (g managerTurnCommitGate) CommitFinalTurn(
	ctx context.Context,
	turn pipeline.TurnContext,
	commit pipeline.FinalTurnCommit,
) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if g.manager == nil || turn.SessionID == "" {
		return false, ErrDependencyRequired
	}
	unlock := g.manager.locks.lock(turn.SessionID)
	coordinator, err := g.manager.currentModeCoordinator(turn.SessionID)
	unlock()
	if err != nil {
		return false, err
	}
	return coordinator.CommitFinalTurn(ctx, turn, commit)
}

func (g managerTurnCommitGate) CommitAssistantReply(
	ctx context.Context,
	turn pipeline.TurnContext,
	commit pipeline.AssistantReplyCommit,
) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if g.manager == nil || turn.SessionID == "" {
		return false, ErrDependencyRequired
	}
	unlock := g.manager.locks.lock(turn.SessionID)
	coordinator, err := g.manager.currentModeCoordinator(turn.SessionID)
	unlock()
	if err != nil {
		return false, err
	}
	return coordinator.CommitAssistantReply(ctx, turn, commit)
}

func defaultRuntimeInstanceID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate runtime instance id: %w", err)
	}
	return "rt_" + hex.EncodeToString(value[:]), nil
}

func cloneModeState(state realtimev1.ModeStateSnapshot) realtimev1.ModeStateSnapshot {
	clone := state
	if state.LastOperationID != nil {
		operationID := *state.LastOperationID
		clone.LastOperationID = &operationID
	}
	return clone
}

func cloneModeResult(result realtimev1.SwitchModeResult) realtimev1.SwitchModeResult {
	clone := result
	clone.State = cloneModeState(result.State)
	return clone
}
