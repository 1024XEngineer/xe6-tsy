package runtime

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/asr"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/audio"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/config"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/pipeline"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/segment"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/session"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/translate"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/tts"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/vad"
)

func TestManagerRunsOneTurnThroughConfiguredProviders(t *testing.T) {
	base := time.Unix(1700000000, 0).UTC()
	source := &fakeFrameSource{frames: []audio.Frame{
		mustFrame(t, []byte{1, 0}, base),
		mustFrame(t, []byte{1, 0}, base.Add(20*time.Millisecond)),
		mustFrame(t, []byte{0, 0}, base.Add(100*time.Millisecond)),
	}}
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
		Languages: languages, FinalTurns: finals, Usage: usage, Audio: audioSink, Runtime: reporter,
	})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	snapshot := session.SessionSnapshot{SessionID: "session-1", AccountID: "account-1", TraceID: "trace-1", Status: "created"}
	if err := manager.Start(context.Background(), snapshot); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := manager.Start(context.Background(), snapshot); err != nil {
		t.Fatalf("idempotent Start() error = %v", err)
	}
	if openCalls != 1 {
		t.Fatalf("source open calls = %d, want 1", openCalls)
	}
	if err := manager.Activate(context.Background(), snapshot.SessionID); err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	if err := manager.Activate(context.Background(), snapshot.SessionID); err != nil {
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
	snapshot := session.SessionSnapshot{SessionID: "session-1", AccountID: "account-1", TraceID: "trace-1"}
	if err := manager.Start(context.Background(), snapshot); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := manager.Activate(ctx, snapshot.SessionID); !errors.Is(err, context.Canceled) {
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
	first := &fakeFrameSource{frames: []audio.Frame{
		mustFrame(t, []byte{1, 0}, base),
		mustFrame(t, []byte{0, 0}, base.Add(100*time.Millisecond)),
	}}
	second := &fakeFrameSource{}
	reporter := &recordingRuntimeReporter{
		failureCalled:   make(chan struct{}),
		failureReturned: make(chan struct{}),
		failureRelease:  make(chan struct{}),
	}
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
	snapshot := session.SessionSnapshot{SessionID: "session-1", AccountID: "account-1", TraceID: "trace-1"}
	if err := manager.Start(context.Background(), snapshot); err != nil {
		t.Fatalf("first Start() error = %v", err)
	}
	if err := manager.Activate(context.Background(), snapshot.SessionID); err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	select {
	case <-reporter.failureCalled:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for RuntimeFailed report")
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
	if err := <-retryDone; err != nil {
		t.Fatalf("retry Start() error = %v", err)
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
	snapshot := session.SessionSnapshot{SessionID: "session-1", AccountID: "account-1", TraceID: "trace-1"}
	if err := manager.Start(context.Background(), snapshot); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := manager.Activate(context.Background(), snapshot.SessionID); err != nil {
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
	manager, err := NewManager(config.ProviderConfig{}, config.Providers{
		ASR:         asr.NewFakeProvider(asr.FakeProviderConfig{Final: asr.FinalResult{Text: "x", SourceLanguage: "zh-CN", Provider: "asr", Model: "v1"}}),
		Translation: &translate.FakeProvider{Result: translate.Result{Text: "y", Provider: "llm", Model: "qwen3.6-flash"}},
		TTS:         tts.NewFakeProvider(tts.FakeProviderConfig{Result: tts.Result{Provider: "tts", Model: "v1"}}),
	}, func() Dependencies {
		deps := testDependencies(&fakeFrameSource{}, &fakeLanguageReader{snapshot: activeConfig("session-1")})
		deps.Runtime = reporter
		return deps
	}())
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	snapshot := session.SessionSnapshot{SessionID: "session-1", AccountID: "account-1", TraceID: "trace-1"}
	if err := manager.Start(context.Background(), snapshot); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := manager.Activate(context.Background(), snapshot.SessionID); err != nil {
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
	snapshot := session.SessionSnapshot{SessionID: "session-1", AccountID: "account-1", TraceID: "trace-1"}
	if err := manager.Start(context.Background(), snapshot); err != nil {
		t.Fatalf("first Start() error = %v", err)
	}
	if err := manager.Activate(context.Background(), snapshot.SessionID); err != nil {
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
	if openCalls != 2 {
		t.Fatalf("source open calls after retry = %d, want 2", openCalls)
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
	snapshot := session.SessionSnapshot{SessionID: "session-1", AccountID: "account-1", TraceID: "trace-1"}
	if err := manager.Start(context.Background(), snapshot); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := manager.Activate(context.Background(), snapshot.SessionID); err != nil {
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
	snapshot := session.SessionSnapshot{SessionID: "session-1", AccountID: "account-1", TraceID: "trace-1"}
	if err := manager.Start(context.Background(), snapshot); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := manager.Activate(context.Background(), snapshot.SessionID); err != nil {
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
			SessionID: "session-a", AccountID: "account-a", TraceID: "trace-a",
		})
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("session-a did not enter source factory")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := manager.Start(ctx, session.SessionSnapshot{SessionID: "session-b", AccountID: "account-b", TraceID: "trace-b"}); err != nil {
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
	snapshot := session.SessionSnapshot{SessionID: "session-1", AccountID: "account-1", TraceID: "trace-1"}
	if err := manager.Start(context.Background(), snapshot); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := manager.Activate(context.Background(), snapshot.SessionID); err != nil {
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
	snapshot := session.SessionSnapshot{SessionID: "session-1", AccountID: "account-1", TraceID: "trace-1"}
	if err := manager.Start(context.Background(), snapshot); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := manager.Activate(context.Background(), snapshot.SessionID); err != nil {
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
		FrameSources: FrameSourceFactoryFunc(func(context.Context, session.SessionSnapshot) (AudioInput, error) {
			return AudioInput{Source: source, SourceLanguage: "zh-CN"}, nil
		}),
		NewSegmenter: func() (*vad.Segmenter, error) {
			return vad.NewSegmenter(speechClassifier{}, vad.Options{SilenceAfter: 50 * time.Millisecond, MaxDuration: time.Second})
		},
		Languages: languages, FinalTurns: &recordingFinalSink{}, Usage: &recordingUsageSink{},
		Audio: &recordingAudioSink{}, Runtime: &recordingRuntimeReporter{},
	}
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
	s.closeCalls++
	if s.closed == nil {
		s.closed = make(chan struct{})
	}
	select {
	case <-s.closed:
	default:
		close(s.closed)
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

func (r *recordingRuntimeReporter) SetRuntimeFailed(ctx context.Context, _ string) error {
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
