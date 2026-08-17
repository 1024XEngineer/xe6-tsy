package runtime

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/asr"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/assistant"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/audio"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/command"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/config"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/pipeline"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/segment"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/session"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/translate"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/tts"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/vad"
)

func TestManagerRegistersAssistantWithoutReplacingRealtimeRuntime(t *testing.T) {
	var output bytes.Buffer
	source := &fakeFrameSource{waitForClose: true}
	deps := testDependencies(source, &fakeLanguageReader{snapshot: activeConfig("session-1")})
	deps.AssistantReplies = &recordingAssistantReplySink{}
	deps.NewRuntimeInstanceID = func() (string, error) { return "runtime-1", nil }
	deps.Logger = slog.New(slog.NewJSONHandler(&output, nil))
	manager, err := newManager(config.Providers{
		ASR: asr.NewFakeProvider(asr.FakeProviderConfig{}),
		Assistant: assistant.NewFakeProvider(assistant.FakeProviderConfig{Result: assistant.Result{
			Text: "hello", Language: "en-US", Provider: "mock-llm", Model: "v1",
		}}),
		Translation: &translate.FakeProvider{}, TTS: tts.NewFakeProvider(tts.FakeProviderConfig{}),
	}, deps)
	if err != nil {
		t.Fatalf("newManager() error = %v", err)
	}
	snapshot := session.SessionSnapshot{
		SessionID: "session-1", AccountID: "account-1", StartOperationID: "start-1", TraceID: "trace-1",
	}
	if err := manager.Start(t.Context(), snapshot); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = manager.Stop(context.Background(), snapshot.SessionID) })
	before, err := manager.GetModeState(t.Context(), snapshot.SessionID)
	if err != nil || before.ActiveMode != realtimev1.ModeInterpretation || before.Generation != 1 {
		t.Fatalf("initial mode = %#v, %v", before, err)
	}
	command := realtimev1.SwitchModeCommand{
		SessionID: snapshot.SessionID, RuntimeInstanceID: before.RuntimeInstanceID,
		OperationID: "switch-1", TraceID: snapshot.TraceID,
		ExpectedGeneration: before.Generation, TargetMode: realtimev1.ModeAssistant,
	}
	result, err := manager.SwitchMode(t.Context(), command)
	if err != nil || result.State.ActiveMode != realtimev1.ModeAssistant || result.State.Generation != 2 {
		t.Fatalf("SwitchMode() = %#v, %v", result, err)
	}
	command.OperationID = "switch-stale"
	if _, err := manager.SwitchMode(t.Context(), command); !errors.Is(err, ErrModeGenerationConflict) {
		t.Fatalf("stale SwitchMode() error = %v, want generation conflict", err)
	}
	result, err = manager.SwitchMode(t.Context(), realtimev1.SwitchModeCommand{
		SessionID: snapshot.SessionID, RuntimeInstanceID: before.RuntimeInstanceID,
		OperationID: "switch-2", TraceID: snapshot.TraceID,
		ExpectedGeneration: result.State.Generation, TargetMode: realtimev1.ModeInterpretation,
	})
	if err != nil || result.State.ActiveMode != realtimev1.ModeInterpretation || result.State.Generation != 3 ||
		result.State.RuntimeInstanceID != before.RuntimeInstanceID {
		t.Fatalf("reverse SwitchMode() = %#v, %v", result, err)
	}
	for _, field := range []string{
		`"event":"runtime_started"`, `"trace_id":"trace-1"`, `"active_mode":"interpretation"`,
		`"event":"mode_switch"`, `"status":"applied"`,
		`"expected_generation":1`,
		`"status":"failed"`, `"error_class":"generation_conflict"`,
	} {
		if !strings.Contains(output.String(), field) {
			t.Fatalf("mode logs = %s, missing %s", output.String(), field)
		}
	}
	if got := modeSwitchErrorClass(ErrModeEventUnavailable); got != "event_unavailable" {
		t.Fatalf("mode event error class = %q", got)
	}
	if source.CloseCalls() != 0 {
		t.Fatalf("mode switch closed shared audio source %d times", source.CloseCalls())
	}
}

func TestManagerStartsInRequestedAssistantMode(t *testing.T) {
	source := &fakeFrameSource{waitForClose: true}
	deps := testDependencies(source, &fakeLanguageReader{snapshot: activeConfig("session-1")})
	deps.AssistantReplies = &recordingAssistantReplySink{}
	deps.NewRuntimeInstanceID = func() (string, error) { return "runtime-1", nil }
	manager, err := newManager(config.Providers{
		ASR: asr.NewFakeProvider(asr.FakeProviderConfig{}),
		Assistant: assistant.NewFakeProvider(assistant.FakeProviderConfig{Result: assistant.Result{
			Text: "hello", Language: "en-US", Provider: "mock-llm", Model: "v1",
		}}),
		Translation: &translate.FakeProvider{}, TTS: tts.NewFakeProvider(tts.FakeProviderConfig{}),
	}, deps)
	if err != nil {
		t.Fatalf("newManager() error = %v", err)
	}
	snapshot := session.SessionSnapshot{
		SessionID: "session-1", AccountID: "account-1", StartOperationID: "start-1", TraceID: "trace-1",
		InitialMode: realtimev1.ModeAssistant,
	}
	if err := manager.Start(t.Context(), snapshot); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = manager.Stop(context.Background(), snapshot.SessionID) })
	state, err := manager.GetModeState(t.Context(), snapshot.SessionID)
	if err != nil {
		t.Fatalf("GetModeState() error = %v", err)
	}
	if state.ActiveMode != realtimev1.ModeAssistant || state.Generation != 1 {
		t.Fatalf("initial mode = %#v, want assistant generation 1", state)
	}
}

