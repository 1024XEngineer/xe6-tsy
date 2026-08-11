package runtime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/session"
)

var (
	ErrModeCommandInvalid          = errors.New("realtime mode command is invalid")
	ErrModeNotAvailable            = errors.New("realtime mode is not available")
	ErrModeGenerationConflict      = errors.New("realtime mode generation conflict")
	ErrModeRuntimeInstanceMismatch = errors.New("realtime mode runtime instance mismatch")
	ErrModeOperationConflict       = errors.New("realtime mode operation conflict")
	ErrRuntimeInstanceIDRequired   = errors.New("realtime runtime instance id is required")
)

const modeOperationRetentionLimit = 256

// RuntimeInstanceIDFactory creates a process-local runtime identity. A new ID
// is required whenever Start replaces a stopped or terminal entry, even when
// the durable Start operation is retried.
type RuntimeInstanceIDFactory func() (string, error)

type modeOperationRecord struct {
	command realtimev1.SwitchModeCommand
	result  realtimev1.SwitchModeResult
}

// modeCoordinator owns the business mode for exactly one runtime entry. Its
// lock serializes every command source so HTTP and future DataChannel commands
// cannot create separate state-transition paths.
type modeCoordinator struct {
	mu              sync.Mutex
	state           realtimev1.ModeStateSnapshot
	available       map[realtimev1.Mode]struct{}
	operations      map[string]modeOperationRecord
	operationOrder  []string
	operationCursor int
	now             func() time.Time
}

func newModeCoordinator(
	sessionID string,
	runtimeInstanceID string,
	initialMode realtimev1.Mode,
	available []realtimev1.Mode,
	now func() time.Time,
) (*modeCoordinator, error) {
	if sessionID == "" || runtimeInstanceID == "" || now == nil {
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
		now:            now,
	}, nil
}

func (c *modeCoordinator) Snapshot() realtimev1.ModeStateSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return cloneModeState(c.state)
}

// Switch serializes validation and state mutation for one runtime. A retained
// operation is checked before runtime-instance and generation CAS so an exact
// retry returns its first result after later transitions, while a changed
// payload conflicts. Cancellation is observed before and after lock acquisition;
// once mutation starts it is atomic and cannot be rolled back by cancellation.
// Replay records are bounded to the most recent operations. An evicted command
// is treated as a new request and must still pass the current generation CAS.
func (c *modeCoordinator) Switch(
	ctx context.Context,
	command realtimev1.SwitchModeCommand,
) (realtimev1.SwitchModeResult, error) {
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
	if command.RuntimeInstanceID != c.state.RuntimeInstanceID {
		return realtimev1.SwitchModeResult{}, ErrModeRuntimeInstanceMismatch
	}
	if command.ExpectedGeneration != c.state.Generation {
		return realtimev1.SwitchModeResult{}, ErrModeGenerationConflict
	}
	if _, ok := c.available[command.TargetMode]; !ok {
		return realtimev1.SwitchModeResult{}, ErrModeNotAvailable
	}

	status := realtimev1.ModeSwitchUnchanged
	if command.TargetMode != c.state.ActiveMode {
		// The transition is atomic today. Keeping switching as an explicit
		// internal phase reserves the boundary where later stages pause new
		// Turns and cancel uncommitted work under this same coordinator lock.
		c.state.Phase = realtimev1.ModePhaseSwitching
		c.state.ActiveMode = command.TargetMode
		c.state.Generation++
		status = realtimev1.ModeSwitchApplied
	}
	operationID := command.OperationID
	c.state.LastOperationID = &operationID
	c.state.UpdatedAt = c.now().UTC()
	c.state.Phase = realtimev1.ModePhaseActive
	result := realtimev1.SwitchModeResult{
		OperationID: command.OperationID,
		Status:      status,
		State:       cloneModeState(c.state),
	}
	c.rememberOperation(command, result)
	return cloneModeResult(result), nil
}

// rememberOperation keeps exact replay results within a fixed memory bound.
// The coordinator lock must be held by the caller.
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

// GetModeState returns the authoritative mode state without mixing it into the
// media RuntimeSnapshot state machine.
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

// SwitchMode applies a command to the existing runtime entry. It never starts,
// stops, or replaces the WebRTC connection or media pipeline.
func (m *Manager) SwitchMode(
	ctx context.Context,
	command realtimev1.SwitchModeCommand,
) (realtimev1.SwitchModeResult, error) {
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
	defer unlock()
	if err := ctx.Err(); err != nil {
		return realtimev1.SwitchModeResult{}, err
	}
	coordinator, err := m.currentModeCoordinator(command.SessionID)
	if err != nil {
		return realtimev1.SwitchModeResult{}, err
	}
	return coordinator.Switch(ctx, command)
}

func (m *Manager) currentModeCoordinator(sessionID string) (*modeCoordinator, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	item := m.entries[sessionID]
	if item == nil || item.mode == nil || item.stopping || item.terminal || item.finished {
		return nil, session.ErrRuntimeNotFound
	}
	return item.mode, nil
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
