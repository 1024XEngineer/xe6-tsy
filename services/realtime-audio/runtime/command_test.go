package runtime

import (
	"context"
	"sync"
	"testing"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/asr"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/audio"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/command"
)

func TestCommandExecutorUsesRuntimeModeCoordinator(t *testing.T) {
	t.Parallel()
	sink := &recordingModeChangedSink{}
	coordinator, err := newModeCoordinator(
		"session-1", "runtime-1", realtimev1.ModeInterpretation,
		[]realtimev1.Mode{realtimev1.ModeInterpretation, realtimev1.ModeAssistant},
		sink, func() time.Time { return time.Unix(10, 0).UTC() },
	)
	if err != nil {
		t.Fatalf("newModeCoordinator() error = %v", err)
	}
	manager := &Manager{
		locks: newKeyedLocker(),
		deps:  Dependencies{ModeChanges: sink},
		entries: map[string]*entry{
			"session-1": {mode: coordinator, ctx: context.Background(), active: true},
		},
	}

	result, err := (commandExecutor{manager: manager}).ExecuteCommand(context.Background(), command.ExecuteRequest{
		SessionID: "session-1", CommandID: "signal-1",
		Command: command.Command{Text: "停止翻译", TargetMode: realtimev1.ModeAssistant},
	})
	if err != nil {
		t.Fatalf("ExecuteCommand() error = %v", err)
	}
	if result.Status != realtimev1.ModeSwitchApplied || result.State.ActiveMode != realtimev1.ModeAssistant {
		t.Fatalf("ExecuteCommand() result = %#v, want applied assistant state", result)
	}
	if got := coordinator.Snapshot().ActiveMode; got != realtimev1.ModeAssistant {
		t.Fatalf("active mode = %q, want %q", got, realtimev1.ModeAssistant)
	}
}

func TestRuntimeCommandGateInterruptsPlaybackBeforeArming(t *testing.T) {
	t.Parallel()
	interrupter := &recordingPlaybackInterrupter{}
	gate, err := command.NewGate(command.Dependencies{
		Classifier: speechClassifier{}, ASR: asr.NewFakeProvider(asr.FakeProviderConfig{}),
		Interpreter: command.LegacyInterpreter{}, Validator: commandRegistryForTest(t), Executor: commandExecutor{},
	}, command.Options{
		WindowTTL: 2 * time.Second, NoSpeechTimeout: time.Second,
		MaxAudioDuration: time.Second, EndSilence: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewGate() error = %v", err)
	}
	wrapped := newRuntimeCommandGate(gate, interrupter)
	openedAt := time.Unix(20, 0).UTC()
	if err := wrapped.Open(command.OpenRequest{SessionID: "session-1", CommandID: "signal-1", OpenedAt: openedAt}); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if interrupter.sessionID != "session-1" || interrupter.reason != "wake_word_detected" {
		t.Fatalf("interrupt = %#v, want wake-word cancellation", interrupter)
	}
	if got := gate.State(); got != command.StateArmed {
		t.Fatalf("gate state = %q, want armed", got)
	}
}

func TestRuntimeCommandGateForwardsReplay(t *testing.T) {
	t.Parallel()
	gate, err := command.NewGate(command.Dependencies{
		Classifier: speechClassifier{}, ASR: asr.NewFakeProvider(asr.FakeProviderConfig{}),
		Interpreter: command.LegacyInterpreter{}, Validator: commandRegistryForTest(t), Executor: commandExecutor{},
	}, command.Options{
		WindowTTL: 2 * time.Second, NoSpeechTimeout: time.Second,
		MaxAudioDuration: time.Second, EndSilence: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewGate() error = %v", err)
	}
	t.Cleanup(gate.Cancel)
	openedAt := time.Unix(21, 0).UTC()
	if err := gate.Open(command.OpenRequest{
		SessionID: "session-1", CommandID: "signal-1", OpenedAt: openedAt,
		CaptureFrom: openedAt.Add(-time.Second),
	}); err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	result := newRuntimeCommandGate(gate, nil).Replay(t.Context(), []audio.Frame{
		mustFrame(t, []byte{1, 0}, openedAt.Add(-500*time.Millisecond)),
	})
	if !result.Consumed || result.State != command.StateCapturing {
		t.Fatalf("Replay() result = %#v, want consumed capturing", result)
	}
}

func commandRegistryForTest(t *testing.T) *command.Registry {
	t.Helper()
	registry, err := commandRegistry([]realtimev1.Mode{realtimev1.ModeInterpretation, realtimev1.ModeAssistant})
	if err != nil {
		t.Fatalf("commandRegistry() error = %v", err)
	}
	return registry
}

type recordingPlaybackInterrupter struct {
	mu        sync.Mutex
	sessionID string
	reason    string
}

func (r *recordingPlaybackInterrupter) InterruptCurrent(_ context.Context, sessionID, reason string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessionID, r.reason = sessionID, reason
	return nil
}

var _ PlaybackInterrupter = (*recordingPlaybackInterrupter)(nil)