func TestManagerRoutesAssistantReplyOnExistingAudioSession(t *testing.T) {
	base := time.Unix(1700000000, 0).UTC()
	source := &fakeFrameSource{frames: []audio.Frame{
		mustFrame(t, []byte{1, 0}, base),
		mustFrame(t, []byte{1, 0}, base.Add(20*time.Millisecond)),
		mustFrame(t, []byte{0, 0}, base.Add(100*time.Millisecond)),
	}, waitForClose: true}
	replies := &assistantReplySignalSink{events: make(chan realtimev1.AssistantReplyEvent, 1)}
	deps := testDependencies(source, &fakeLanguageReader{snapshot: activeConfig("session-1")})
	deps.AssistantReplies = replies
	deps.NewRuntimeInstanceID = func() (string, error) { return "runtime-1", nil }

	asrProvider := asr.NewFakeProvider(asr.FakeProviderConfig{Final: asr.FinalResult{
		Text: "你好", SourceLanguage: "zh-CN", Provider: "mock-asr", Model: "asr-v1",
	}})
	llmProvider := assistant.NewFakeProvider(assistant.FakeProviderConfig{Result: assistant.Result{
		Text: "你好，我可以帮你。", Language: "zh-CN", Provider: "mock-llm", Model: "assistant-v1",
	}})
	manager, err := NewManager(config.ProviderConfig{}, config.Providers{
		ASR: asrProvider, Assistant: llmProvider, Translation: &translate.FakeProvider{},
		TTS: tts.NewFakeProvider(tts.FakeProviderConfig{}),
	}, deps)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	snapshot := session.SessionSnapshot{
		SessionID: "session-1", AccountID: "account-1", StartOperationID: "start-1", TraceID: "trace-1",
	}
	if err := manager.Start(t.Context(), snapshot); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = manager.Stop(context.Background(), snapshot.SessionID) })

	state, err := manager.GetModeState(t.Context(), snapshot.SessionID)
	if err != nil {
		t.Fatalf("GetModeState() error = %v", err)
	}
	if state.ActiveMode != realtimev1.ModeInterpretation {
		t.Fatalf("default mode = %q, want interpretation", state.ActiveMode)
	}
	result, err := manager.SwitchMode(t.Context(), realtimev1.SwitchModeCommand{
		SessionID: snapshot.SessionID, RuntimeInstanceID: state.RuntimeInstanceID,
		OperationID: "switch-assistant", TraceID: snapshot.TraceID,
		ExpectedGeneration: state.Generation, TargetMode: realtimev1.ModeAssistant,
	})
	if err != nil || result.State.ActiveMode != realtimev1.ModeAssistant {
		t.Fatalf("SwitchMode() = %#v, %v", result, err)
	}
	if source.CloseCalls() != 0 {
		t.Fatalf("mode switch closed the shared audio source %d times", source.CloseCalls())
	}
	if err := manager.Activate(t.Context(), snapshot.SessionID, snapshot.StartOperationID); err != nil {
		t.Fatalf("Activate() error = %v", err)
	}

	event := <-replies.events
	if event.SessionID != snapshot.SessionID || event.Text != "你好，我可以帮你。" ||
		event.RuntimeInstanceID != state.RuntimeInstanceID || event.Generation != result.State.Generation {
		t.Fatalf("AssistantReply event = %#v", event)
	}
	if len(asrProvider.Requests()) != 1 || len(llmProvider.Requests()) != 1 {
		t.Fatalf("provider calls = ASR %d, LLM %d", len(asrProvider.Requests()), len(llmProvider.Requests()))
	}
}

