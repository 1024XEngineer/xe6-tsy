package runtime

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	languagesv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/languages/v1"
	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/asr"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/assistant"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/audio"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/command"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/config"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/pipeline"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/session"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/translate"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/tts"
)

func TestDefaultCommandOptionsMatchOrdinaryUtteranceBounds(t *testing.T) {
	if defaultCommandOptions.WindowTTL != 15*time.Second ||
		defaultCommandOptions.NoSpeechTimeout != 2*time.Second ||
		defaultCommandOptions.MaxAudioDuration != 12*time.Second ||
		defaultCommandOptions.EndSilence != 800*time.Millisecond ||
		defaultCommandOptions.PrefixPadding != 500*time.Millisecond {
		t.Fatalf("default command options = %#v, want ordinary VAD sentence bounds", defaultCommandOptions)
	}
}

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
		Command: command.Command{Text: "停止翻译", Action: command.ActionReturnToAssistant, TargetMode: realtimev1.ModeAssistant},
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

func TestCommandExecutorConfiguresLanguagesBeforeModeSwitch(t *testing.T) {
	t.Parallel()
	sink := &recordingModeChangedSink{}
	manager := commandTestManager(t, realtimev1.ModeAssistant, sink)
	configurator := &recordingLanguageConfigurator{onConfigure: func() {
		if manager.entries["session-1"].mode.Snapshot().ActiveMode != realtimev1.ModeAssistant {
			t.Error("mode changed before language configuration completed")
		}
	}}
	executor := commandExecutor{manager: manager, languages: commandLanguageReader(), configurator: configurator}
	_, err := executor.ExecuteCommand(t.Context(), interpretationRequest(command.Arguments{
		SourceLanguage: "zh-cn", TargetLanguage: "en-us",
	}))
	if err != nil {
		t.Fatalf("ExecuteCommand() error = %v", err)
	}
	if len(configurator.requests) != 1 {
		t.Fatalf("configure requests = %#v", configurator.requests)
	}
	configured := configurator.requests[0]
	if configured.SessionID != "session-1" || configured.CommandID != "signal-1" ||
		configured.SourceLanguage != "zh-CN" || configured.TargetLanguage != "en-US" {
		t.Fatalf("configure request = %#v", configured)
	}
	if manager.entries["session-1"].mode.Snapshot().ActiveMode != realtimev1.ModeInterpretation {
		t.Fatal("mode did not switch after configuration")
	}
}

func TestCommandExecutorNormalizesChineseJapaneseDirection(t *testing.T) {
	t.Parallel()
	manager := commandTestManager(t, realtimev1.ModeAssistant, &recordingModeChangedSink{})
	configurator := &recordingLanguageConfigurator{}
	executor := commandExecutor{manager: manager, languages: commandLanguageReader(), configurator: configurator}
	_, err := executor.ExecuteCommand(t.Context(), interpretationRequest(command.Arguments{
		SourceLanguage: "zh", TargetLanguage: "ja",
	}))
	if err != nil {
		t.Fatalf("ExecuteCommand() error = %v", err)
	}
	if len(configurator.requests) != 1 || configurator.requests[0].SourceLanguage != "zh-CN" ||
		configurator.requests[0].TargetLanguage != "ja-JP" {
		t.Fatalf("configure requests = %#v", configurator.requests)
	}
}

func TestCommandExecutorDoesNotSwitchWhenLanguageConfigurationFails(t *testing.T) {
	t.Parallel()
	dependencyErr := errors.New("API unavailable")
	sink := &recordingModeChangedSink{}
	manager := commandTestManager(t, realtimev1.ModeAssistant, sink)
	executor := commandExecutor{
		manager: manager, languages: commandLanguageReader(),
		configurator: &recordingLanguageConfigurator{err: dependencyErr},
	}
	_, err := executor.ExecuteCommand(t.Context(), interpretationRequest(command.Arguments{
		SourceLanguage: "zh-CN", TargetLanguage: "en-US",
	}))
	if !errors.Is(err, dependencyErr) {
		t.Fatalf("ExecuteCommand() error = %v, want %v", err, dependencyErr)
	}
	if manager.entries["session-1"].mode.Snapshot().ActiveMode != realtimev1.ModeAssistant || len(sink.Attempts()) != 0 {
		t.Fatalf("configuration failure changed mode: state=%#v attempts=%#v", manager.entries["session-1"].mode.Snapshot(), sink.Attempts())
	}
}

