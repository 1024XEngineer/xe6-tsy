package runtime

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/asr"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/assistant"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/config"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/pipeline"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/session"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/translate"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/tts"
)

// TestVerticalAcceptanceAssistantInterpretationAssistant exercises the shared Turn Runner
// across both implemented modes. It deliberately calls ProcessAudio directly so the test stays
// offline and deterministic while still traversing ASR, mode snapshotting, the selected handler,
// FinalTurn/AssistantReply commit, shared TTS, usage, and runtime state reporting.
func TestVerticalAcceptanceAssistantInterpretationAssistant(t *testing.T) {
	finals := &verticalFinalSink{}
	replies := &verticalAssistantReplySink{}
	modeChanges := &recordingModeChangedSink{}
	usage := &recordingUsageSink{}
	audioSink := &recordingAudioSink{}
	opened := make([]*fakeFrameSource, 0, 1)
	openCalls := 0
	base := time.Unix(1700000000, 0).UTC()
	deps := testDependencies(&fakeFrameSource{}, &fakeLanguageReader{snapshot: activeConfig("session-1")})
	deps.FrameSources = FrameSourceFactoryFunc(func(context.Context, session.SessionSnapshot) (AudioInput, error) {
		openCalls++
		source := &fakeFrameSource{waitForClose: true}
		opened = append(opened, source)
		return AudioInput{Source: source, SourceLanguage: "zh-CN"}, nil
	})
	deps.FinalTurns = finals
	deps.AssistantReplies = replies
	deps.ModeChanges = modeChanges
	deps.Usage = usage
	deps.Audio = audioSink
	deps.NewRuntimeInstanceID = func() (string, error) { return "runtime-1", nil }
	manager, err := NewManager(config.ProviderConfig{}, config.Providers{
		ASR: asr.NewFakeProvider(asr.FakeProviderConfig{Final: asr.FinalResult{
			Text: "你好", SourceLanguage: "zh-CN", Provider: "mock-asr", Model: "asr-v1",
		}}),
		Assistant: assistant.NewFakeProvider(assistant.FakeProviderConfig{Result: assistant.Result{
			Text: "你好，我可以帮你。", Language: "zh-CN", Provider: "mock-assistant", Model: "assistant-v1",
		}}),
		Translation: &translate.FakeProvider{Result: translate.Result{
			Text: "Hello", Provider: "mock-translation", Model: "translation-v1",
		}},
		TTS: tts.NewFakeProvider(tts.FakeProviderConfig{
			Chunks: []tts.AudioChunk{{SequenceNo: 1, Data: []byte{1, 2}}},
			Result: tts.Result{Provider: "mock-tts", Model: "tts-v1"},
		}),
	}, deps)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	snapshot := session.SessionSnapshot{
		SessionID: "session-1", AccountID: "account-1", StartOperationID: "start-1", TraceID: "trace-1",
		InitialMode: realtimev1.ModeAssistant,
	}
	if err := manager.Start(t.Context(), snapshot); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = manager.Stop(context.Background(), snapshot.SessionID) })

	initial, err := manager.GetModeState(t.Context(), snapshot.SessionID)
	if err != nil {
		t.Fatalf("GetModeState() error = %v", err)
	}
	if initial.ActiveMode != realtimev1.ModeAssistant || initial.Generation != 1 || initial.RuntimeInstanceID != "runtime-1" {
		t.Fatalf("initial mode state = %#v", initial)
	}

	turn1, err := manager.processor.ProcessAudio(t.Context(), verticalTurnRequest(base))
	if err != nil {
		t.Fatalf("assistant ProcessAudio() error = %v", err)
	}
	if turn1.Mode.Mode != realtimev1.ModeAssistant || turn1.Mode.Generation != 1 {
		t.Fatalf("assistant Turn mode = %#v", turn1.Mode)
	}

	interpretation := verticalModeCommand(initial, "mode-interpretation", realtimev1.ModeInterpretation)
	firstSwitch, err := manager.SwitchMode(t.Context(), interpretation)
	if err != nil {
		t.Fatalf("assistant -> interpretation SwitchMode() error = %v", err)
	}
	if firstSwitch.Status != realtimev1.ModeSwitchApplied || firstSwitch.State.ActiveMode != realtimev1.ModeInterpretation || firstSwitch.State.Generation != 2 {
		t.Fatalf("assistant -> interpretation result = %#v", firstSwitch)
	}

	turn2, err := manager.processor.ProcessAudio(t.Context(), verticalTurnRequest(base.Add(time.Second)))
	if err != nil {
		t.Fatalf("interpretation ProcessAudio() error = %v", err)
	}
	if turn2.Mode.Mode != realtimev1.ModeInterpretation || turn2.Mode.Generation != 2 {
		t.Fatalf("interpretation Turn mode = %#v", turn2.Mode)
	}

	assistantCommand := verticalModeCommand(firstSwitch.State, "mode-assistant", realtimev1.ModeAssistant)
	secondSwitch, err := manager.SwitchMode(t.Context(), assistantCommand)
	if err != nil {
		t.Fatalf("interpretation -> assistant SwitchMode() error = %v", err)
	}
	if secondSwitch.Status != realtimev1.ModeSwitchApplied || secondSwitch.State.ActiveMode != realtimev1.ModeAssistant || secondSwitch.State.Generation != 3 {
		t.Fatalf("interpretation -> assistant result = %#v", secondSwitch)
	}

	turn3, err := manager.processor.ProcessAudio(t.Context(), verticalTurnRequest(base.Add(2*time.Second)))
	if err != nil {
		t.Fatalf("assistant (second) ProcessAudio() error = %v", err)
	}
	if turn3.Mode.Mode != realtimev1.ModeAssistant || turn3.Mode.Generation != 3 {
		t.Fatalf("assistant (second) Turn mode = %#v", turn3.Mode)
	}

	// Replaying the applied operation returns its frozen result and cannot publish a second event.
	replayed, err := manager.SwitchMode(t.Context(), interpretation)
	if err != nil {
		t.Fatalf("replayed interpretation command error = %v", err)
	}
	if replayed.State.Generation != firstSwitch.State.Generation || replayed.State.ActiveMode != firstSwitch.State.ActiveMode {
		t.Fatalf("replayed result = %#v, want %#v", replayed, firstSwitch)
	}
	stale := interpretation
	stale.OperationID = "mode-stale"
	if _, err := manager.SwitchMode(t.Context(), stale); !errors.Is(err, ErrModeGenerationConflict) {
		t.Fatalf("stale command error = %v, want generation conflict", err)
	}

	if openCalls != 1 || len(opened) != 1 {
		t.Fatalf("shared media input opened %d times, want one", openCalls)
	}
	if len(modeChanges.Events()) != 2 {
		t.Fatalf("mode changed events = %d, want two applied transitions", len(modeChanges.Events()))
	}
	if len(replies.Events()) != 2 || len(finals.Events()) != 1 {
		t.Fatalf("mode handler outputs = assistant replies %d, FinalTurns %d", len(replies.Events()), len(finals.Events()))
	}
	if len(usage.Facts()) != 9 || len(audioSink.Chunks()) != 3 {
		t.Fatalf("shared output facts/chunks = %d/%d, want 9/3", len(usage.Facts()), len(audioSink.Chunks()))
	}
	for index, turn := range []pipeline.TurnContext{turn1, turn2, turn3} {
		if turn.Mode.RuntimeInstanceID != "runtime-1" || turn.SessionID != "session-1" {
			t.Fatalf("Turn %d runtime identity = %#v", index+1, turn.Mode)
		}
	}
}