func TestManagerRunsOneTurnThroughConfiguredProviders(t *testing.T) {
	base := time.Unix(1700000000, 0).UTC()
	source := &fakeFrameSource{frames: []audio.Frame{
		mustFrame(t, []byte{1, 0}, base),
		mustFrame(t, []byte{1, 0}, base.Add(20*time.Millisecond)),
		mustFrame(t, []byte{0, 0}, base.Add(100*time.Millisecond)),
	}, waitForClose: true}
	asrProvider := asr.NewFakeProvider(asr.FakeProviderConfig{Final: asr.FinalResult{
		Text: "你好", SourceLanguage: "zh-CN", Provider: "mock-asr", Model: "v1",
	}})
	translator := &translate.FakeProvider{Result: translate.Result{Text: "hello", Provider: "mock-llm", Model: "qwen3.6-flash"}}
	ttsProvider := tts.NewFakeProvider(tts.FakeProviderConfig{
		Chunks: []tts.AudioChunk{{SequenceNo: 1, Data: []byte{3, 4}}},
		Result: tts.Result{Provider: "mock-tts", Model: "v1"},
	})
	languages := &fakeLanguageReader{snapshot: session.LanguageConfigSnapshot{
		SessionID: "session-1", Version: 7, Status: "active",
		LanguagePairs: []session.LanguagePair{{Source: "zh-CN", Target: "en-US"}},
	}}
	finals := &recordingFinalSink{events: make(chan recordsv1.FinalTurnEvent, 1)}
	usage := &recordingUsageSink{}
	audioSink := &recordingAudioSink{}
	reporter := &recordingRuntimeReporter{}
	openCalls := 0
	manager, err := NewManager(config.ProviderConfig{}, config.Providers{
		ASR: asrProvider, Translation: translator, TTS: ttsProvider,
	}, Dependencies{
		FrameSources: FrameSourceFactoryFunc(func(context.Context, session.SessionSnapshot) (AudioInput, error) {
			openCalls++
			return AudioInput{Source: source, SourceLanguage: "zh-CN"}, nil
		}),
		NewSegmenter: func() (*vad.Segmenter, error) {
			return vad.NewSegmenter(speechClassifier{}, vad.Options{SilenceAfter: 50 * time.Millisecond, MaxDuration: time.Second})
		},
		Languages: languages, FinalTurns: finals, ModeChanges: &recordingModeChangedSink{},
		Usage: usage, Audio: audioSink, Runtime: reporter,
	})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	snapshot := session.SessionSnapshot{SessionID: "session-1", AccountID: "account-1", StartOperationID: "operation-1", TraceID: "trace-1", Status: "created"}
	if err := manager.Start(context.Background(), snapshot); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := manager.Start(context.Background(), snapshot); err != nil {
		t.Fatalf("idempotent Start() error = %v", err)
	}
	if openCalls != 1 {
		t.Fatalf("source open calls = %d, want 1", openCalls)
	}
	if err := manager.Activate(context.Background(), snapshot.SessionID, snapshot.StartOperationID); err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	if err := manager.Activate(context.Background(), snapshot.SessionID, snapshot.StartOperationID); err != nil {
		t.Fatalf("idempotent Activate() error = %v", err)
	}
	select {
	case event := <-finals.events:
		if event.TurnID == "" || event.SourceText != "你好" || event.TranslatedText != "hello" {
			t.Fatalf("FinalTurn = %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for FinalTurn")
	}
	deadline := time.Now().Add(time.Second)
	for len(usage.Facts()) < 3 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if err := manager.Stop(context.Background(), snapshot.SessionID); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if err := manager.Stop(context.Background(), snapshot.SessionID); err != nil {
		t.Fatalf("idempotent Stop() error = %v", err)
	}
	if len(asrProvider.Requests()) != 1 || len(translator.Requests()) != 1 || len(ttsProvider.Requests()) != 1 {
		t.Fatalf("provider calls = ASR %d, translation %d, TTS %d", len(asrProvider.Requests()), len(translator.Requests()), len(ttsProvider.Requests()))
	}
	if languages.Calls() != 1 {
		t.Fatalf("language snapshot calls = %d, want 1", languages.Calls())
	}
	if len(usage.Facts()) != 3 || len(audioSink.Chunks()) != 1 {
		t.Fatalf("sink calls = usage %d, audio %d", len(usage.Facts()), len(audioSink.Chunks()))
	}
	if source.CloseCalls() != 1 {
		t.Fatalf("source close calls = %d, want 1", source.CloseCalls())
	}
}

func TestManagerPlaysFallbackThroughActiveSession(t *testing.T) {
	source := &fakeFrameSource{waitForClose: true}
	audioSink := &recordingAudioSink{}
	usageSink := &recordingUsageSink{}
	deps := testDependencies(source, &fakeLanguageReader{snapshot: activeConfig("session-1")})
	deps.Audio = audioSink
	deps.Usage = usageSink
	manager, err := NewManager(config.ProviderConfig{}, config.Providers{
		ASR:         asr.NewFakeProvider(asr.FakeProviderConfig{}),
		Translation: &translate.FakeProvider{},
		TTS: tts.NewFakeProvider(tts.FakeProviderConfig{
			Chunks: []tts.AudioChunk{{SequenceNo: 1, Data: []byte{1, 2}}},
			Result: tts.Result{Provider: "mock-tts", Model: "v1"},
		}),
	}, deps)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	snapshot := session.SessionSnapshot{SessionID: "session-1", AccountID: "account-1", StartOperationID: "operation-1", TraceID: "trace-1"}
	if err := manager.Start(t.Context(), snapshot); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := manager.Activate(t.Context(), snapshot.SessionID, snapshot.StartOperationID); err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	t.Cleanup(func() { _ = manager.Stop(context.Background(), snapshot.SessionID) })

	err = manager.PlayFallback(t.Context(), realtimev1.FallbackPlaybackRequest{
		OperationID: "fallback-1", SessionID: "session-1", TurnID: "turn-1", TargetLanguage: "zh-CN",
		TranslatedText: "fallback text", LanguageConfigVersion: 3, TraceID: "trace-fallback",
	})
	if err != nil {
		t.Fatalf("PlayFallback() error = %v", err)
	}
	if len(audioSink.Chunks()) != 1 || len(usageSink.Facts()) != 1 || usageSink.Facts()[0].ServiceType != "tts" {
		t.Fatalf("fallback sinks = audio %d, usage %#v", len(audioSink.Chunks()), usageSink.Facts())
	}
}

func TestManagerPlayFallbackRejectsCanceledOrUnknownSession(t *testing.T) {
	request := realtimev1.FallbackPlaybackRequest{
		OperationID: "fallback-1", SessionID: "session-1", TurnID: "turn-1", TargetLanguage: "zh-CN",
		TranslatedText: "fallback text", LanguageConfigVersion: 3, TraceID: "trace-fallback",
	}
	var nilManager *Manager
	if err := nilManager.PlayFallback(t.Context(), request); !errors.Is(err, ErrDependencyRequired) {
		t.Fatalf("nil manager PlayFallback() error = %v, want dependency error", err)
	} else if !hasFallbackPlaybackNotStarted(err) {
		t.Fatalf("nil manager PlayFallback() error = %v, want not-started marker", err)
	}
	manager, _ := newOwnershipTestManager(t)
	if err := manager.PlayFallback(t.Context(), request); !errors.Is(err, session.ErrRuntimeNotFound) {
		t.Fatalf("unknown session PlayFallback() error = %v, want runtime not found", err)
	} else if !hasFallbackPlaybackNotStarted(err) {
		t.Fatalf("unknown session PlayFallback() error = %v, want not-started marker", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := manager.PlayFallback(ctx, request); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled PlayFallback() error = %v, want context canceled", err)
	} else if !hasFallbackPlaybackNotStarted(err) {
		t.Fatalf("canceled PlayFallback() error = %v, want not-started marker", err)
	}
}

func hasFallbackPlaybackNotStarted(err error) bool {
	type fallbackPlaybackNotStarted interface {
		FallbackPlaybackNotStarted()
	}
	var marker fallbackPlaybackNotStarted
	return errors.As(err, &marker)
}

func TestManagerStartRequiresOperationID(t *testing.T) {
	manager, _ := newOwnershipTestManager(t)
	snapshot := session.SessionSnapshot{SessionID: "session-1", AccountID: "account-1", TraceID: "trace-1"}

	if err := manager.Start(context.Background(), snapshot); !errors.Is(err, session.ErrStartOperationIDRequired) {
		t.Fatalf("Start() error = %v, want ErrStartOperationIDRequired", err)
	}
}

func TestManagerRejectsForeignOperationWhilePipelineOwned(t *testing.T) {
	manager, opens := newOwnershipTestManager(t)
	snapshot := session.SessionSnapshot{
		SessionID: "session-1", AccountID: "account-1", StartOperationID: "operation-1", TraceID: "trace-1",
	}
	if err := manager.Start(context.Background(), snapshot); err != nil {
		t.Fatalf("first Start() error = %v", err)
	}
	foreign := snapshot
	foreign.StartOperationID = "operation-2"
	if err := manager.Start(context.Background(), foreign); !errors.Is(err, session.ErrRuntimeOperationConflict) {
		t.Fatalf("foreign Start() error = %v, want ErrRuntimeOperationConflict", err)
	}
	if *opens != 1 {
		t.Fatalf("source open calls = %d, want 1", *opens)
	}
	if err := manager.Stop(context.Background(), snapshot.SessionID); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
}

func TestManagerAllowsNewOperationAfterSuccessfulStop(t *testing.T) {
	manager, opens := newOwnershipTestManager(t)
	snapshot := session.SessionSnapshot{
		SessionID: "session-1", AccountID: "account-1", StartOperationID: "operation-1", TraceID: "trace-1",
	}
	if err := manager.Start(context.Background(), snapshot); err != nil {
		t.Fatalf("first Start() error = %v", err)
	}
	if err := manager.Stop(context.Background(), snapshot.SessionID); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	snapshot.StartOperationID = "operation-2"
	if err := manager.Start(context.Background(), snapshot); err != nil {
		t.Fatalf("second Start() error = %v", err)
	}
	if *opens != 2 {
		t.Fatalf("source open calls = %d, want 2", *opens)
	}
	if err := manager.Stop(context.Background(), snapshot.SessionID); err != nil {
		t.Fatalf("second Stop() error = %v", err)
	}
}

func TestManagerClosesPreparedInputWhenActivationIsCanceled(t *testing.T) {
	source := &fakeFrameSource{waitForClose: true}
	manager, err := NewManager(config.ProviderConfig{}, config.Providers{
		ASR:         asr.NewFakeProvider(asr.FakeProviderConfig{Final: asr.FinalResult{Text: "x", SourceLanguage: "zh-CN", Provider: "asr", Model: "v1"}}),
		Translation: &translate.FakeProvider{Result: translate.Result{Text: "y", Provider: "llm", Model: "qwen3.6-flash"}},
		TTS:         tts.NewFakeProvider(tts.FakeProviderConfig{Result: tts.Result{Provider: "tts", Model: "v1"}}),
	}, testDependencies(source, &fakeLanguageReader{snapshot: activeConfig("session-1")}))
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	snapshot := session.SessionSnapshot{SessionID: "session-1", AccountID: "account-1", StartOperationID: "operation-1", TraceID: "trace-1"}
	if err := manager.Start(context.Background(), snapshot); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := manager.Activate(ctx, snapshot.SessionID, snapshot.StartOperationID); !errors.Is(err, context.Canceled) {
		t.Fatalf("Activate() error = %v, want context canceled", err)
	}
	if err := manager.Stop(context.Background(), snapshot.SessionID); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if source.CloseCalls() != 1 {
		t.Fatalf("source close calls = %d, want 1", source.CloseCalls())
	}
}

func TestManagerReportsTerminalFailureAndAllowsRetry(t *testing.T) {
	base := time.Unix(1700000000, 0)
	closeErr := errors.New("close audio input")
	first := &fakeFrameSource{frames: []audio.Frame{
		mustFrame(t, []byte{1, 0}, base),
		mustFrame(t, []byte{0, 0}, base.Add(100*time.Millisecond)),
	}, closeErrors: []error{closeErr, closeErr, nil}}
	second := &fakeFrameSource{}
	reporter := &recordingRuntimeReporter{
		failureCalled:   make(chan struct{}),
		failureReturned: make(chan struct{}),
		failureRelease:  make(chan struct{}),
	}
	openCalls := 0
	deps := testDependencies(first, &fakeLanguageReader{snapshot: activeConfig("session-1")})
	var logs bytes.Buffer
	deps.Logger = slog.New(slog.NewJSONHandler(&logs, nil))
	deps.Runtime = reporter
	deps.FrameSources = FrameSourceFactoryFunc(func(context.Context, session.SessionSnapshot) (AudioInput, error) {
		openCalls++
		if openCalls == 1 {
			return AudioInput{Source: first, SourceLanguage: "zh-CN"}, nil
		}
		return AudioInput{Source: second, SourceLanguage: "zh-CN"}, nil
	})
	manager, err := NewManager(config.ProviderConfig{}, config.Providers{
		ASR:         asr.NewFakeProvider(asr.FakeProviderConfig{StartErr: errors.New("ASR unavailable")}),
		Translation: &translate.FakeProvider{Result: translate.Result{Text: "y", Provider: "llm", Model: "qwen3.6-flash"}},
		TTS:         tts.NewFakeProvider(tts.FakeProviderConfig{Result: tts.Result{Provider: "tts", Model: "v1"}}),
	}, deps)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	snapshot := session.SessionSnapshot{SessionID: "session-1", AccountID: "account-1", StartOperationID: "operation-1", TraceID: "trace-1"}
	if err := manager.Start(context.Background(), snapshot); err != nil {
		t.Fatalf("first Start() error = %v", err)
	}
	if err := manager.Activate(context.Background(), snapshot.SessionID, snapshot.StartOperationID); err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	select {
	case <-reporter.failureCalled:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for RuntimeFailed report")
	}
	logOutput := logs.String()
	for _, want := range []string{"realtime pipeline worker failed", "session-1", "operation-1", "trace-1", "ASR unavailable"} {
		if !strings.Contains(logOutput, want) {
			t.Fatalf("terminal failure log %q does not contain %q", logOutput, want)
		}
	}
	if !manager.PipelineActive(snapshot.SessionID) {
		t.Fatal("terminal worker was reported inactive before failure settlement")
	}
	retryDone := make(chan error, 1)
	go func() { retryDone <- manager.Start(context.Background(), snapshot) }()
	select {
	case err := <-retryDone:
		t.Fatalf("retry Start() returned before terminal entry settled: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(reporter.failureRelease)
	select {
	case <-reporter.failureReturned:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for failure report completion")
	}
	deadline := time.Now().Add(time.Second)
	for {
		manager.mu.Lock()
		item := manager.entries[snapshot.SessionID]
		retained := item != nil && item.terminal && item.finished
		manager.mu.Unlock()
		if retained {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("terminal entry was removed despite source close failure")
		}
		time.Sleep(time.Millisecond)
	}
	if err := <-retryDone; !errors.Is(err, closeErr) {
		t.Fatalf("retry Start() error = %v, want source close error", err)
	}
	if err := manager.Start(context.Background(), snapshot); err != nil {
		t.Fatalf("second retry Start() error = %v", err)
	}
	if calls := reporter.FailureCalls(); calls != 1 {
		t.Fatalf("failure report calls = %d, want 1", calls)
	}
	if openCalls != 2 {
		t.Fatalf("source open calls after retry = %d, want 2", openCalls)
	}
	if err := manager.Stop(context.Background(), snapshot.SessionID); err != nil {
		t.Fatalf("Stop() after retry = %v", err)
	}
}

func TestManagerStopCancellationDoesNotReportRuntimeFailure(t *testing.T) {
	source := &fakeFrameSource{waitForClose: true}
	reporter := &recordingRuntimeReporter{}
	deps := testDependencies(source, &fakeLanguageReader{snapshot: activeConfig("session-1")})
	deps.Runtime = reporter
	manager, err := NewManager(config.ProviderConfig{}, config.Providers{
		ASR:         asr.NewFakeProvider(asr.FakeProviderConfig{Final: asr.FinalResult{Text: "x", SourceLanguage: "zh-CN", Provider: "asr", Model: "v1"}}),
		Translation: &translate.FakeProvider{Result: translate.Result{Text: "y", Provider: "llm", Model: "qwen3.6-flash"}},
		TTS:         tts.NewFakeProvider(tts.FakeProviderConfig{Result: tts.Result{Provider: "tts", Model: "v1"}}),
	}, deps)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	snapshot := session.SessionSnapshot{SessionID: "session-1", AccountID: "account-1", StartOperationID: "operation-1", TraceID: "trace-1"}
	if err := manager.Start(context.Background(), snapshot); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := manager.Activate(context.Background(), snapshot.SessionID, snapshot.StartOperationID); err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	if err := manager.Stop(context.Background(), snapshot.SessionID); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if calls := reporter.FailureCalls(); calls != 0 {
		t.Fatalf("failure report calls = %d, want 0", calls)
	}
}

func TestManagerReportsCleanEOFAsRetryableTermination(t *testing.T) {
	reporter := &recordingRuntimeReporter{
		failureCalled:   make(chan struct{}),
		failureReturned: make(chan struct{}),
	}
	lifecycle := &atomicLifecycleObserver{}
	manager, err := NewManager(config.ProviderConfig{}, config.Providers{
		ASR:         asr.NewFakeProvider(asr.FakeProviderConfig{Final: asr.FinalResult{Text: "x", SourceLanguage: "zh-CN", Provider: "asr", Model: "v1"}}),
		Translation: &translate.FakeProvider{Result: translate.Result{Text: "y", Provider: "llm", Model: "qwen3.6-flash"}},
		TTS:         tts.NewFakeProvider(tts.FakeProviderConfig{Result: tts.Result{Provider: "tts", Model: "v1"}}),
	}, func() Dependencies {
		deps := testDependencies(&fakeFrameSource{}, &fakeLanguageReader{snapshot: activeConfig("session-1")})
		deps.Runtime = reporter
		deps.Lifecycle = lifecycle
		return deps
	}())
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	snapshot := session.SessionSnapshot{SessionID: "session-1", AccountID: "account-1", StartOperationID: "operation-1", TraceID: "trace-1"}
	if err := manager.Start(context.Background(), snapshot); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := manager.Activate(context.Background(), snapshot.SessionID, snapshot.StartOperationID); err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	select {
	case <-reporter.failureCalled:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for clean EOF termination report")
	}
	select {
	case <-reporter.failureReturned:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for clean EOF report completion")
	}
	if calls := reporter.FailureCalls(); calls != 1 {
		t.Fatalf("failure report calls = %d, want 1", calls)
	}
	deadline := time.Now().Add(time.Second)
	for {
		manager.mu.Lock()
		_, exists := manager.entries[snapshot.SessionID]
		manager.mu.Unlock()
		if !exists {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("clean EOF entry was not removed")
		}
		time.Sleep(time.Millisecond)
	}
	if manager.PipelineActive(snapshot.SessionID) {
		t.Fatal("clean EOF left an active pipeline")
	}
	if started, stopped := lifecycle.values(); started != 1 || stopped != 1 {
		t.Fatalf("lifecycle counters = %d/%d, want 1/1", started, stopped)
	}
}

func TestManagerRetainsFinishedEntryWhenFailureReportFails(t *testing.T) {
	reporter := &recordingRuntimeReporter{
		failureErr:      errors.New("runtime repository unavailable"),
		failureCalled:   make(chan struct{}),
		failureReturned: make(chan struct{}),
	}
	first := &fakeFrameSource{frames: []audio.Frame{
		mustFrame(t, []byte{1, 0}, time.Unix(1700000000, 0)),
		mustFrame(t, []byte{0, 0}, time.Unix(1700000000, 100000000)),
	}}
	second := &fakeFrameSource{}
	openCalls := 0
	deps := testDependencies(first, &fakeLanguageReader{snapshot: activeConfig("session-1")})
	deps.Runtime = reporter
	lifecycle := &atomicLifecycleObserver{}
	deps.Lifecycle = lifecycle
	deps.FrameSources = FrameSourceFactoryFunc(func(context.Context, session.SessionSnapshot) (AudioInput, error) {
		openCalls++
		if openCalls == 1 {
			return AudioInput{Source: first, SourceLanguage: "zh-CN"}, nil
		}
		return AudioInput{Source: second, SourceLanguage: "zh-CN"}, nil
	})
	manager, err := NewManager(config.ProviderConfig{}, config.Providers{
		ASR:         asr.NewFakeProvider(asr.FakeProviderConfig{StartErr: errors.New("ASR unavailable")}),
		Translation: &translate.FakeProvider{Result: translate.Result{Text: "y", Provider: "llm", Model: "qwen3.6-flash"}},
		TTS:         tts.NewFakeProvider(tts.FakeProviderConfig{Result: tts.Result{Provider: "tts", Model: "v1"}}),
	}, deps)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	snapshot := session.SessionSnapshot{SessionID: "session-1", AccountID: "account-1", StartOperationID: "operation-1", TraceID: "trace-1"}
	if err := manager.Start(context.Background(), snapshot); err != nil {
		t.Fatalf("first Start() error = %v", err)
	}
	if err := manager.Activate(context.Background(), snapshot.SessionID, snapshot.StartOperationID); err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	select {
	case <-reporter.failureReturned:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for failed report")
	}
	deadline := time.Now().Add(time.Second)
	for {
		manager.mu.Lock()
		item := manager.entries[snapshot.SessionID]
		retained := item != nil && item.terminal && item.finished
		manager.mu.Unlock()
		if retained {
			break
		}
		if item == nil || time.Now().After(deadline) {
			t.Fatal("finished entry was removed after failure report error")
		}
		time.Sleep(time.Millisecond)
	}
	if err := manager.Start(context.Background(), snapshot); err != nil {
		t.Fatalf("retry Start() error = %v", err)
	}
	if started, stopped := lifecycle.values(); started != 2 || stopped != 1 {
		t.Fatalf("lifecycle counters after restart = %d/%d, want 2/1", started, stopped)
	}
	if openCalls != 2 {
		t.Fatalf("source open calls after retry = %d, want 2", openCalls)
	}
	if err := manager.Stop(context.Background(), snapshot.SessionID); err != nil {
		t.Fatalf("Stop() after retry = %v", err)
	}
	if started, stopped := lifecycle.values(); started != 2 || stopped != 2 {
		t.Fatalf("lifecycle counters after stop = %d/%d, want 2/2", started, stopped)
	}
}

func TestManagerStartRetriesRetainedTerminalSourceClose(t *testing.T) {
	closeErr := errors.New("close audio input")
	reporter := &recordingRuntimeReporter{
		failureErr:      errors.New("runtime repository unavailable"),
		failureReturned: make(chan struct{}),
	}
	first := &fakeFrameSource{
		frames: []audio.Frame{
			mustFrame(t, []byte{1, 0}, time.Unix(1700000000, 0)),
			mustFrame(t, []byte{0, 0}, time.Unix(1700000000, 100000000)),
		},
		closeErrors: []error{closeErr, closeErr, nil},
	}
	second := &fakeFrameSource{}
	openCalls := 0
	deps := testDependencies(first, &fakeLanguageReader{snapshot: activeConfig("session-1")})
	deps.Runtime = reporter
	deps.FrameSources = FrameSourceFactoryFunc(func(context.Context, session.SessionSnapshot) (AudioInput, error) {
		openCalls++
		if openCalls == 1 {
			return AudioInput{Source: first, SourceLanguage: "zh-CN"}, nil
		}
		return AudioInput{Source: second, SourceLanguage: "zh-CN"}, nil
	})
	manager, err := NewManager(config.ProviderConfig{}, config.Providers{
		ASR:         asr.NewFakeProvider(asr.FakeProviderConfig{StartErr: errors.New("ASR unavailable")}),
		Translation: &translate.FakeProvider{Result: translate.Result{Text: "y", Provider: "llm", Model: "qwen3.6-flash"}},
		TTS:         tts.NewFakeProvider(tts.FakeProviderConfig{Result: tts.Result{Provider: "tts", Model: "v1"}}),
	}, deps)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	snapshot := session.SessionSnapshot{SessionID: "session-1", AccountID: "account-1", StartOperationID: "operation-1", TraceID: "trace-1"}
	if err := manager.Start(context.Background(), snapshot); err != nil {
		t.Fatalf("first Start() error = %v", err)
	}
	if err := manager.Activate(context.Background(), snapshot.SessionID, snapshot.StartOperationID); err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	select {
	case <-reporter.failureReturned:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for failed report")
	}
	deadline := time.Now().Add(time.Second)
	for {
		manager.mu.Lock()
		item := manager.entries[snapshot.SessionID]
		settled := item != nil && item.terminal && item.finished
		manager.mu.Unlock()
		if settled {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("finished terminal entry was not retained")
		}
		time.Sleep(time.Millisecond)
	}
	if err := manager.Start(context.Background(), snapshot); !errors.Is(err, closeErr) {
		t.Fatalf("retry Start() error = %v, want source close error", err)
	}
	if openCalls != 1 {
		t.Fatalf("source open calls after failed cleanup = %d, want 1", openCalls)
	}
	if err := manager.Start(context.Background(), snapshot); err != nil {
		t.Fatalf("second retry Start() error = %v", err)
	}
	if openCalls != 2 {
		t.Fatalf("source open calls after cleanup retry = %d, want 2", openCalls)
	}
	if err := manager.Stop(context.Background(), snapshot.SessionID); err != nil {
		t.Fatalf("Stop() after retry = %v", err)
	}
}

func TestManagerStopIgnoresSettledWorkerError(t *testing.T) {
	reporter := &recordingRuntimeReporter{
		failureErr:      errors.New("runtime repository unavailable"),
		failureCalled:   make(chan struct{}),
		failureReturned: make(chan struct{}),
	}
	source := &fakeFrameSource{frames: []audio.Frame{
		mustFrame(t, []byte{1, 0}, time.Unix(1700000000, 0)),
		mustFrame(t, []byte{0, 0}, time.Unix(1700000000, 100000000)),
	}}
	deps := testDependencies(source, &fakeLanguageReader{snapshot: activeConfig("session-1")})
	deps.Runtime = reporter
	manager, err := NewManager(config.ProviderConfig{}, config.Providers{
		ASR:         asr.NewFakeProvider(asr.FakeProviderConfig{StartErr: errors.New("ASR unavailable")}),
		Translation: &translate.FakeProvider{Result: translate.Result{Text: "y", Provider: "llm", Model: "qwen3.6-flash"}},
		TTS:         tts.NewFakeProvider(tts.FakeProviderConfig{Result: tts.Result{Provider: "tts", Model: "v1"}}),
	}, deps)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	snapshot := session.SessionSnapshot{SessionID: "session-1", AccountID: "account-1", StartOperationID: "operation-1", TraceID: "trace-1"}
	if err := manager.Start(context.Background(), snapshot); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := manager.Activate(context.Background(), snapshot.SessionID, snapshot.StartOperationID); err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	select {
	case <-reporter.failureReturned:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for failed report")
	}
	deadline := time.Now().Add(time.Second)
	for {
		manager.mu.Lock()
		item := manager.entries[snapshot.SessionID]
		settled := item != nil && item.terminal && item.finished
		manager.mu.Unlock()
		if settled {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("finished terminal entry was not retained")
		}
		time.Sleep(time.Millisecond)
	}
	if err := manager.Stop(context.Background(), snapshot.SessionID); err != nil {
		t.Fatalf("Stop() returned settled worker error: %v", err)
	}
	manager.mu.Lock()
	_, retained := manager.entries[snapshot.SessionID]
	manager.mu.Unlock()
	if retained {
		t.Fatal("Stop() did not remove settled entry")
	}
	if source.CloseCalls() != 1 {
		t.Fatalf("source close calls = %d, want 1", source.CloseCalls())
	}
}

func TestManagerStopRetriesSettledSourceCloseError(t *testing.T) {
	closeErr := errors.New("close audio input")
	reporter := &recordingRuntimeReporter{
		failureErr:      errors.New("runtime repository unavailable"),
		failureReturned: make(chan struct{}),
	}
	source := &fakeFrameSource{
		frames: []audio.Frame{
			mustFrame(t, []byte{1, 0}, time.Unix(1700000000, 0)),
			mustFrame(t, []byte{0, 0}, time.Unix(1700000000, 100000000)),
		},
		closeErrors: []error{closeErr, closeErr, nil},
	}
	deps := testDependencies(source, &fakeLanguageReader{snapshot: activeConfig("session-1")})
	deps.Runtime = reporter
	manager, err := NewManager(config.ProviderConfig{}, config.Providers{
		ASR:         asr.NewFakeProvider(asr.FakeProviderConfig{StartErr: errors.New("ASR unavailable")}),
		Translation: &translate.FakeProvider{Result: translate.Result{Text: "y", Provider: "llm", Model: "qwen3.6-flash"}},
		TTS:         tts.NewFakeProvider(tts.FakeProviderConfig{Result: tts.Result{Provider: "tts", Model: "v1"}}),
	}, deps)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	snapshot := session.SessionSnapshot{SessionID: "session-1", AccountID: "account-1", StartOperationID: "operation-1", TraceID: "trace-1"}
	if err := manager.Start(context.Background(), snapshot); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := manager.Activate(context.Background(), snapshot.SessionID, snapshot.StartOperationID); err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	select {
	case <-reporter.failureReturned:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for failed report")
	}
	deadline := time.Now().Add(time.Second)
	for {
		manager.mu.Lock()
		item := manager.entries[snapshot.SessionID]
		settled := item != nil && item.terminal && item.finished
		manager.mu.Unlock()
		if settled {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("finished terminal entry was not retained")
		}
		time.Sleep(time.Millisecond)
	}
	if err := manager.Stop(context.Background(), snapshot.SessionID); err != closeErr {
		t.Fatalf("Stop() error = %v, want source close error", err)
	}
	manager.mu.Lock()
	_, retained := manager.entries[snapshot.SessionID]
	manager.mu.Unlock()
	if !retained {
		t.Fatal("Stop() removed entry after source close failure")
	}
	if source.CloseCalls() != 2 {
		t.Fatalf("source close calls = %d, want 2", source.CloseCalls())
	}
	if err := manager.Stop(context.Background(), snapshot.SessionID); err != nil {
		t.Fatalf("retry Stop() error = %v", err)
	}
	manager.mu.Lock()
	_, retained = manager.entries[snapshot.SessionID]
	manager.mu.Unlock()
	if retained {
		t.Fatal("retry Stop() retained entry after source close succeeded")
	}
	if source.CloseCalls() != 3 {
		t.Fatalf("source close calls after retry = %d, want 3", source.CloseCalls())
	}
}

func TestManagerStopCancelsBlockedFailureReport(t *testing.T) {
	base := time.Unix(1700000000, 0)
	source := &fakeFrameSource{frames: []audio.Frame{
		mustFrame(t, []byte{1, 0}, base),
		mustFrame(t, []byte{0, 0}, base.Add(100*time.Millisecond)),
	}}
	reporter := &recordingRuntimeReporter{
		failureCalled:   make(chan struct{}),
		failureReturned: make(chan struct{}),
		failureRelease:  make(chan struct{}),
	}
	deps := testDependencies(source, &fakeLanguageReader{snapshot: activeConfig("session-1")})
	deps.Runtime = reporter
	manager, err := NewManager(config.ProviderConfig{}, config.Providers{
		ASR:         asr.NewFakeProvider(asr.FakeProviderConfig{StartErr: errors.New("ASR unavailable")}),
		Translation: &translate.FakeProvider{Result: translate.Result{Text: "y", Provider: "llm", Model: "qwen3.6-flash"}},
		TTS:         tts.NewFakeProvider(tts.FakeProviderConfig{Result: tts.Result{Provider: "tts", Model: "v1"}}),
	}, deps)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	snapshot := session.SessionSnapshot{SessionID: "session-1", AccountID: "account-1", StartOperationID: "operation-1", TraceID: "trace-1"}
	if err := manager.Start(context.Background(), snapshot); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := manager.Activate(context.Background(), snapshot.SessionID, snapshot.StartOperationID); err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	select {
	case <-reporter.failureCalled:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for blocked failure report")
	}
	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := manager.Stop(stopCtx, snapshot.SessionID); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	select {
	case <-reporter.failureReturned:
	case <-time.After(time.Second):
		t.Fatal("failure reporter did not observe Stop cancellation")
	}
}

func TestManagerStartAllowsEmptySourceLanguageForAutoDetect(t *testing.T) {
	deps := testDependencies(&fakeFrameSource{}, &fakeLanguageReader{snapshot: activeConfig("session-1")})
	deps.FrameSources = FrameSourceFactoryFunc(func(context.Context, session.SessionSnapshot) (AudioInput, error) {
		return AudioInput{Source: &fakeFrameSource{}, SourceLanguage: ""}, nil
	})
	manager, err := NewManager(config.ProviderConfig{}, config.Providers{
		ASR:         asr.NewFakeProvider(asr.FakeProviderConfig{Final: asr.FinalResult{Text: "x", SourceLanguage: "en-US", Provider: "asr", Model: "v1"}}),
		Translation: &translate.FakeProvider{Result: translate.Result{Text: "y", Provider: "llm", Model: "qwen3.6-flash"}},
		TTS:         tts.NewFakeProvider(tts.FakeProviderConfig{Result: tts.Result{Provider: "tts", Model: "v1"}}),
	}, deps)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	if err := manager.Start(context.Background(), session.SessionSnapshot{
		SessionID: "session-1", AccountID: "account-1", StartOperationID: "operation-1", TraceID: "trace-1",
	}); err != nil {
		t.Fatalf("Start() error = %v, empty SourceLanguage must be allowed for ASR auto-detect", err)
	}
	if err := manager.Stop(context.Background(), "session-1"); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
}

func TestManagerDoesNotBlockOtherSessionsWhileOpeningInput(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	deps := testDependencies(&fakeFrameSource{}, &fakeLanguageReader{snapshot: activeConfig("session-a")})
	deps.FrameSources = FrameSourceFactoryFunc(func(ctx context.Context, snapshot session.SessionSnapshot) (AudioInput, error) {
		if snapshot.SessionID == "session-a" {
			close(entered)
			select {
			case <-ctx.Done():
				return AudioInput{}, ctx.Err()
			case <-release:
			}
		}
		return AudioInput{Source: &fakeFrameSource{}, SourceLanguage: "zh-CN"}, nil
	})
	manager, err := NewManager(config.ProviderConfig{}, config.Providers{
		ASR:         asr.NewFakeProvider(asr.FakeProviderConfig{Final: asr.FinalResult{Text: "x", SourceLanguage: "zh-CN", Provider: "asr", Model: "v1"}}),
		Translation: &translate.FakeProvider{Result: translate.Result{Text: "y", Provider: "llm", Model: "qwen3.6-flash"}},
		TTS:         tts.NewFakeProvider(tts.FakeProviderConfig{Result: tts.Result{Provider: "tts", Model: "v1"}}),
	}, deps)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	startA := make(chan error, 1)
	go func() {
		startA <- manager.Start(context.Background(), session.SessionSnapshot{
			SessionID: "session-a", AccountID: "account-a", StartOperationID: "operation-a", TraceID: "trace-a",
		})
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("session-a did not enter source factory")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := manager.Start(ctx, session.SessionSnapshot{SessionID: "session-b", AccountID: "account-b", StartOperationID: "operation-b", TraceID: "trace-b"}); err != nil {
		t.Fatalf("session-b Start() blocked behind session-a: %v", err)
	}
	close(release)
	if err := <-startA; err != nil {
		t.Fatalf("session-a Start() error = %v", err)
	}
	if err := manager.Stop(context.Background(), "session-a"); err != nil {
		t.Fatalf("session-a Stop() error = %v", err)
	}
	if err := manager.Stop(context.Background(), "session-b"); err != nil {
		t.Fatalf("session-b Stop() error = %v", err)
	}
}

func TestManagerStopTimeoutCanBeRetriedAfterSourceUnblocks(t *testing.T) {
	source := &stubbornFrameSource{entered: make(chan struct{}), release: make(chan struct{})}
	manager, err := NewManager(config.ProviderConfig{}, config.Providers{
		ASR:         asr.NewFakeProvider(asr.FakeProviderConfig{Final: asr.FinalResult{Text: "x", SourceLanguage: "zh-CN", Provider: "asr", Model: "v1"}}),
		Translation: &translate.FakeProvider{Result: translate.Result{Text: "y", Provider: "llm", Model: "qwen3.6-flash"}},
		TTS:         tts.NewFakeProvider(tts.FakeProviderConfig{Result: tts.Result{Provider: "tts", Model: "v1"}}),
	}, testDependencies(source, &fakeLanguageReader{snapshot: activeConfig("session-1")}))
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	snapshot := session.SessionSnapshot{SessionID: "session-1", AccountID: "account-1", StartOperationID: "operation-1", TraceID: "trace-1"}
	if err := manager.Start(context.Background(), snapshot); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := manager.Activate(context.Background(), snapshot.SessionID, snapshot.StartOperationID); err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	select {
	case <-source.entered:
	case <-time.After(time.Second):
		t.Fatal("source ReadFrame() was not entered")
	}
	stopCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := manager.Stop(stopCtx, snapshot.SessionID); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("first Stop() error = %v, want deadline exceeded", err)
	}
	close(source.release)
	if err := manager.Stop(context.Background(), snapshot.SessionID); err != nil {
		t.Fatalf("retry Stop() error = %v", err)
	}
	if source.CloseCalls() != 1 {
		t.Fatalf("source close calls = %d, want 1", source.CloseCalls())
	}
}

func TestManagerStopTimeoutBoundsBlockingSourceClose(t *testing.T) {
	source := &blockingCloseSource{
		readEntered: make(chan struct{}), closeEntered: make(chan struct{}), closeRelease: make(chan struct{}),
	}
	manager, err := NewManager(config.ProviderConfig{}, config.Providers{
		ASR:         asr.NewFakeProvider(asr.FakeProviderConfig{Final: asr.FinalResult{Text: "x", SourceLanguage: "zh-CN", Provider: "asr", Model: "v1"}}),
		Translation: &translate.FakeProvider{Result: translate.Result{Text: "y", Provider: "llm", Model: "qwen3.6-flash"}},
		TTS:         tts.NewFakeProvider(tts.FakeProviderConfig{Result: tts.Result{Provider: "tts", Model: "v1"}}),
	}, testDependencies(source, &fakeLanguageReader{snapshot: activeConfig("session-1")}))
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	snapshot := session.SessionSnapshot{SessionID: "session-1", AccountID: "account-1", StartOperationID: "operation-1", TraceID: "trace-1"}
	if err := manager.Start(context.Background(), snapshot); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := manager.Activate(context.Background(), snapshot.SessionID, snapshot.StartOperationID); err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	select {
	case <-source.readEntered:
	case <-time.After(time.Second):
		t.Fatal("source ReadFrame() was not entered")
	}
	stopCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := manager.Stop(stopCtx, snapshot.SessionID); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("first Stop() error = %v, want deadline exceeded", err)
	}
	select {
	case <-source.closeEntered:
	case <-time.After(time.Second):
		t.Fatal("source Close() was not entered")
	}
	close(source.closeRelease)
	if err := manager.Stop(context.Background(), snapshot.SessionID); err != nil {
		t.Fatalf("retry Stop() error = %v", err)
	}
	if source.CloseCalls() != 1 {
		t.Fatalf("source close calls = %d, want 1", source.CloseCalls())
	}
}

func testDependencies(source segment.FrameSource, languages session.LanguageConfigReader) Dependencies {
	return Dependencies{
		NewCommandInterpreter: func([]command.CapabilityDescriptor) (command.Interpreter, error) {
			return command.LegacyInterpreter{}, nil
		},
		FrameSources: FrameSourceFactoryFunc(func(context.Context, session.SessionSnapshot) (AudioInput, error) {
			return AudioInput{Source: source, SourceLanguage: "zh-CN"}, nil
		}),
		NewSegmenter: func() (*vad.Segmenter, error) {
			return vad.NewSegmenter(speechClassifier{}, vad.Options{SilenceAfter: 50 * time.Millisecond, MaxDuration: time.Second})
		},
		Languages: languages, FinalTurns: &recordingFinalSink{}, ModeChanges: &recordingModeChangedSink{}, Usage: &recordingUsageSink{},
		Audio: &recordingAudioSink{}, Runtime: &recordingRuntimeReporter{},
	}
}

type atomicLifecycleObserver struct {
	started atomic.Uint64
	stopped atomic.Uint64
}

func (o *atomicLifecycleObserver) RecordRuntimeStarted() { o.started.Add(1) }
func (o *atomicLifecycleObserver) RecordRuntimeStopped() { o.stopped.Add(1) }
func (o *atomicLifecycleObserver) values() (uint64, uint64) {
	return o.started.Load(), o.stopped.Load()
}

func newOwnershipTestManager(t *testing.T) (*Manager, *int) {
	t.Helper()
	opens := 0
	deps := testDependencies(&fakeFrameSource{}, &fakeLanguageReader{snapshot: activeConfig("session-1")})
	deps.FrameSources = FrameSourceFactoryFunc(func(context.Context, session.SessionSnapshot) (AudioInput, error) {
		opens++
		return AudioInput{Source: &fakeFrameSource{}, SourceLanguage: "zh-CN"}, nil
	})
	manager, err := NewManager(config.ProviderConfig{}, config.Providers{
		ASR:         asr.NewFakeProvider(asr.FakeProviderConfig{}),
		Translation: &translate.FakeProvider{},
		TTS:         tts.NewFakeProvider(tts.FakeProviderConfig{}),
	}, deps)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	return manager, &opens
}

func activeConfig(sessionID string) session.LanguageConfigSnapshot {
	return session.LanguageConfigSnapshot{SessionID: sessionID, Version: 1, Status: "active", LanguagePairs: []session.LanguagePair{{Source: "zh-CN", Target: "en-US"}}}
}

func mustFrame(t *testing.T, pcm []byte, capturedAt time.Time) audio.Frame {
	t.Helper()
	frame, err := audio.NewFrame(pcm, audio.SupportedSampleRate, capturedAt)
	if err != nil {
		t.Fatal(err)
	}
	return frame
}

type speechClassifier struct{}

func (speechClassifier) Speech(frame audio.Frame) bool { return frame.PCM[0] == 1 }

type fakeFrameSource struct {
	mu           sync.Mutex
	frames       []audio.Frame
	waitForClose bool
	index        int
	closeCalls   int
	closeErrors  []error
	closed       chan struct{}
}

type stubbornFrameSource struct {
	mu         sync.Mutex
	entered    chan struct{}
	release    chan struct{}
	enterOnce  sync.Once
	closeCalls int
}

type blockingCloseSource struct {
	mu           sync.Mutex
	readEntered  chan struct{}
	closeEntered chan struct{}
	closeRelease chan struct{}
	readOnce     sync.Once
	closeOnce    sync.Once
	closeCalls   int
}

func (s *blockingCloseSource) ReadFrame(ctx context.Context) (audio.Frame, error) {
	s.readOnce.Do(func() { close(s.readEntered) })
	<-ctx.Done()
	return audio.Frame{}, ctx.Err()
}

func (s *blockingCloseSource) Close() error {
	s.mu.Lock()
	s.closeCalls++
	s.mu.Unlock()
	s.closeOnce.Do(func() { close(s.closeEntered) })
	<-s.closeRelease
	return nil
}

func (s *blockingCloseSource) CloseCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closeCalls
}

func (s *stubbornFrameSource) ReadFrame(context.Context) (audio.Frame, error) {
	s.enterOnce.Do(func() { close(s.entered) })
	<-s.release
	return audio.Frame{}, io.EOF
}

func (s *stubbornFrameSource) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closeCalls++
	return nil
}

func (s *stubbornFrameSource) CloseCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closeCalls
}