func TestCommandExecutorRejectsLanguageSnapshotFromAnotherSession(t *testing.T) {
	t.Parallel()
	sink := &recordingModeChangedSink{}
	manager := commandTestManager(t, realtimev1.ModeAssistant, sink)
	snapshot := session.LanguageConfigSnapshot{
		SessionID: "session-2", Version: 1, Status: "active",
		LanguagePairs: []session.LanguagePair{{Source: "zh-CN", Target: "en-US"}},
	}
	configurator := &recordingLanguageConfigurator{}
	_, err := (commandExecutor{
		manager: manager, languages: commandLanguageReaderWith(snapshot), configurator: configurator,
	}).ExecuteCommand(t.Context(), interpretationRequest(command.Arguments{SourceLanguage: "zh-CN"}))
	if !errors.Is(err, ErrCommandLanguageSession) {
		t.Fatalf("ExecuteCommand() error = %v, want session mismatch", err)
	}
	if len(configurator.requests) != 0 || len(sink.Attempts()) != 0 ||
		manager.entries["session-1"].mode.Snapshot().ActiveMode != realtimev1.ModeAssistant {
		t.Fatalf("mismatched snapshot caused side effects: requests=%#v attempts=%#v", configurator.requests, sink.Attempts())
	}
}

func TestCommandExecutorRejectsInvalidLanguageConfigurationResult(t *testing.T) {
	t.Parallel()
	sink := &recordingModeChangedSink{}
	manager := commandTestManager(t, realtimev1.ModeAssistant, sink)
	configurator := command.LanguageConfiguratorFunc(func(context.Context, languagesv1.CommandConfigRequest) (languagesv1.CommandConfigResult, error) {
		return languagesv1.CommandConfigResult{SessionID: "session-2", CommandID: "signal-1", Version: 2}, nil
	})
	_, err := (commandExecutor{
		manager: manager, languages: commandLanguageReader(), configurator: configurator,
	}).ExecuteCommand(t.Context(), interpretationRequest(command.Arguments{
		SourceLanguage: "zh-CN", TargetLanguage: "en-US",
	}))
	if !errors.Is(err, ErrCommandConfigResultInvalid) {
		t.Fatalf("ExecuteCommand() error = %v, want invalid configuration result", err)
	}
	if len(sink.Attempts()) != 0 || manager.entries["session-1"].mode.Snapshot().ActiveMode != realtimev1.ModeAssistant {
		t.Fatalf("invalid result changed mode: state=%#v attempts=%#v", manager.entries["session-1"].mode.Snapshot(), sink.Attempts())
	}
}

func TestCommandExecutorReplaysConfigurationAfterModeCASFailure(t *testing.T) {
	t.Parallel()
	dependencyErr := errors.New("outbox unavailable")
	sink := &recordingModeChangedSink{failNext: dependencyErr}
	manager := commandTestManager(t, realtimev1.ModeAssistant, sink)
	configurator := &recordingLanguageConfigurator{}
	executor := commandExecutor{manager: manager, languages: commandLanguageReader(), configurator: configurator}
	request := interpretationRequest(command.Arguments{SourceLanguage: "zh-CN", TargetLanguage: "en-US"})
	if _, err := executor.ExecuteCommand(t.Context(), request); !errors.Is(err, dependencyErr) {
		t.Fatalf("first ExecuteCommand() error = %v, want %v", err, dependencyErr)
	}
	if _, err := executor.ExecuteCommand(t.Context(), request); err != nil {
		t.Fatalf("retry ExecuteCommand() error = %v", err)
	}
	if len(configurator.requests) != 2 || configurator.requests[0] != configurator.requests[1] {
		t.Fatalf("configuration retries = %#v", configurator.requests)
	}
	if len(sink.Events()) != 1 || manager.entries["session-1"].mode.Snapshot().ActiveMode != realtimev1.ModeInterpretation {
		t.Fatalf("mode events=%#v state=%#v", sink.Events(), manager.entries["session-1"].mode.Snapshot())
	}
}