// TestVerticalAcceptanceRejectsStaleCommandsAfterRuntimeRestart proves that generation values
// are scoped to a runtime instance. A command captured before Stop must not mutate a fresh run,
// even when the fresh run starts at generation one again.
func TestVerticalAcceptanceRejectsStaleCommandsAfterRuntimeRestart(t *testing.T) {
	opened := 0
	runtimeIDs := []string{"runtime-old", "runtime-new"}
	deps := testDependencies(&fakeFrameSource{}, &fakeLanguageReader{snapshot: activeConfig("session-1")})
	deps.FrameSources = FrameSourceFactoryFunc(func(context.Context, session.SessionSnapshot) (AudioInput, error) {
		opened++
		return AudioInput{Source: &fakeFrameSource{}, SourceLanguage: "zh-CN"}, nil
	})
	deps.AssistantReplies = &verticalAssistantReplySink{}
	deps.NewRuntimeInstanceID = func() (string, error) {
		id := runtimeIDs[0]
		runtimeIDs = runtimeIDs[1:]
		return id, nil
	}
	manager, err := newManager(config.Providers{
		ASR: asr.NewFakeProvider(asr.FakeProviderConfig{}),
		Assistant: assistant.NewFakeProvider(assistant.FakeProviderConfig{Result: assistant.Result{
			Text: "ok", Language: "zh-CN", Provider: "assistant", Model: "v1",
		}}),
		Translation: &translate.FakeProvider{}, TTS: tts.NewFakeProvider(tts.FakeProviderConfig{}),
	}, deps)
	if err != nil {
		t.Fatalf("newManager() error = %v", err)
	}
	snapshot := session.SessionSnapshot{SessionID: "session-1", AccountID: "account-1", StartOperationID: "start-old", TraceID: "trace-1", InitialMode: realtimev1.ModeAssistant}
	if err := manager.Start(t.Context(), snapshot); err != nil {
		t.Fatalf("first Start() error = %v", err)
	}
	oldState, err := manager.GetModeState(t.Context(), snapshot.SessionID)
	if err != nil {
		t.Fatalf("first GetModeState() error = %v", err)
	}
	oldCommand := verticalModeCommand(oldState, "old-command", realtimev1.ModeInterpretation)
	if err := manager.Stop(t.Context(), snapshot.SessionID); err != nil {
		t.Fatalf("first Stop() error = %v", err)
	}

	snapshot.StartOperationID = "start-new"
	if err := manager.Start(t.Context(), snapshot); err != nil {
		t.Fatalf("second Start() error = %v", err)
	}
	t.Cleanup(func() { _ = manager.Stop(context.Background(), snapshot.SessionID) })
	newState, err := manager.GetModeState(t.Context(), snapshot.SessionID)
	if err != nil {
		t.Fatalf("second GetModeState() error = %v", err)
	}
	if newState.RuntimeInstanceID != "runtime-new" || newState.Generation != 1 || newState.ActiveMode != realtimev1.ModeAssistant {
		t.Fatalf("new runtime state = %#v", newState)
	}
	if _, err := manager.SwitchMode(t.Context(), oldCommand); !errors.Is(err, ErrModeRuntimeInstanceMismatch) {
		t.Fatalf("old runtime command error = %v, want runtime mismatch", err)
	}
	current, err := manager.GetModeState(t.Context(), snapshot.SessionID)
	if err != nil {
		t.Fatalf("current GetModeState() error = %v", err)
	}
	if current != newState || opened != 2 {
		t.Fatalf("old command changed fresh runtime: state=%#v opens=%d", current, opened)
	}
}

