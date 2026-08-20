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
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/pipeline"
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

func TestNewModeCoordinatorRejectsInvalidConfiguration(t *testing.T) {
	modeChanges := &recordingModeChangedSink{}
	now := func() time.Time { return time.Unix(1700000000, 0).UTC() }
	tests := []struct {
		name      string
		sessionID string
		runtimeID string
		initial   realtimev1.Mode
		available []realtimev1.Mode
		sink      ModeChangedSink
		now       func() time.Time
		wantErr   error
	}{
		{
			name: "missing session", runtimeID: "runtime-1", initial: realtimev1.ModeInterpretation,
			available: []realtimev1.Mode{realtimev1.ModeInterpretation}, sink: modeChanges, now: now,
			wantErr: ErrModeCommandInvalid,
		},
		{
			name: "missing runtime instance", sessionID: "session-1", initial: realtimev1.ModeInterpretation,
			available: []realtimev1.Mode{realtimev1.ModeInterpretation}, sink: modeChanges, now: now,
			wantErr: ErrModeCommandInvalid,
		},
		{
			name: "missing event sink", sessionID: "session-1", runtimeID: "runtime-1", initial: realtimev1.ModeInterpretation,
			available: []realtimev1.Mode{realtimev1.ModeInterpretation}, now: now, wantErr: ErrModeCommandInvalid,
		},
		{
			name: "missing clock", sessionID: "session-1", runtimeID: "runtime-1", initial: realtimev1.ModeInterpretation,
			available: []realtimev1.Mode{realtimev1.ModeInterpretation}, sink: modeChanges, wantErr: ErrModeCommandInvalid,
		},
		{
			name: "invalid registered mode", sessionID: "session-1", runtimeID: "runtime-1", initial: realtimev1.ModeInterpretation,
			available: []realtimev1.Mode{"invalid"}, sink: modeChanges, now: now, wantErr: ErrModeCommandInvalid,
		},
		{
			name: "unavailable initial mode", sessionID: "session-1", runtimeID: "runtime-1", initial: realtimev1.ModeAssistant,
			available: []realtimev1.Mode{realtimev1.ModeInterpretation}, sink: modeChanges, now: now, wantErr: ErrModeNotAvailable,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			coordinator, err := newModeCoordinator(
				test.sessionID, test.runtimeID, test.initial, test.available, test.sink, test.now,
			)
			if coordinator != nil || !errors.Is(err, test.wantErr) {
				t.Fatalf("newModeCoordinator() = %p, %v; want nil, %v", coordinator, err, test.wantErr)
			}
		})
	}
}

func TestTurnModeSnapshotRemainsFixedAcrossModeSwitch(t *testing.T) {
	coordinator := mustModeCoordinator(t, realtimev1.ModeInterpretation, []realtimev1.Mode{
		realtimev1.ModeInterpretation,
		realtimev1.ModeAssistant,
	})
	manager := &Manager{
		locks: newKeyedLocker(),
		entries: map[string]*entry{
			"session-1": {mode: coordinator},
		},
	}
	opener := pipeline.NewTurnOpener(
		pipeline.NewMemoryTurnAllocator(),
		&fakeLanguageReader{snapshot: activeConfig("session-1")},
		managerTurnModeReader{manager: manager},
	)

	first, err := opener.OpenTurn(t.Context(), pipeline.TurnOpenRequest{SessionID: "session-1"})
	if err != nil {
		t.Fatalf("first OpenTurn() error = %v", err)
	}
	if _, err := coordinator.Switch(t.Context(), modeCommand("operation-1", 1, realtimev1.ModeAssistant)); err != nil {
		t.Fatalf("Switch() error = %v", err)
	}
	second, err := opener.OpenTurn(t.Context(), pipeline.TurnOpenRequest{SessionID: "session-1"})
	if err != nil {
		t.Fatalf("second OpenTurn() error = %v", err)
	}

	if first.Mode.Mode != realtimev1.ModeInterpretation || first.Mode.Generation != 1 {
		t.Fatalf("first Turn mode changed after switch = %#v", first.Mode)
	}
	if second.Mode.Mode != realtimev1.ModeAssistant || second.Mode.Generation != 2 {
		t.Fatalf("second Turn mode = %#v, want assistant generation 2", second.Mode)
	}
}