func (s *fakeFrameSource) ReadFrame(ctx context.Context) (audio.Frame, error) {
	s.mu.Lock()
	if s.closed == nil {
		s.closed = make(chan struct{})
	}
	closed := s.closed
	if s.index < len(s.frames) {
		frame := s.frames[s.index].Clone()
		s.index++
		s.mu.Unlock()
		return frame, nil
	}
	s.mu.Unlock()
	if !s.waitForClose {
		return audio.Frame{}, io.EOF
	}
	select {
	case <-ctx.Done():
		return audio.Frame{}, ctx.Err()
	case <-closed:
		return audio.Frame{}, io.EOF
	}
}

func (s *fakeFrameSource) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	call := s.closeCalls
	s.closeCalls++
	if s.closed == nil {
		s.closed = make(chan struct{})
	}
	select {
	case <-s.closed:
	default:
		close(s.closed)
	}
	if call < len(s.closeErrors) {
		return s.closeErrors[call]
	}
	return nil
}

func (s *fakeFrameSource) CloseCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closeCalls
}

type fakeLanguageReader struct {
	mu       sync.Mutex
	snapshot session.LanguageConfigSnapshot
	calls    int
}

func (r *fakeLanguageReader) GetCurrentConfig(context.Context, string) (session.LanguageConfigSnapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	return r.snapshot, nil
}