func TestCommandExecutorCompletesOneLanguageSlot(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name      string
		arguments command.Arguments
	}{
		{name: "source only", arguments: command.Arguments{SourceLanguage: "zh-CN"}},
		{name: "target only", arguments: command.Arguments{TargetLanguage: "en-US"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			manager := commandTestManager(t, realtimev1.ModeAssistant, &recordingModeChangedSink{})
			configurator := &recordingLanguageConfigurator{}
			executor := commandExecutor{manager: manager, languages: commandLanguageReader(), configurator: configurator}
			if _, err := executor.ExecuteCommand(t.Context(), interpretationRequest(tt.arguments)); err != nil {
				t.Fatalf("ExecuteCommand() error = %v", err)
			}
			if len(configurator.requests) != 1 || configurator.requests[0].SourceLanguage != "zh-CN" ||
				configurator.requests[0].TargetLanguage != "en-US" {
				t.Fatalf("configure requests = %#v", configurator.requests)
			}
		})
	}
}

func TestCommandExecutorRejectsAmbiguousAndInvalidLanguages(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name      string
		arguments command.Arguments
		reader    session.LanguageConfigReader
		want      error
	}{
		{name: "slot cannot be completed", arguments: command.Arguments{SourceLanguage: "ja-JP"}, reader: commandLanguageReader(), want: ErrCommandLanguageClarification},
		{name: "same source and target", arguments: command.Arguments{SourceLanguage: "zh-CN", TargetLanguage: "zh-CN"}, reader: commandLanguageReader(), want: ErrCommandLanguageInvalid},
		{name: "inactive current config", reader: commandLanguageReaderWith(session.LanguageConfigSnapshot{
			SessionID: "session-1", Version: 1, Status: "superseded",
			LanguagePairs: []session.LanguagePair{{Source: "zh-CN", Target: "en-US"}},
		}), want: ErrCommandLanguageInvalid},
	} {
		t.Run(tt.name, func(t *testing.T) {
			manager := commandTestManager(t, realtimev1.ModeAssistant, &recordingModeChangedSink{})
			configurator := &recordingLanguageConfigurator{}
			_, err := (commandExecutor{manager: manager, languages: tt.reader, configurator: configurator}).ExecuteCommand(t.Context(), interpretationRequest(tt.arguments))
			if !errors.Is(err, tt.want) {
				t.Fatalf("ExecuteCommand() error = %v, want %v", err, tt.want)
			}
			if len(configurator.requests) != 0 || manager.entries["session-1"].mode.Snapshot().ActiveMode != realtimev1.ModeAssistant {
				t.Fatalf("rejected command caused side effects: requests=%#v state=%#v", configurator.requests, manager.entries["session-1"].mode.Snapshot())
			}
		})
	}
}

func TestCommandExecutorReturnToAssistantSkipsLanguageDependencies(t *testing.T) {
	t.Parallel()
	manager := commandTestManager(t, realtimev1.ModeInterpretation, &recordingModeChangedSink{})
	configurator := &recordingLanguageConfigurator{}
	reader := &countingLanguageReader{err: errors.New("must not read")}
	_, err := (commandExecutor{manager: manager, languages: reader, configurator: configurator}).ExecuteCommand(t.Context(), command.ExecuteRequest{
		SessionID: "session-1", CommandID: "signal-1",
		Command: command.Command{Action: command.ActionReturnToAssistant, TargetMode: realtimev1.ModeAssistant},
	})
	if err != nil {
		t.Fatalf("ExecuteCommand() error = %v", err)
	}
	if reader.calls != 0 || len(configurator.requests) != 0 {
		t.Fatalf("language side effects: reads=%d writes=%#v", reader.calls, configurator.requests)
	}
}