func TestModeCoordinatorDropsFinalTurnAfterModeSwitch(t *testing.T) {
	coordinator := mustModeCoordinator(t, realtimev1.ModeInterpretation, []realtimev1.Mode{
		realtimev1.ModeInterpretation,
		realtimev1.ModeAssistant,
	})
	turn := modeTurn(realtimev1.ModeInterpretation, 1)
	if _, err := coordinator.Switch(t.Context(), modeCommand("operation-1", 1, realtimev1.ModeAssistant)); err != nil {
		t.Fatalf("Switch() error = %v", err)
	}
	commitCalls := 0

	committed, err := coordinator.CommitFinalTurn(t.Context(), turn, func(context.Context) error {
		commitCalls++
		return nil
	})
	if err != nil {
		t.Fatalf("CommitFinalTurn() error = %v", err)
	}
	if committed || commitCalls != 0 {
		t.Fatalf("CommitFinalTurn() = %v with %d callback calls, want stale drop", committed, commitCalls)
	}
}

func TestModeCoordinatorRejectsEachSupersededTurnIdentity(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*pipeline.TurnContext)
	}{
		{name: "runtime instance", mutate: func(turn *pipeline.TurnContext) { turn.Mode.RuntimeInstanceID = "runtime-old" }},
		{name: "mode", mutate: func(turn *pipeline.TurnContext) { turn.Mode.Mode = realtimev1.ModeAssistant }},
		{name: "generation", mutate: func(turn *pipeline.TurnContext) { turn.Mode.Generation = 2 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			coordinator := mustModeCoordinator(t, realtimev1.ModeInterpretation, []realtimev1.Mode{
				realtimev1.ModeInterpretation,
				realtimev1.ModeAssistant,
			})
			turn := modeTurn(realtimev1.ModeInterpretation, 1)
			test.mutate(&turn)
			commitCalls := 0

			committed, err := coordinator.CommitFinalTurn(t.Context(), turn, func(context.Context) error {
				commitCalls++
				return nil
			})
			if err != nil {
				t.Fatalf("CommitFinalTurn() error = %v", err)
			}
			if committed || commitCalls != 0 {
				t.Fatalf("CommitFinalTurn() = %v with %d callback calls, want stale drop", committed, commitCalls)
			}
		})
	}
}