func (r *fakeLanguageReader) Calls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

type recordingFinalSink struct {
	mu     sync.Mutex
	events chan recordsv1.FinalTurnEvent
}

func (s *recordingFinalSink) Publish(_ context.Context, event recordsv1.FinalTurnEvent) error {
	s.mu.Lock()
	if s.events == nil {
		s.events = make(chan recordsv1.FinalTurnEvent, 1)
	}
	s.mu.Unlock()
	s.events <- event
	return nil
}

type recordingUsageSink struct {
	mu    sync.Mutex
	facts []pipeline.UsageFact
}

type recordingAssistantReplySink struct {
	events []realtimev1.AssistantReplyEvent
}

func (s *recordingAssistantReplySink) Publish(_ context.Context, event realtimev1.AssistantReplyEvent) error {
	s.events = append(s.events, event)
	return nil
}

type assistantReplySignalSink struct {
	events chan realtimev1.AssistantReplyEvent
}

func (s *assistantReplySignalSink) Publish(_ context.Context, event realtimev1.AssistantReplyEvent) error {
	s.events <- event
	return nil
}

func (s *recordingUsageSink) Publish(_ context.Context, fact pipeline.UsageFact) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.facts = append(s.facts, fact)
	return nil
}

func (s *recordingUsageSink) Facts() []pipeline.UsageFact {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]pipeline.UsageFact(nil), s.facts...)
}

