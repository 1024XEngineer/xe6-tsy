package runtime

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/asr"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/config"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/session"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/translate"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/tts"
)

func TestModeCoordinatorStartsWithIndependentModeState(t *testing.T) {
	coordinator := mustModeCoordinator(t, realtimev1.ModeInterpretation, []realtimev1.Mode{realtimev1.ModeInterpretation})

	state := coordinator.Snapshot()
	if state.SessionID != "session-1" || state.RuntimeInstanceID != "runtime-1" ||
		state.ActiveMode != realtimev1.ModeInterpretation || state.Generation != 1 ||
		state.Phase != realtimev1.ModePhaseActive || state.LastOperationID != nil || state.UpdatedAt.IsZero() {
		t.Fatalf("initial mode state = %#v", state)
	}
}

func TestModeCoordinatorRejectsInvalidAndStaleCommands(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*realtimev1.SwitchModeCommand)
		wantErr error
	}{
		{
			name: "missing operation",
			mutate: func(command *realtimev1.SwitchModeCommand) {
				command.OperationID = ""
			},
			wantErr: ErrModeCommandInvalid,
		},
		{
			name: "different session",
			mutate: func(command *realtimev1.SwitchModeCommand) {
				command.SessionID = "session-2"
			},
			wantErr: ErrModeCommandInvalid,
		},
		{
			name: "different runtime instance",
			mutate: func(command *realtimev1.SwitchModeCommand) {
				command.RuntimeInstanceID = "runtime-old"
			},
			wantErr: ErrModeRuntimeInstanceMismatch,
		},
		{
			name: "stale generation",
			mutate: func(command *realtimev1.SwitchModeCommand) {
				command.ExpectedGeneration = 2
			},
			wantErr: ErrModeGenerationConflict,
		},
		{
			name: "unregistered mode",
			mutate: func(command *realtimev1.SwitchModeCommand) {
				command.TargetMode = realtimev1.ModeAssistant
			},
			wantErr: ErrModeNotAvailable,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			coordinator := mustModeCoordinator(t, realtimev1.ModeInterpretation, []realtimev1.Mode{realtimev1.ModeInterpretation})
			command := modeCommand("operation-1", 1, realtimev1.ModeInterpretation)
			test.mutate(&command)

			if _, err := coordinator.Switch(t.Context(), command); !errors.Is(err, test.wantErr) {
				t.Fatalf("Switch() error = %v, want %v", err, test.wantErr)
			}
			state := coordinator.Snapshot()
			if state.Generation != 1 || state.ActiveMode != realtimev1.ModeInterpretation || state.LastOperationID != nil {
				t.Fatalf("failed command changed state = %#v", state)
			}
		})
	}
}

func TestModeCoordinatorAppliesAndReplaysFirstResult(t *testing.T) {
	coordinator := mustModeCoordinator(t, realtimev1.ModeInterpretation, []realtimev1.Mode{
		realtimev1.ModeInterpretation,
		realtimev1.ModeAssistant,
	})
	firstCommand := modeCommand("operation-1", 1, realtimev1.ModeAssistant)

	first, err := coordinator.Switch(t.Context(), firstCommand)
	if err != nil {
		t.Fatalf("first Switch() error = %v", err)
	}
	if first.Status != realtimev1.ModeSwitchApplied || first.State.ActiveMode != realtimev1.ModeAssistant ||
		first.State.Generation != 2 || first.State.Phase != realtimev1.ModePhaseActive ||
		first.State.LastOperationID == nil || *first.State.LastOperationID != firstCommand.OperationID {
		t.Fatalf("first Switch() = %#v", first)
	}

	second, err := coordinator.Switch(t.Context(), modeCommand("operation-2", 2, realtimev1.ModeInterpretation))
	if err != nil {
		t.Fatalf("second Switch() error = %v", err)
	}
	if second.State.Generation != 3 || second.State.ActiveMode != realtimev1.ModeInterpretation {
		t.Fatalf("second Switch() = %#v", second)
	}

	replayed, err := coordinator.Switch(t.Context(), firstCommand)
	if err != nil {
		t.Fatalf("replayed Switch() error = %v", err)
	}
	if replayed.Status != first.Status || replayed.State.Generation != first.State.Generation ||
		replayed.State.ActiveMode != first.State.ActiveMode {
		t.Fatalf("replayed result = %#v, want first result %#v", replayed, first)
	}
	current := coordinator.Snapshot()
	if current.Generation != 3 || current.ActiveMode != realtimev1.ModeInterpretation {
		t.Fatalf("replay changed current state = %#v", current)
	}
}

