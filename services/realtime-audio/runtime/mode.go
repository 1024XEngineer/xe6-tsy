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
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/pipeline"
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

// modeOperationRetentionLimit 限制成功切换结果的保留数量。
// coordinator 只保留最近的一小段记录，让短时间重试仍然幂等，
// 同时避免长连接 Runtime 的内存占用随着操作数量无限增长。
const modeOperationRetentionLimit = 256

// RuntimeInstanceIDFactory 负责生成进程内的 Runtime 身份。
// 只要 Start 需要替换一个已经停止或进入终态的 entry，就必须重新生成。
// 这样即使持久化的 Start 操作被重试，也不会把旧 Runtime 误认为新实例。
type RuntimeInstanceIDFactory func() (string, error)

type modeOperationRecord struct {
	command realtimev1.SwitchModeCommand
	result  realtimev1.SwitchModeResult
}

// modeCoordinator 只负责一个 runtime entry 的业务模式状态。
// 它把所有模式切换命令串行化，保证 HTTP、未来的 DataChannel，或者
// 其他入口都走同一条状态迁移路径，避免并发命令把状态拆成两套。
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

// Switch 负责单个 runtime 的模式切换。
// 处理顺序是：先检查是否存在可重放的已执行操作，再校验 runtime 实例和
// generation，最后才真正提交状态变更。这样同一个 operation_id 的重试
// 可以稳定返回第一次结果，而不同载荷会被识别为冲突。
//
// 这里的锁只保护一个 entry 的模式状态，不影响外层媒体管线。取消请求只会在
// 进入锁前和拿到锁后各检查一次；一旦状态变更开始，就视为原子提交，不能再
// 依赖 context 进行回滚。
//
// 重放记录只保留最近的一小段窗口。被淘汰的旧命令不再拥有精确重放能力，
// 会像新请求一样重新走当前 generation 的 CAS 校验。
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
		// 当前阶段的切换在锁内一次性完成。保留 switching 中间状态，
		// 是为了给后续阶段预留边界：届时可以在同一把 coordinator 锁内
		// 暂停新 Turn，并取消尚未提交的异步工作。
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

// rememberOperation 负责把本次成功切换记录进重放缓存。
// 它只保存固定数量的最近操作，避免 runtime 生命周期内的 operation 历史
// 无界增长。
// 调用方必须已经持有 coordinator 的锁。
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

// GetModeState 返回当前 runtime 的权威模式状态。
// 这个状态只描述 assistant / interpretation 之类的业务模式，不和媒体
// RuntimeSnapshot 的 listening、playing、stopping 状态混在一起。
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

// SwitchMode 把模式切换命令应用到现有 runtime entry。
// 它只改业务模式，不会启动、停止，也不会重建 WebRTC 连接或媒体管线。
// 这样模式切换就只是同一条实时会话里的状态迁移，而不是一次新的会话生命周期。
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