type recordingAudioSink struct {
	mu     sync.Mutex
	chunks []pipeline.AudioChunk
}

func (s *recordingAudioSink) Publish(_ context.Context, chunk pipeline.AudioChunk) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.chunks = append(s.chunks, chunk)
	return nil
}

func (s *recordingAudioSink) Chunks() []pipeline.AudioChunk {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]pipeline.AudioChunk(nil), s.chunks...)
}

type recordingRuntimeReporter struct {
	mu              sync.Mutex
	states          []session.RuntimeState
	failureCalls    int
	failureErr      error
	failureCalled   chan struct{}
	failureReturned chan struct{}
	failureRelease  chan struct{}
	failureOnce     sync.Once
}

func (r *recordingRuntimeReporter) SetProcessingState(_ context.Context, update session.ProcessingStateUpdate) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.states = append(r.states, update.RuntimeState)
	return nil
}

func (r *recordingRuntimeReporter) SetRuntimeFailed(ctx context.Context, _ string, _ realtimev1.RuntimeErrorCode) error {
	r.mu.Lock()
	r.failureCalls++
	r.mu.Unlock()
	if r.failureCalled != nil {
		r.failureOnce.Do(func() { close(r.failureCalled) })
	}
	if r.failureRelease != nil {
		select {
		case <-r.failureRelease:
		case <-ctx.Done():
			if r.failureReturned != nil {
				close(r.failureReturned)
			}
			return ctx.Err()
		}
	}
	if r.failureReturned != nil {
		close(r.failureReturned)
	}
	return r.failureErr
}

func (r *recordingRuntimeReporter) FailureCalls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.failureCalls
}