func TestModeCoordinatorDoesNotExposeMutableState(t *testing.T) {
	coordinator := mustModeCoordinator(t, realtimev1.ModeInterpretation, []realtimev1.Mode{
		realtimev1.ModeInterpretation,
		realtimev1.ModeAssistant,
	})
	command := modeCommand("operation-1", 1, realtimev1.ModeAssistant)

	result, err := coordinator.Switch(t.Context(), command)
	if err != nil {
		t.Fatalf("Switch() error = %v", err)
	}
	result.State.ActiveMode = realtimev1.ModeInterpretation
	*result.State.LastOperationID = "mutated-result"
	state := coordinator.Snapshot()
	state.ActiveMode = realtimev1.ModeInterpretation
	*state.LastOperationID = "mutated-snapshot"

	replayed, err := coordinator.Switch(t.Context(), command)
	if err != nil {
		t.Fatalf("replayed Switch() error = %v", err)
	}
	current := coordinator.Snapshot()
	if replayed.State.ActiveMode != realtimev1.ModeAssistant || replayed.State.LastOperationID == nil ||
		*replayed.State.LastOperationID != command.OperationID || current.ActiveMode != realtimev1.ModeAssistant ||
		current.LastOperationID == nil || *current.LastOperationID != command.OperationID {
		t.Fatalf("mutable result leaked into coordinator: replayed=%#v current=%#v", replayed, current)
	}
}

func TestModeCoordinatorReturnsUnchangedWithoutAdvancingGeneration(t *testing.T) {
	coordinator := mustModeCoordinator(t, realtimev1.ModeInterpretation, []realtimev1.Mode{realtimev1.ModeInterpretation})
	command := modeCommand("operation-1", 1, realtimev1.ModeInterpretation)

	result, err := coordinator.Switch(t.Context(), command)
	if err != nil {
		t.Fatalf("Switch() error = %v", err)
	}
	if result.Status != realtimev1.ModeSwitchUnchanged || result.State.Generation != 1 ||
		result.State.LastOperationID == nil || *result.State.LastOperationID != command.OperationID {
		t.Fatalf("unchanged Switch() = %#v", result)
	}
}

func TestModeCoordinatorRejectsOperationPayloadConflict(t *testing.T) {
	coordinator := mustModeCoordinator(t, realtimev1.ModeInterpretation, []realtimev1.Mode{realtimev1.ModeInterpretation})
	command := modeCommand("operation-1", 1, realtimev1.ModeInterpretation)
	if _, err := coordinator.Switch(t.Context(), command); err != nil {
		t.Fatalf("first Switch() error = %v", err)
	}
	command.TraceID = "trace-different"

	if _, err := coordinator.Switch(t.Context(), command); !errors.Is(err, ErrModeOperationConflict) {
		t.Fatalf("conflicting Switch() error = %v, want ErrModeOperationConflict", err)
	}
}