// TestVerticalAcceptanceFinalTurnCommitAndModeSwitchAreLinearized runs both operations from
// the same generation. Exactly one ordering may win, but the commit callback and mode event must
// never both observe an inconsistent state or publish duplicate durable facts.
func TestVerticalAcceptanceFinalTurnCommitAndModeSwitchAreLinearized(t *testing.T) {
	for attempt := 0; attempt < 32; attempt++ {
		t.Run(fmt.Sprintf("attempt-%02d", attempt), func(t *testing.T) {
			runFinalTurnModeRace(t)
		})
	}
}

func runFinalTurnModeRace(t *testing.T) {
	t.Helper()
	sink := &recordingModeChangedSink{}
	coordinator := mustModeCoordinatorWithSink(t, realtimev1.ModeInterpretation, []realtimev1.Mode{
		realtimev1.ModeInterpretation, realtimev1.ModeAssistant,
	}, sink)
	finals := &verticalFinalSink{}
	turn := modeTurn(realtimev1.ModeInterpretation, 1)
	start := make(chan struct{})
	commitCalls := make(chan int, 1)
	switchResults := make(chan error, 1)
	commitResults := make(chan struct {
		committed bool
		err       error
	}, 1)
	go func() {
		<-start
		result, err := coordinator.Switch(context.Background(), modeCommand("race-switch", 1, realtimev1.ModeAssistant))
		switchResults <- func() error {
			if err == nil && result.State.ActiveMode != realtimev1.ModeAssistant {
				return errors.New("switch returned wrong active mode")
			}
			return err
		}()
	}()
	go func() {
		<-start
		committed, err := coordinator.CommitFinalTurn(context.Background(), turn, func(context.Context) error {
			commitCalls <- 1
			return finals.Publish(context.Background(), recordsv1.FinalTurnEvent{
				EventVersion: recordsv1.FinalTurnEventVersion, EventID: "final-race", TraceID: "trace-race",
				TurnID: "turn-race", SessionID: "session-1", SequenceNo: 1,
				SourceLanguage: "zh-CN", TargetLanguage: "en-US", LanguageConfigVersion: 1,
				SourceText: "你好", TranslatedText: "hello", SpeakerCode: recordsv1.PendingSpeakerCode,
				AttributionStatus: recordsv1.AttributionPending,
				StartedAt:         time.Unix(1700000000, 0).UTC(), EndedAt: time.Unix(1700000001, 0).UTC(),
				OccurredAt: time.Unix(1700000001, 0).UTC(),
			})
		})
		commitResults <- struct {
			committed bool
			err       error
		}{committed: committed, err: err}
	}()
	close(start)
	if err := <-switchResults; err != nil {
		t.Fatalf("mode switch race error = %v", err)
	}
	commit := <-commitResults
	if commit.err != nil {
		t.Fatalf("FinalTurn race error = %v", commit.err)
	}
	callbackCalls := 0
	select {
	case <-commitCalls:
		callbackCalls++
	default:
	}
	if callbackCalls != boolInt(commit.committed) {
		t.Fatalf("FinalTurn callback calls = %d, committed=%t", callbackCalls, commit.committed)
	}
	if len(finals.Events()) != boolInt(commit.committed) {
		t.Fatalf("committed FinalTurn events = %d, committed=%t", len(finals.Events()), commit.committed)
	}
	state := coordinator.Snapshot()
	if state.ActiveMode != realtimev1.ModeAssistant || state.Generation != 2 {
		t.Fatalf("race final state = %#v", state)
	}
	if len(sink.Events()) != 1 {
		t.Fatalf("race mode events = %d, want one", len(sink.Events()))
	}
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func verticalTurnRequest(startedAt time.Time) pipeline.TurnProcessRequest {
	return pipeline.TurnProcessRequest{
		SessionID: "session-1", AccountID: "account-1", TraceID: "trace-turn",
		SourceLanguage: "zh-CN", StartedAt: startedAt, AudioChunks: [][]byte{{1, 2, 3}},
	}
}

func verticalModeCommand(state realtimev1.ModeStateSnapshot, operationID string, target realtimev1.Mode) realtimev1.SwitchModeCommand {
	return realtimev1.SwitchModeCommand{
		SessionID: state.SessionID, RuntimeInstanceID: state.RuntimeInstanceID,
		OperationID: operationID, TraceID: "trace-mode-" + operationID,
		ExpectedGeneration: state.Generation, TargetMode: target,
	}
}

type verticalFinalSink struct {
	mu     sync.Mutex
	events []recordsv1.FinalTurnEvent
}

func (s *verticalFinalSink) Publish(_ context.Context, event recordsv1.FinalTurnEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
	return nil
}

func (s *verticalFinalSink) Events() []recordsv1.FinalTurnEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]recordsv1.FinalTurnEvent(nil), s.events...)
}

type verticalAssistantReplySink struct {
	mu     sync.Mutex
	events []realtimev1.AssistantReplyEvent
}

func (s *verticalAssistantReplySink) Publish(_ context.Context, event realtimev1.AssistantReplyEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
	return nil
}

func (s *verticalAssistantReplySink) Events() []realtimev1.AssistantReplyEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]realtimev1.AssistantReplyEvent(nil), s.events...)
}

var _ recordsv1.FinalTurnSink = (*verticalFinalSink)(nil)
var _ pipeline.AssistantReplySink = (*verticalAssistantReplySink)(nil)