func TestModeCoordinatorSerializesFinalTurnCommitAndSwitch(t *testing.T) {
	const runs = 128
	for run := 0; run < runs; run++ {
		coordinator := mustModeCoordinator(t, realtimev1.ModeInterpretation, []realtimev1.Mode{
			realtimev1.ModeInterpretation,
			realtimev1.ModeAssistant,
		})
		start := make(chan struct{})
		var waitGroup sync.WaitGroup
		var committed bool
		var commitErr error
		var switchErr error
		commitCalls := 0
		callbackGeneration := int64(0)
		callbackMode := realtimev1.Mode("")

		waitGroup.Add(2)
		go func() {
			defer waitGroup.Done()
			<-start
			committed, commitErr = coordinator.CommitFinalTurn(t.Context(), modeTurn(realtimev1.ModeInterpretation, 1), func(context.Context) error {
				commitCalls++
				callbackGeneration = coordinator.state.Generation
				callbackMode = coordinator.state.ActiveMode
				return nil
			})
		}()
		go func() {
			defer waitGroup.Done()
			<-start
			_, switchErr = coordinator.Switch(t.Context(), modeCommand("operation-1", 1, realtimev1.ModeAssistant))
		}()
		close(start)
		waitGroup.Wait()

		if commitErr != nil || switchErr != nil {
			t.Fatalf("run %d errors: commit %v, switch %v", run, commitErr, switchErr)
		}
		if committed {
			if commitCalls != 1 || callbackGeneration != 1 || callbackMode != realtimev1.ModeInterpretation {
				t.Fatalf("run %d committed callback = calls %d, mode %q, generation %d", run, commitCalls, callbackMode, callbackGeneration)
			}
		} else if commitCalls != 0 {
			t.Fatalf("run %d stale commit invoked callback %d times", run, commitCalls)
		}
		state := coordinator.Snapshot()
		if state.ActiveMode != realtimev1.ModeAssistant || state.Generation != 2 {
			t.Fatalf("run %d final state = %#v", run, state)
		}
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

func TestModeCoordinatorPublishesOneEventForAppliedTransition(t *testing.T) {
	sink := &recordingModeChangedSink{}
	coordinator := mustModeCoordinatorWithSink(t, realtimev1.ModeInterpretation, []realtimev1.Mode{
		realtimev1.ModeInterpretation,
		realtimev1.ModeAssistant,
	}, sink)
	command := modeCommand("operation-1", 1, realtimev1.ModeAssistant)

	result, err := coordinator.Switch(t.Context(), command)
	if err != nil {
		t.Fatalf("Switch() error = %v", err)
	}
	if _, err := coordinator.Switch(t.Context(), command); err != nil {
		t.Fatalf("replayed Switch() error = %v", err)
	}
	events := sink.Events()
	if len(events) != 1 {
		t.Fatalf("published events = %d, want 1", len(events))
	}
	event := events[0]
	if err := event.Validate(); err != nil {
		t.Fatalf("event Validate() error = %v", err)
	}
	if event.EventID != modeChangedEventID(command) || event.SessionID != command.SessionID ||
		event.RuntimeInstanceID != command.RuntimeInstanceID || event.OperationID != command.OperationID ||
		event.FromMode != realtimev1.ModeInterpretation || event.ToMode != realtimev1.ModeAssistant ||
		event.ResultingGeneration != result.State.Generation || !event.OccurredAt.Equal(result.State.UpdatedAt) {
		t.Fatalf("published event = %#v, result = %#v", event, result)
	}
}

func TestModeCoordinatorDoesNotPublishUnchangedMode(t *testing.T) {
	sink := &recordingModeChangedSink{}
	coordinator := mustModeCoordinatorWithSink(t, realtimev1.ModeInterpretation, []realtimev1.Mode{
		realtimev1.ModeInterpretation,
	}, sink)

	result, err := coordinator.Switch(t.Context(), modeCommand("operation-1", 1, realtimev1.ModeInterpretation))
	if err != nil {
		t.Fatalf("Switch() error = %v", err)
	}
	if result.Status != realtimev1.ModeSwitchUnchanged || len(sink.Events()) != 0 {
		t.Fatalf("unchanged result = %#v, events = %#v", result, sink.Events())
	}
}

func TestManagerModeObserverExcludesPreCoordinatorFailures(t *testing.T) {
	observer := &recordingModeCommandObserver{}
	manager := &Manager{deps: Dependencies{ModeCommands: observer}, entries: make(map[string]*entry), locks: newKeyedLocker()}
	if _, err := manager.SwitchMode(t.Context(), realtimev1.SwitchModeCommand{SessionID: "missing"}); !errors.Is(err, session.ErrRuntimeNotFound) {
		t.Fatalf("SwitchMode() error = %v, want runtime not found", err)
	}
	if observer.calls != 0 {
		t.Fatalf("pre-coordinator observation calls = %d, want 0", observer.calls)
	}

	coordinator := mustModeCoordinator(t, realtimev1.ModeInterpretation, []realtimev1.Mode{realtimev1.ModeInterpretation})
	coordinator.observer = observer
	if _, err := coordinator.Switch(t.Context(), modeCommand("operation-1", 1, realtimev1.ModeInterpretation)); err != nil {
		t.Fatalf("coordinator Switch() error = %v", err)
	}
	if observer.calls != 1 || observer.result.Status != realtimev1.ModeSwitchUnchanged || observer.err != nil {
		t.Fatalf("coordinator observation = %#v", observer)
	}
}

func TestModeCoordinatorRetriesFrozenEventBeforeStateCommit(t *testing.T) {
	publishErr := errors.New("outbox unavailable")
	sink := &recordingModeChangedSink{failNext: publishErr}
	nowCalls := 0
	coordinator, err := newModeCoordinator(
		"session-1", "runtime-1", realtimev1.ModeInterpretation,
		[]realtimev1.Mode{realtimev1.ModeInterpretation, realtimev1.ModeAssistant}, sink,
		func() time.Time {
			nowCalls++
			return time.Unix(1700000000+int64(nowCalls), 0).UTC()
		},
	)
	if err != nil {
		t.Fatalf("newModeCoordinator() error = %v", err)
	}
	command := modeCommand("operation-1", 1, realtimev1.ModeAssistant)

	if _, err := coordinator.Switch(t.Context(), command); !errors.Is(err, publishErr) {
		t.Fatalf("first Switch() error = %v, want %v", err, publishErr)
	}
	state := coordinator.Snapshot()
	if state.ActiveMode != realtimev1.ModeInterpretation || state.Generation != 1 || state.LastOperationID != nil {
		t.Fatalf("failed publication changed state = %#v", state)
	}
	result, err := coordinator.Switch(t.Context(), command)
	if err != nil {
		t.Fatalf("retry Switch() error = %v", err)
	}
	attempts := sink.Attempts()
	if len(attempts) != 2 || attempts[0] != attempts[1] {
		t.Fatalf("publication attempts = %#v, want identical frozen payloads", attempts)
	}
	if result.State.ActiveMode != realtimev1.ModeAssistant || result.State.Generation != 2 {
		t.Fatalf("retry result = %#v", result)
	}
}

func TestModeCoordinatorLaterCommandRecoversPendingTransition(t *testing.T) {
	publishErr := errors.New("outbox unavailable")
	sink := &recordingModeChangedSink{failNext: publishErr}
	coordinator := mustModeCoordinatorWithSink(t, realtimev1.ModeInterpretation, []realtimev1.Mode{
		realtimev1.ModeInterpretation,
		realtimev1.ModeAssistant,
	}, sink)
	first := modeCommand("operation-1", 1, realtimev1.ModeAssistant)

	if _, err := coordinator.Switch(t.Context(), first); !errors.Is(err, publishErr) {
		t.Fatalf("first Switch() error = %v, want %v", err, publishErr)
	}
	second := modeCommand("operation-2", 1, realtimev1.ModeInterpretation)
	if _, err := coordinator.Switch(t.Context(), second); !errors.Is(err, ErrModeGenerationConflict) {
		t.Fatalf("later Switch() error = %v, want ErrModeGenerationConflict", err)
	}
	state := coordinator.Snapshot()
	if state.ActiveMode != realtimev1.ModeAssistant || state.Generation != 2 ||
		state.LastOperationID == nil || *state.LastOperationID != first.OperationID {
		t.Fatalf("recovered state = %#v", state)
	}
	attempts := sink.Attempts()
	if len(attempts) != 2 || attempts[0] != attempts[1] || len(sink.Events()) != 1 {
		t.Fatalf("recovery attempts = %#v, accepted = %#v", attempts, sink.Events())
	}
}

func TestManagerModePublicationDoesNotHoldLifecycleLock(t *testing.T) {
	sink := &blockingModeChangedSink{entered: make(chan struct{})}
	coordinator := mustModeCoordinatorWithSink(t, realtimev1.ModeInterpretation, []realtimev1.Mode{
		realtimev1.ModeInterpretation,
		realtimev1.ModeAssistant,
	}, sink)
	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()
	manager := &Manager{
		locks: newKeyedLocker(),
		entries: map[string]*entry{
			"session-1": {mode: coordinator, ctx: runCtx},
		},
	}

	switchDone := make(chan error, 1)
	go func() {
		_, err := manager.SwitchMode(context.Background(), modeCommand("operation-1", 1, realtimev1.ModeAssistant))
		switchDone <- err
	}()
	<-sink.entered

	lockAcquired := make(chan struct{})
	go func() {
		unlock := manager.locks.lock("session-1")
		close(lockAcquired)
		cancelRun()
		unlock()
	}()
	select {
	case <-lockAcquired:
	case <-time.After(2 * time.Second):
		t.Fatal("mode publication held the lifecycle lock")
	}
	select {
	case err := <-switchDone:
		if !errors.Is(err, context.Canceled) || !errors.Is(err, ErrModeEventUnavailable) {
			t.Fatalf("SwitchMode() error = %v, want canceled mode event publication", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SwitchMode() did not stop after runtime cancellation")
	}
	state := coordinator.Snapshot()
	if state.ActiveMode != realtimev1.ModeInterpretation || state.Generation != 1 {
		t.Fatalf("canceled publication changed state = %#v", state)
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

func TestManagerStopCancelsBlockingFinalTurnCommit(t *testing.T) {
	manager, _ := newModeTestManager(t, []string{"runtime-1"}, nil)
	snapshot := session.SessionSnapshot{
		SessionID: "session-1", AccountID: "account-1", StartOperationID: "start-1", TraceID: "trace-1",
	}
	if err := manager.Start(t.Context(), snapshot); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	manager.mu.Lock()
	runCtx := manager.entries[snapshot.SessionID].ctx
	manager.mu.Unlock()
	gate := managerTurnCommitGate{manager: manager}
	commitStarted := make(chan struct{})
	commitDone := make(chan error, 1)
	go func() {
		_, err := gate.CommitFinalTurn(runCtx, modeTurn(realtimev1.ModeInterpretation, 1), func(ctx context.Context) error {
			close(commitStarted)
			<-ctx.Done()
			return ctx.Err()
		})
		commitDone <- err
	}()
	select {
	case <-commitStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for blocking FinalTurn commit")
	}

	stopDone := make(chan error, 1)
	go func() { stopDone <- manager.Stop(context.Background(), snapshot.SessionID) }()
	select {
	case err := <-commitDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("CommitFinalTurn() error = %v, want context canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("FinalTurn commit did not unblock when Stop canceled the runtime")
	}
	select {
	case err := <-stopDone:
		if err != nil {
			t.Fatalf("Stop() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Stop() did not complete after canceling the runtime")
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
	return mustModeCoordinatorWithSink(t, initial, available, &recordingModeChangedSink{})
}

func mustModeCoordinatorWithSink(
	t *testing.T,
	initial realtimev1.Mode,
	available []realtimev1.Mode,
	sink ModeChangedSink,
) *modeCoordinator {
	t.Helper()
	coordinator, err := newModeCoordinator(
		"session-1",
		"runtime-1",
		initial,
		available,
		sink,
		func() time.Time { return time.Unix(1700000000, 0).UTC() },
	)
	if err != nil {
		t.Fatalf("newModeCoordinator() error = %v", err)
	}
	return coordinator
}

type recordingModeChangedSink struct {
	mu       sync.Mutex
	attempts []realtimev1.ModeChangedEvent
	events   []realtimev1.ModeChangedEvent
	failNext error
}

type recordingModeCommandObserver struct {
	result realtimev1.SwitchModeResult
	err    error
	calls  int
}

func (o *recordingModeCommandObserver) RecordModeCommand(result realtimev1.SwitchModeResult, err error) {
	o.result, o.err, o.calls = result, err, o.calls+1
}

type blockingModeChangedSink struct {
	entered chan struct{}
}

func (s *blockingModeChangedSink) Publish(ctx context.Context, _ realtimev1.ModeChangedEvent) error {
	close(s.entered)
	<-ctx.Done()
	return ctx.Err()
}

func (s *recordingModeChangedSink) Publish(_ context.Context, event realtimev1.ModeChangedEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attempts = append(s.attempts, event)
	if s.failNext != nil {
		err := s.failNext
		s.failNext = nil
		return err
	}
	s.events = append(s.events, event)
	return nil
}

func (s *recordingModeChangedSink) Attempts() []realtimev1.ModeChangedEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]realtimev1.ModeChangedEvent(nil), s.attempts...)
}

func (s *recordingModeChangedSink) Events() []realtimev1.ModeChangedEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]realtimev1.ModeChangedEvent(nil), s.events...)
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

func modeTurn(mode realtimev1.Mode, generation int64) pipeline.TurnContext {
	return pipeline.TurnContext{
		SessionID: "session-1",
		Mode: pipeline.TurnModeSnapshot{
			SessionID: "session-1", RuntimeInstanceID: "runtime-1",
			Mode: mode, Generation: generation,
		},
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