func TestModeCoordinatorBoundsOperationReplayRetention(t *testing.T) {
	coordinator := mustModeCoordinator(t, realtimev1.ModeInterpretation, []realtimev1.Mode{
		realtimev1.ModeInterpretation,
		realtimev1.ModeAssistant,
	})
	firstCommand := modeCommand("operation-0", 1, realtimev1.ModeAssistant)
	latestCommand := firstCommand
	var latestResult realtimev1.SwitchModeResult
	for index := 0; index <= modeOperationRetentionLimit; index++ {
		target := realtimev1.ModeAssistant
		if index%2 == 1 {
			target = realtimev1.ModeInterpretation
		}
		latestCommand = modeCommand(fmt.Sprintf("operation-%d", index), int64(index+1), target)
		result, err := coordinator.Switch(t.Context(), latestCommand)
		if err != nil {
			t.Fatalf("Switch(%d) error = %v", index, err)
		}
		latestResult = result
	}

	if len(coordinator.operations) != modeOperationRetentionLimit ||
		len(coordinator.operationOrder) != modeOperationRetentionLimit {
		t.Fatalf("retained operations = map %d, order %d", len(coordinator.operations), len(coordinator.operationOrder))
	}
	if _, ok := coordinator.operations[firstCommand.OperationID]; ok {
		t.Fatalf("oldest operation %q was not evicted", firstCommand.OperationID)
	}
	if _, err := coordinator.Switch(t.Context(), firstCommand); !errors.Is(err, ErrModeGenerationConflict) {
		t.Fatalf("evicted operation replay error = %v, want ErrModeGenerationConflict", err)
	}
	replayed, err := coordinator.Switch(t.Context(), latestCommand)
	if err != nil || replayed.State.Generation != latestResult.State.Generation {
		t.Fatalf("retained operation replay = %#v, %v", replayed, err)
	}
}