func TestCommandExecutorDispatchesAssistantQueryWithoutChangingMode(t *testing.T) {
	t.Parallel()
	replies := &recordingAssistantReplySink{}
	modeChanges := &recordingModeChangedSink{}
	manager := commandAssistantTestManager(t, realtimev1.ModeAssistant, replies, modeChanges)
	before, err := manager.GetModeState(t.Context(), "session-1")
	if err != nil {
		t.Fatalf("GetModeState(before) error = %v", err)
	}
	result, err := (commandExecutor{manager: manager}).ExecuteCommand(t.Context(), command.ExecuteRequest{
		SessionID: "session-1", CommandID: "signal-query", Language: "zh-CN",
		Command: command.Command{Text: "帮我查一下今天上海的天气", Action: command.ActionAssistantQuery, TargetMode: realtimev1.ModeAssistant},
	})
	if err != nil {
		t.Fatalf("ExecuteCommand() error = %v", err)
	}
	if len(replies.events) != 1 || replies.events[0].Text != "今天上海天气晴朗。" {
		t.Fatalf("assistant replies = %#v", replies.events)
	}
	if result.Status != realtimev1.ModeSwitchUnchanged || result.State.ActiveMode != realtimev1.ModeAssistant ||
		result.State.Generation != before.Generation || len(modeChanges.Events()) != 0 {
		t.Fatalf("result=%#v mode events=%#v", result, modeChanges.Events())
	}
}

func TestCommandExecutorRejectsAssistantQueryOutsideAssistantMode(t *testing.T) {
	t.Parallel()
	replies := &recordingAssistantReplySink{}
	manager := commandAssistantTestManager(t, realtimev1.ModeInterpretation, replies, &recordingModeChangedSink{})
	_, err := (commandExecutor{manager: manager}).ExecuteCommand(t.Context(), command.ExecuteRequest{
		SessionID: "session-1", CommandID: "signal-query", Language: "zh-CN",
		Command: command.Command{Text: "帮我查天气", Action: command.ActionAssistantQuery, TargetMode: realtimev1.ModeAssistant},
	})
	if !errors.Is(err, command.ErrClarificationRequired) {
		t.Fatalf("ExecuteCommand() error = %v, want clarification", err)
	}
	if len(replies.events) != 0 {
		t.Fatalf("assistant replies = %#v", replies.events)
	}
}

func TestCommandExecutorRejectsAssistantQueryWhenTurnCapturesNewMode(t *testing.T) {
	t.Parallel()
	replies := &recordingAssistantReplySink{}
	manager := commandAssistantTestManager(t, realtimev1.ModeAssistant, replies, &recordingModeChangedSink{})
	reader := &switchingCommandLanguageReader{snapshot: activeConfig("session-1")}
	reader.beforeReturn = func() {
		state, err := manager.GetModeState(t.Context(), "session-1")
		if err != nil {
			t.Errorf("GetModeState() error = %v", err)
			return
		}
		_, err = manager.SwitchMode(t.Context(), realtimev1.SwitchModeCommand{
			SessionID: "session-1", RuntimeInstanceID: state.RuntimeInstanceID,
			OperationID: "external-switch", TraceID: "external-switch",
			ExpectedGeneration: state.Generation, TargetMode: realtimev1.ModeInterpretation,
		})
		if err != nil {
			t.Errorf("SwitchMode() error = %v", err)
		}
	}
	manager.commandOpener = pipeline.NewTurnOpener(
		manager.deps.Allocator, reader, managerTurnModeReader{manager: manager},
	)

	_, err := (commandExecutor{manager: manager}).ExecuteCommand(t.Context(), command.ExecuteRequest{
		SessionID: "session-1", CommandID: "signal-query", Language: "zh-CN",
		Command: command.Command{Text: "帮我查天气", Action: command.ActionAssistantQuery, TargetMode: realtimev1.ModeAssistant},
	})
	if !errors.Is(err, command.ErrClarificationRequired) {
		t.Fatalf("ExecuteCommand() error = %v, want clarification", err)
	}
	if len(replies.events) != 0 {
		t.Fatalf("assistant replies = %#v, want none", replies.events)
	}
}

func commandAssistantTestManager(t *testing.T, initial realtimev1.Mode, replies *recordingAssistantReplySink, modeChanges *recordingModeChangedSink) *Manager {
	t.Helper()
	deps := testDependencies(&fakeFrameSource{waitForClose: true}, &fakeLanguageReader{snapshot: activeConfig("session-1")})
	deps.AssistantReplies = replies
	deps.ModeChanges = modeChanges
	deps.NewRuntimeInstanceID = func() (string, error) { return "runtime-1", nil }
	manager, err := NewManager(config.ProviderConfig{}, config.Providers{
		ASR: asr.NewFakeProvider(asr.FakeProviderConfig{}),
		Assistant: assistant.NewFakeProvider(assistant.FakeProviderConfig{Result: assistant.Result{
			Text: "今天上海天气晴朗。", Language: "zh-CN", Provider: "mock-assistant", Model: "assistant-v1",
		}}),
		Translation: &translate.FakeProvider{},
		TTS:         tts.NewFakeProvider(tts.FakeProviderConfig{Result: tts.Result{Provider: "mock-tts", Model: "tts-v1"}}),
	}, deps)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	snapshot := session.SessionSnapshot{
		SessionID: "session-1", AccountID: "account-1", StartOperationID: "start-1", TraceID: "trace-1", InitialMode: initial,
	}
	if err := manager.Start(t.Context(), snapshot); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = manager.Stop(context.Background(), snapshot.SessionID) })
	if err := manager.Activate(t.Context(), snapshot.SessionID, snapshot.StartOperationID); err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	return manager
}

func commandTestManager(t *testing.T, initial realtimev1.Mode, sink *recordingModeChangedSink) *Manager {
	t.Helper()
	coordinator, err := newModeCoordinator(
		"session-1", "runtime-1", initial,
		[]realtimev1.Mode{realtimev1.ModeInterpretation, realtimev1.ModeAssistant}, sink,
		func() time.Time { return time.Unix(10, 0).UTC() },
	)
	if err != nil {
		t.Fatalf("newModeCoordinator() error = %v", err)
	}
	return &Manager{
		locks: newKeyedLocker(), deps: Dependencies{ModeChanges: sink, Now: func() time.Time { return time.Unix(10, 0).UTC() }},
		entries: map[string]*entry{"session-1": {mode: coordinator, ctx: context.Background(), active: true}},
	}
}

func interpretationRequest(arguments command.Arguments) command.ExecuteRequest {
	return command.ExecuteRequest{
		SessionID: "session-1", CommandID: "signal-1",
		Command: command.Command{Action: command.ActionActivateMode, TargetMode: realtimev1.ModeInterpretation, Arguments: arguments},
	}
}

func commandLanguageReader() session.LanguageConfigReader {
	return commandLanguageReaderWith(session.LanguageConfigSnapshot{
		SessionID: "session-1", Version: 1, Status: "active",
		LanguagePairs: []session.LanguagePair{{Source: "zh-CN", Target: "en-US"}, {Source: "en-US", Target: "zh-CN"}},
	})
}

func commandLanguageReaderWith(snapshot session.LanguageConfigSnapshot) session.LanguageConfigReader {
	return &countingLanguageReader{snapshot: snapshot}
}

type countingLanguageReader struct {
	snapshot session.LanguageConfigSnapshot
	err      error
	calls    int
}

type switchingCommandLanguageReader struct {
	snapshot     session.LanguageConfigSnapshot
	beforeReturn func()
}

func (r *switchingCommandLanguageReader) GetCurrentConfig(context.Context, string) (session.LanguageConfigSnapshot, error) {
	if r.beforeReturn != nil {
		r.beforeReturn()
	}
	return r.snapshot, nil
}

func (r *countingLanguageReader) GetCurrentConfig(context.Context, string) (session.LanguageConfigSnapshot, error) {
	r.calls++
	return r.snapshot, r.err
}

type recordingLanguageConfigurator struct {
	requests    []languagesv1.CommandConfigRequest
	err         error
	onConfigure func()
}

func (c *recordingLanguageConfigurator) Configure(_ context.Context, request languagesv1.CommandConfigRequest) (languagesv1.CommandConfigResult, error) {
	c.requests = append(c.requests, request)
	if c.onConfigure != nil {
		c.onConfigure()
	}
	if c.err != nil {
		return languagesv1.CommandConfigResult{}, c.err
	}
	return languagesv1.CommandConfigResult{SessionID: request.SessionID, CommandID: request.CommandID, Version: 2}, nil
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