func TestModeCoordinatorSerializesConcurrentGenerationCompareAndSwitch(t *testing.T) {
	coordinator := mustModeCoordinator(t, realtimev1.ModeInterpretation, []realtimev1.Mode{
		realtimev1.ModeInterpretation,
		realtimev1.ModeAssistant,
	})
	start := make(chan struct{})
	results := make(chan error, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	for _, operationID := range []string{"operation-1", "operation-2"} {
		go func(operationID string) {
			ready.Done()
			<-start
			_, err := coordinator.Switch(context.Background(), modeCommand(operationID, 1, realtimev1.ModeAssistant))
			results <- err
		}(operationID)
	}
	ready.Wait()
	close(start)

	applied := 0
	conflicted := 0
	for range 2 {
		err := <-results
		switch {
		case err == nil:
			applied++
		case errors.Is(err, ErrModeGenerationConflict):
			conflicted++
		default:
			t.Fatalf("concurrent Switch() error = %v", err)
		}
	}
	if applied != 1 || conflicted != 1 {
		t.Fatalf("concurrent results = applied %d, conflicted %d", applied, conflicted)
	}
	state := coordinator.Snapshot()
	if state.Generation != 2 || state.ActiveMode != realtimev1.ModeAssistant {
		t.Fatalf("concurrent final state = %#v", state)
	}
}

func TestManagerOwnsOneCoordinatorPerRuntimeEntry(t *testing.T) {
	manager, sourceOpens := newModeTestManager(t, []string{"runtime-1", "runtime-2"}, nil)
	snapshot := session.SessionSnapshot{
		SessionID: "session-1", AccountID: "account-1", StartOperationID: "start-1", TraceID: "trace-1",
	}
	if err := manager.Start(t.Context(), snapshot); err != nil {
		t.Fatalf("first Start() error = %v", err)
	}
	state, err := manager.GetModeState(t.Context(), snapshot.SessionID)
	if err != nil {
		t.Fatalf("GetModeState() error = %v", err)
	}
	if state.RuntimeInstanceID != "runtime-1" || state.ActiveMode != realtimev1.ModeInterpretation || state.Generation != 1 {
		t.Fatalf("first mode state = %#v", state)
	}

	assistant := modeCommand("mode-1", state.Generation, realtimev1.ModeAssistant)
	assistant.RuntimeInstanceID = state.RuntimeInstanceID
	if _, err := manager.SwitchMode(t.Context(), assistant); !errors.Is(err, ErrModeNotAvailable) {
		t.Fatalf("assistant SwitchMode() error = %v, want ErrModeNotAvailable", err)
	}
	unchanged := modeCommand("mode-2", state.Generation, realtimev1.ModeInterpretation)
	unchanged.RuntimeInstanceID = state.RuntimeInstanceID
	result, err := manager.SwitchMode(t.Context(), unchanged)
	if err != nil || result.Status != realtimev1.ModeSwitchUnchanged {
		t.Fatalf("interpretation SwitchMode() = %#v, %v", result, err)
	}
	if *sourceOpens != 1 {
		t.Fatalf("mode commands reopened media source %d times, want 1", *sourceOpens)
	}

	if err := manager.Stop(t.Context(), snapshot.SessionID); err != nil {
		t.Fatalf("first Stop() error = %v", err)
	}
	if _, err := manager.GetModeState(t.Context(), snapshot.SessionID); !errors.Is(err, session.ErrRuntimeNotFound) {
		t.Fatalf("stopped GetModeState() error = %v, want runtime not found", err)
	}

	snapshot.StartOperationID = "start-2"
	if err := manager.Start(t.Context(), snapshot); err != nil {
		t.Fatalf("second Start() error = %v", err)
	}
	restarted, err := manager.GetModeState(t.Context(), snapshot.SessionID)
	if err != nil {
		t.Fatalf("restarted GetModeState() error = %v", err)
	}
	if restarted.RuntimeInstanceID != "runtime-2" || restarted.Generation != 1 || restarted.LastOperationID != nil {
		t.Fatalf("restarted mode state = %#v", restarted)
	}
	if err := manager.Stop(t.Context(), snapshot.SessionID); err != nil {
		t.Fatalf("second Stop() error = %v", err)
	}
}

func TestManagerDoesNotOpenMediaWhenRuntimeIdentityCreationFails(t *testing.T) {
	idErr := errors.New("random source unavailable")
	manager, sourceOpens := newModeTestManager(t, nil, idErr)
	snapshot := session.SessionSnapshot{
		SessionID: "session-1", AccountID: "account-1", StartOperationID: "start-1", TraceID: "trace-1",
	}

	if err := manager.Start(t.Context(), snapshot); !errors.Is(err, idErr) {
		t.Fatalf("Start() error = %v, want runtime identity error", err)
	}
	if *sourceOpens != 0 {
		t.Fatalf("source open calls = %d, want 0", *sourceOpens)
	}
}

func mustModeCoordinator(
	t *testing.T,
	initial realtimev1.Mode,
	available []realtimev1.Mode,
) *modeCoordinator {
	t.Helper()
	coordinator, err := newModeCoordinator(
		"session-1",
		"runtime-1",
		initial,
		available,
		func() time.Time { return time.Unix(1700000000, 0).UTC() },
	)
	if err != nil {
		t.Fatalf("newModeCoordinator() error = %v", err)
	}
	return coordinator
}

func modeCommand(operationID string, generation int64, target realtimev1.Mode) realtimev1.SwitchModeCommand {
	return realtimev1.SwitchModeCommand{
		SessionID:          "session-1",
		RuntimeInstanceID:  "runtime-1",
		OperationID:        operationID,
		TraceID:            "trace-1",
		ExpectedGeneration: generation,
		TargetMode:         target,
	}
}

func newModeTestManager(t *testing.T, runtimeIDs []string, runtimeIDErr error) (*Manager, *int) {
	t.Helper()
	ids := append([]string(nil), runtimeIDs...)
	sourceOpens := 0
	deps := testDependencies(&fakeFrameSource{}, &fakeLanguageReader{snapshot: activeConfig("session-1")})
	deps.FrameSources = FrameSourceFactoryFunc(func(context.Context, session.SessionSnapshot) (AudioInput, error) {
		sourceOpens++
		return AudioInput{Source: &fakeFrameSource{}, SourceLanguage: "zh-CN"}, nil
	})
	deps.NewRuntimeInstanceID = func() (string, error) {
		if runtimeIDErr != nil {
			return "", runtimeIDErr
		}
		if len(ids) == 0 {
			return "", ErrRuntimeInstanceIDRequired
		}
		id := ids[0]
		ids = ids[1:]
		return id, nil
	}
	manager, err := NewManager(config.ProviderConfig{}, config.Providers{
		ASR:         asr.NewFakeProvider(asr.FakeProviderConfig{}),
		Translation: &translate.FakeProvider{},
		TTS:         tts.NewFakeProvider(tts.FakeProviderConfig{}),
	}, deps)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	return manager, &sourceOpens
}
