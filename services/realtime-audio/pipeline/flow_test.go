package pipeline

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/asr"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/session"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/translate"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/tts"
)

func TestTurnProcessorRunsMockASRTranslationTTSFlow(t *testing.T) {
	asrProvider := asr.NewFakeProvider(asr.FakeProviderConfig{
		Partial: asr.Event{Text: "你"},
		Final: asr.FinalResult{
			Text: "你好", SourceLanguage: "zh-CN", Provider: "mock-asr", Model: "v1",
			AudioDuration: time.Second,
		},
	})
	translator := &translate.FakeProvider{Result: translate.Result{Text: "hello", Provider: "mock-translate", Model: "v1", InputTokens: 2, OutputTokens: 1}}
	ttsProvider := tts.NewFakeProvider(tts.FakeProviderConfig{
		Chunks: []tts.AudioChunk{{SequenceNo: 1, Data: []byte{1}}, {SequenceNo: 2, Data: []byte{2}}},
		Result: tts.Result{Provider: "mock-tts", Model: "v1", AudioDuration: 250 * time.Millisecond},
	})
	finalSink := &recordingFinalSink{}
	usageSink := &recordingUsageSink{}
	audioSink := &recordingAudioSink{}
	runtimeReporter := &recordingRuntimeReporter{}
	service := newTestPipelineService(PipelineDependencies{
		Translator: translator, TTS: ttsProvider, FinalTurns: finalSink,
		Usage: usageSink, Audio: audioSink, Runtime: runtimeReporter, VoiceID: "voice-1",
	})
	processor := NewTurnProcessor(TurnProcessorDependencies{
		ASR: asrProvider,
		Opener: newTestTurnOpener(&fakeLanguageConfigReader{snapshot: session.LanguageConfigSnapshot{
			SessionID: "session-1", Version: 3, Status: "active",
			LanguagePairs: []session.LanguagePair{{Source: "zh-CN", Target: "en-US"}},
		}}),
		Pipeline: service,
		Finals:   service,
	})

	turn, err := processor.ProcessAudio(context.Background(), TurnProcessRequest{
		SessionID: "session-1", AccountID: "account-1", TraceID: "trace-1",
		StartedAt: time.Unix(1700000000, 0).UTC(), AudioChunks: [][]byte{{1, 2}, {3}},
	})
	if err != nil {
		t.Fatalf("ProcessAudio() error = %v", err)
	}
	if turn.SequenceNo != 1 || turn.ID == "" {
		t.Fatalf("turn = %#v", turn)
	}
	requests := asrProvider.Requests()
	if len(requests) != 1 || requests[0].TurnID != turn.ID {
		t.Fatalf("ASR requests = %#v", requests)
	}
	translationRequests := translator.Requests()
	if len(translationRequests) != 1 || translationRequests[0].TurnID != turn.ID || translationRequests[0].TargetLanguage != "en-US" {
		t.Fatalf("translation requests = %#v", translationRequests)
	}
	ttsRequests := ttsProvider.Requests()
	if len(ttsRequests) != 1 || ttsRequests[0].TurnID != turn.ID {
		t.Fatalf("TTS requests = %#v", ttsRequests)
	}
	if len(finalSink.events) != 1 || finalSink.events[0].TurnID != turn.ID || finalSink.events[0].LanguageConfigVersion != 3 {
		t.Fatalf("FinalTurn events = %#v", finalSink.events)
	}
	if len(usageSink.facts) != 3 || usageSink.facts[0].ServiceType != "asr" ||
		usageSink.facts[1].ServiceType != "translation" || usageSink.facts[2].ServiceType != "tts" ||
		usageSink.facts[0].AudioDurationMS != 1000 {
		t.Fatalf("UsageFacts = %#v, want ASR, translation, TTS", usageSink.facts)
	}
	if len(audioSink.chunks) != 2 || audioSink.chunks[0].TurnID != turn.ID || audioSink.chunks[1].TurnID != turn.ID {
		t.Fatalf("audio chunks = %#v", audioSink.chunks)
	}
	for _, fact := range usageSink.facts {
		if fact.TurnID != turn.ID {
			t.Fatalf("UsageFact turn ID = %q, want %q", fact.TurnID, turn.ID)
		}
	}
	wantStates := []session.RuntimeState{session.RuntimeASRProcessing, session.RuntimeTranslating, session.RuntimeTTSProcessing, session.RuntimePlaying, session.RuntimeListening}
	if len(runtimeReporter.updates) != len(wantStates) {
		t.Fatalf("runtime updates = %#v", runtimeReporter.updates)
	}
	for index, want := range wantStates {
		if runtimeReporter.updates[index].RuntimeState != want || runtimeReporter.updates[index].SessionID != turn.SessionID {
			t.Fatalf("runtime update %d = %#v, want %q", index, runtimeReporter.updates[index], want)
		}
	}
	for index := 0; index < 4; index++ {
		if runtimeReporter.updates[index].CurrentTurnID == nil || *runtimeReporter.updates[index].CurrentTurnID != turn.ID {
			t.Fatalf("runtime update %d Turn = %#v, want %q", index, runtimeReporter.updates[index], turn.ID)
		}
	}
	wantPlaybackID := "playback_" + turn.ID
	if runtimeReporter.updates[2].CurrentPlaybackID == nil || *runtimeReporter.updates[2].CurrentPlaybackID != wantPlaybackID {
		t.Fatalf("TTS runtime update = %#v, want playback %q", runtimeReporter.updates[2], wantPlaybackID)
	}
	if runtimeReporter.updates[3].CurrentPlaybackID == nil || *runtimeReporter.updates[3].CurrentPlaybackID != wantPlaybackID {
		t.Fatalf("playing runtime update = %#v, want playback %q", runtimeReporter.updates[3], wantPlaybackID)
	}
	listening := runtimeReporter.updates[4]
	if listening.CurrentTurnID != nil || listening.CurrentPlaybackID != nil {
		t.Fatalf("listening runtime update retained active IDs: %#v", listening)
	}
}

func TestTurnProcessorDispatchesASRFinalToInjectedHandler(t *testing.T) {
	// 这条测试专门保护 TurnProcessor 与同传 Pipeline 之间的解耦边界。
	// Pipeline 仍作为公共运行状态和错误收尾依赖注入，但 ASR final 必须只交给 Finals；
	// 如果实现误改为直接调用 Pipeline.HandleASRFinal，下面的翻译和 FinalTurn 断言会立即失败。
	asrProvider := asr.NewFakeProvider(asr.FakeProviderConfig{Final: asr.FinalResult{
		Text: "你好", SourceLanguage: "zh-CN", Provider: "mock-asr", Model: "v1",
	}})
	translator := &translate.FakeProvider{Result: translate.Result{
		Text: "hello", Provider: "mock-translate", Model: "v1",
	}}
	finalSink := &recordingFinalSink{}
	usageSink := &recordingUsageSink{}
	service := newTestPipelineService(PipelineDependencies{
		Translator: translator,
		TTS:        tts.NewFakeProvider(tts.FakeProviderConfig{}),
		FinalTurns: finalSink,
		Usage:      usageSink,
		Audio:      &recordingAudioSink{},
		Runtime:    &recordingRuntimeReporter{},
	})
	finalHandler := &recordingASRFinalHandler{}
	processor := NewTurnProcessor(TurnProcessorDependencies{
		ASR: asrProvider,
		Opener: newTestTurnOpener(&fakeLanguageConfigReader{snapshot: session.LanguageConfigSnapshot{
			SessionID: "session-1", Version: 1, Status: "active",
			LanguagePairs: []session.LanguagePair{{Source: "zh-CN", Target: "en-US"}},
		}}),
		Pipeline: service,
		Finals:   finalHandler,
	})

	turn, err := processor.ProcessAudio(context.Background(), TurnProcessRequest{
		SessionID: "session-1", AccountID: "account-1", TraceID: "trace-1", AudioChunks: [][]byte{{1, 2}},
	})
	if err != nil {
		t.Fatalf("ProcessAudio() error = %v", err)
	}
	if len(finalHandler.turns) != 1 || finalHandler.turns[0].ID != turn.ID {
		t.Fatalf("ASR final Handler turns = %#v, want Turn %q", finalHandler.turns, turn.ID)
	}
	if len(finalHandler.results) != 1 || finalHandler.results[0].Text != "你好" || finalHandler.results[0].SourceLanguage != "zh-CN" {
		t.Fatalf("ASR final Handler results = %#v, want normalized final result", finalHandler.results)
	}
	// recording Handler 不执行任何同传业务。因此这里必须保持零次翻译和零个 FinalTurn，
	// 证明 ProcessAudio 没有绕过注入边界去调用内部保存的 PipelineService。
	if requests := translator.Requests(); len(requests) != 0 {
		t.Fatalf("translation requests = %#v, want none", requests)
	}
	if len(finalSink.events) != 0 {
		t.Fatalf("FinalTurn events = %#v, want none", finalSink.events)
	}
	if len(usageSink.facts) != 1 || usageSink.facts[0].ServiceType != "asr" || usageSink.facts[0].TurnID != turn.ID {
		t.Fatalf("UsageFacts = %#v, want one ASR fact for injected Handler", usageSink.facts)
	}
}

func TestTurnProcessorStopsBeforeHandlerWhenASRUsageFails(t *testing.T) {
	wantErr := errors.New("usage sink unavailable")
	translator := &translate.FakeProvider{Result: translate.Result{Text: "hello", Provider: "mock-translate", Model: "v1"}}
	finalSink := &recordingFinalSink{}
	runtimeReporter := &recordingRuntimeReporter{}
	service := newTestPipelineService(PipelineDependencies{
		Translator: translator,
		TTS:        tts.NewFakeProvider(tts.FakeProviderConfig{}),
		FinalTurns: finalSink, Usage: rejectingUsageSink{err: wantErr}, Audio: &recordingAudioSink{}, Runtime: runtimeReporter,
	})
	finalHandler := &recordingASRFinalHandler{}
	processor := NewTurnProcessor(TurnProcessorDependencies{
		ASR: asr.NewFakeProvider(asr.FakeProviderConfig{Final: asr.FinalResult{Text: "你好", SourceLanguage: "zh-CN", Provider: "mock-asr", Model: "v1"}}),
		Opener: newTestTurnOpener(&fakeLanguageConfigReader{snapshot: session.LanguageConfigSnapshot{
			SessionID: "session-1", Version: 1, Status: "active",
			LanguagePairs: []session.LanguagePair{{Source: "zh-CN", Target: "en-US"}},
		}}),
		Pipeline: service,
		Finals:   finalHandler,
	})

	_, err := processor.ProcessAudio(context.Background(), TurnProcessRequest{SessionID: "session-1", AccountID: "account-1", TraceID: "trace-1"})
	if !errors.Is(err, wantErr) {
		t.Fatalf("ProcessAudio() error = %v, want %v", err, wantErr)
	}
	if len(finalHandler.turns) != 0 || len(translator.Requests()) != 0 || len(finalSink.events) != 0 {
		t.Fatalf("ASR usage failure reached Handler or interpretation dependencies")
	}
	if len(runtimeReporter.updates) != 2 || runtimeReporter.updates[0].RuntimeState != session.RuntimeASRProcessing || runtimeReporter.updates[1].RuntimeState != session.RuntimeListening {
		t.Fatalf("runtime updates = %#v, want ASR processing then listening", runtimeReporter.updates)
	}
}

func TestTurnProcessorSkipsUsageAndHandlerForEmptyOrTrivialFinal(t *testing.T) {
	for _, test := range []struct {
		name string
		text string
	}{
		{name: "empty", text: "   "},
		{name: "filler", text: "嗯"},
	} {
		t.Run(test.name, func(t *testing.T) {
			usageSink := &recordingUsageSink{}
			runtimeReporter := &recordingRuntimeReporter{}
			service := newTestPipelineService(PipelineDependencies{
				Translator: &translate.FakeProvider{},
				TTS:        tts.NewFakeProvider(tts.FakeProviderConfig{}),
				FinalTurns: &recordingFinalSink{}, Usage: usageSink, Audio: &recordingAudioSink{}, Runtime: runtimeReporter,
			})
			finalHandler := &recordingASRFinalHandler{}
			processor := NewTurnProcessor(TurnProcessorDependencies{
				ASR: asr.NewFakeProvider(asr.FakeProviderConfig{Final: asr.FinalResult{
					Text: test.text, SourceLanguage: "zh-CN", Provider: "mock-asr", Model: "v1",
				}}),
				Opener: newTestTurnOpener(&fakeLanguageConfigReader{snapshot: session.LanguageConfigSnapshot{
					SessionID: "session-1", Version: 1, Status: "active",
					LanguagePairs: []session.LanguagePair{{Source: "zh-CN", Target: "en-US"}},
				}}),
				Pipeline: service,
				Finals:   finalHandler,
			})

			if _, err := processor.ProcessAudio(t.Context(), TurnProcessRequest{SessionID: "session-1", AccountID: "account-1", TraceID: "trace-1"}); err != nil {
				t.Fatalf("ProcessAudio() error = %v", err)
			}
			if len(usageSink.facts) != 0 || len(finalHandler.turns) != 0 {
				t.Fatalf("ignored final produced usage %#v or Handler calls %d", usageSink.facts, len(finalHandler.turns))
			}
			if len(runtimeReporter.updates) != 2 || runtimeReporter.updates[1].RuntimeState != session.RuntimeListening {
				t.Fatalf("runtime updates = %#v, want listening restore", runtimeReporter.updates)
			}
		})
	}
}

func TestTurnProcessorObservesPartialWithoutTranslatingIt(t *testing.T) {
	asrProvider := asr.NewFakeProvider(asr.FakeProviderConfig{
		Partial: asr.Event{Text: "你"},
		Final: asr.FinalResult{
			Text: "你好", SourceLanguage: "zh-CN", Provider: "mock-asr", Model: "v1",
		},
	})
	translator := &translate.FakeProvider{Result: translate.Result{Text: "hello", Provider: "mock-translate", Model: "v1"}}
	finalSink := &recordingFinalSink{}
	service := newTestPipelineService(PipelineDependencies{
		Translator: translator,
		TTS:        tts.NewFakeProvider(tts.FakeProviderConfig{Result: tts.Result{Provider: "mock-tts", Model: "v1"}}),
		FinalTurns: finalSink,
		Usage:      &recordingUsageSink{}, Audio: &recordingAudioSink{}, Runtime: &recordingRuntimeReporter{},
	})
	processor := NewTurnProcessor(TurnProcessorDependencies{
		ASR: asrProvider,
		Opener: newTestTurnOpener(&fakeLanguageConfigReader{snapshot: session.LanguageConfigSnapshot{
			SessionID: "session-1", Version: 1, Status: "active",
			LanguagePairs: []session.LanguagePair{{Source: "zh-CN", Target: "en-US"}},
		}}),
		Pipeline: service,
		Finals:   service,
	})

	turn, err := processor.ProcessAudio(context.Background(), TurnProcessRequest{
		SessionID: "session-1", AccountID: "account-1", TraceID: "trace-1", AudioChunks: [][]byte{{1, 2}},
	})
	if err != nil {
		t.Fatalf("ProcessAudio() error = %v", err)
	}
	requests := translator.Requests()
	if len(requests) != 1 || requests[0].TurnID != turn.ID || requests[0].Text != "你好" {
		t.Fatalf("translation requests = %#v, want one final-text request", requests)
	}
	if len(finalSink.events) != 1 || finalSink.events[0].SourceText != "你好" {
		t.Fatalf("FinalTurn events = %#v, want final text only", finalSink.events)
	}
}

func TestTurnProcessorPropagatesPostFinalFailureClassification(t *testing.T) {
	wantErr := errors.New("translation usage unavailable")
	finalSink := &recordingFinalSink{}
	service := newTestPipelineService(PipelineDependencies{
		Translator: &translate.FakeProvider{Result: translate.Result{Text: "hello", Provider: "mock-translate", Model: "v1"}},
		TTS:        tts.NewFakeProvider(tts.FakeProviderConfig{}),
		FinalTurns: finalSink,
		Usage:      &recordingUsageSink{failService: "translation", err: wantErr},
		Audio:      &recordingAudioSink{}, Runtime: &recordingRuntimeReporter{},
	})
	processor := NewTurnProcessor(TurnProcessorDependencies{
		ASR: asr.NewFakeProvider(asr.FakeProviderConfig{Final: asr.FinalResult{
			Text: "你好", SourceLanguage: "zh-CN", Provider: "mock-asr", Model: "v1",
		}}),
		Opener: newTestTurnOpener(&fakeLanguageConfigReader{snapshot: session.LanguageConfigSnapshot{
			SessionID: "session-1", Version: 1, Status: "active",
			LanguagePairs: []session.LanguagePair{{Source: "zh-CN", Target: "en-US"}},
		}}),
		Pipeline: service,
		Finals:   service,
	})

	_, err := processor.ProcessAudio(context.Background(), TurnProcessRequest{
		SessionID: "session-1", AccountID: "account-1", TraceID: "trace-1",
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("ProcessAudio() error = %v, want %v", err, wantErr)
	}
	if !errors.Is(err, ErrFinalTurnAccepted) {
		t.Fatalf("ProcessAudio() error = %v, want ErrFinalTurnAccepted", err)
	}
	if len(finalSink.events) != 1 {
		t.Fatalf("accepted FinalTurns = %d, want 1", len(finalSink.events))
	}
}

func TestTurnProcessorRejectsIncompletePipelineDependencies(t *testing.T) {
	service := newTestPipelineService(PipelineDependencies{})
	processor := NewTurnProcessor(TurnProcessorDependencies{
		ASR:      asr.NewFakeProvider(asr.FakeProviderConfig{}),
		Opener:   newTestTurnOpener(&fakeLanguageConfigReader{}),
		Pipeline: service,
		Finals:   service,
	})

	_, err := processor.ProcessAudio(context.Background(), TurnProcessRequest{
		SessionID: "session-1", AccountID: "account-1", TraceID: "trace-1",
	})
	if !errors.Is(err, ErrPipelineDependencyRequired) {
		t.Fatalf("ProcessAudio() error = %v, want ErrPipelineDependencyRequired", err)
	}
}

func TestTurnProcessorRequiresFinalHandler(t *testing.T) {
	processor := NewTurnProcessor(TurnProcessorDependencies{
		ASR:      asr.NewFakeProvider(asr.FakeProviderConfig{}),
		Opener:   newTestTurnOpener(&fakeLanguageConfigReader{}),
		Pipeline: newTestPipelineService(PipelineDependencies{}),
	})

	_, err := processor.ProcessAudio(context.Background(), TurnProcessRequest{
		SessionID: "session-1", AccountID: "account-1", TraceID: "trace-1",
	})
	if !errors.Is(err, ErrTurnProcessorDependencyRequired) {
		t.Fatalf("ProcessAudio() error = %v, want ErrTurnProcessorDependencyRequired", err)
	}
}

func TestTurnProcessorConsumesASREventsBeforePushReturns(t *testing.T) {
	stream := &pushEventStream{
		events:      make(chan asr.Event),
		partialSent: make(chan struct{}),
		result:      asr.FinalResult{Text: "你好", SourceLanguage: "zh-CN", Provider: "mock-asr", Model: "v1"},
	}
	service := newTestPipelineService(PipelineDependencies{
		Translator: &translate.FakeProvider{Result: translate.Result{Text: "hello", Provider: "mock-translate", Model: "v1"}},
		TTS:        tts.NewFakeProvider(tts.FakeProviderConfig{Result: tts.Result{Provider: "mock-tts", Model: "v1"}}),
		FinalTurns: &recordingFinalSink{},
		Usage:      &recordingUsageSink{}, Audio: &recordingAudioSink{}, Runtime: &recordingRuntimeReporter{},
	})
	processor := NewTurnProcessor(TurnProcessorDependencies{
		ASR: &pushEventProvider{stream: stream},
		Opener: newTestTurnOpener(&fakeLanguageConfigReader{snapshot: session.LanguageConfigSnapshot{
			SessionID: "session-1", Version: 1, Status: "active",
			LanguagePairs: []session.LanguagePair{{Source: "zh-CN", Target: "en-US"}},
		}}),
		Pipeline: service,
		Finals:   service,
	})

	done := make(chan error, 1)
	go func() {
		_, err := processor.ProcessAudio(context.Background(), TurnProcessRequest{SessionID: "session-1", AccountID: "account-1", TraceID: "trace-1", AudioChunks: [][]byte{{1}}})
		done <- err
	}()
	select {
	case <-stream.partialSent:
	case <-time.After(time.Second):
		t.Fatal("ASR PushAudio blocked before event collector started")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ProcessAudio() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ProcessAudio() did not complete")
	}
}

func TestMergeFinalResultPreservesFinishMetadata(t *testing.T) {
	got := mergeFinalResult(
		asr.FinalResult{Text: "你好", SourceLanguage: "zh-CN"},
		asr.FinalResult{Confidence: .95, ProviderSpeakerID: "speaker-1", AudioStart: time.Second, AudioEnd: 3 * time.Second},
	)
	if got.Confidence != .95 || got.ProviderSpeakerID != "speaker-1" || got.AudioStart != time.Second || got.AudioEnd != 3*time.Second {
		t.Fatalf("merged metadata = %#v", got)
	}
}

func TestRetainConfirmedPartialTextForStashOnlySnapshot(t *testing.T) {
	lastConfirmed := "你好，"
	got := retainConfirmedPartialText(asr.Event{Type: asr.EventPartial, Stash: "世界"}, &lastConfirmed)
	if got.Text != "你好，" || got.Stash != "世界" {
		t.Fatalf("stash-only partial = %#v, want confirmed text plus stash", got)
	}
	got = retainConfirmedPartialText(asr.Event{Type: asr.EventPartial, Text: "你好，世界", Stash: ""}, &lastConfirmed)
	if got.Text != "你好，世界" || lastConfirmed != "你好，世界" {
		t.Fatalf("confirmed partial = %#v, lastConfirmed=%q", got, lastConfirmed)
	}
}

func TestDispatchASRPartialsDropsQueuedSnapshotsAfterFinalSettlement(t *testing.T) {
	t.Parallel()
	events := make(chan asr.Event, 2)
	events <- asr.Event{Type: asr.EventPartial, Text: "first"}
	events <- asr.Event{Type: asr.EventPartial, Text: "latest"}
	close(events)
	settled := make(chan struct{})
	close(settled)
	observer := &recordingASRPartialObserver{events: make(chan realtimev1.ASRPartialEvent, 1)}
	done := make(chan struct{})
	go func() {
		dispatchASRPartials(context.Background(), observer, nil, TurnContext{SessionID: "session-1", ID: "turn-1"}, "zh-CN", events, settled, nil)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("partial dispatcher did not stop after final settlement")
	}
	select {
	case event := <-observer.events:
		t.Fatalf("observer received settled partial %#v", event)
	default:
	}
}

func TestTurnProcessorFinalPathDoesNotWaitForBlockedPartialObserver(t *testing.T) {
	t.Parallel()
	observerStarted := make(chan struct{})
	provider := &pushEventProvider{stream: &pushEventStream{
		events: make(chan asr.Event), partialSent: make(chan struct{}), beforeFinish: observerStarted,
		result: asr.FinalResult{Text: "你好", SourceLanguage: "zh-CN", Provider: "mock-asr", Model: "v1"},
	}}
	service := newTestPipelineService(PipelineDependencies{
		Translator: &translate.FakeProvider{Result: translate.Result{Text: "hello", Provider: "mock-translate", Model: "v1"}},
		TTS:        tts.NewFakeProvider(tts.FakeProviderConfig{Result: tts.Result{Provider: "mock-tts", Model: "v1"}}),
		FinalTurns: &recordingFinalSink{}, Usage: &recordingUsageSink{}, Audio: &recordingAudioSink{}, Runtime: &recordingRuntimeReporter{},
	})
	processor := NewTurnProcessor(TurnProcessorDependencies{
		ASR: provider, Opener: newTestTurnOpener(&fakeLanguageConfigReader{snapshot: session.LanguageConfigSnapshot{
			SessionID: "session-1", Version: 1, Status: "active", LanguagePairs: []session.LanguagePair{{Source: "zh-CN", Target: "en-US"}},
		}}), Pipeline: service, Finals: service, Partials: &blockingASRPartialObserver{started: observerStarted},
	})
	done := make(chan error, 1)
	go func() {
		_, err := processor.ProcessAudio(context.Background(), TurnProcessRequest{SessionID: "session-1", AccountID: "account-1", TraceID: "trace-1", SourceLanguage: "zh-CN", AudioChunks: [][]byte{{1}}})
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ProcessAudio() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("final path waited for blocked partial observer")
	}
}

func TestStartAudioPublishesPartialBeforeFinish(t *testing.T) {
	t.Parallel()
	provider := &pushEventProvider{stream: &pushEventStream{
		events: make(chan asr.Event), partialSent: make(chan struct{}),
		result: asr.FinalResult{Text: "你好", SourceLanguage: "zh-CN", Provider: "mock-asr", Model: "v1"},
	}}
	observer := &recordingASRPartialObserver{events: make(chan realtimev1.ASRPartialEvent, 1)}
	service := newTestPipelineService(PipelineDependencies{
		Translator: &translate.FakeProvider{Result: translate.Result{Text: "hello", Provider: "mock-translate", Model: "v1"}},
		TTS:        tts.NewFakeProvider(tts.FakeProviderConfig{Result: tts.Result{Provider: "mock-tts", Model: "v1"}}),
		FinalTurns: &recordingFinalSink{}, Usage: &recordingUsageSink{}, Audio: &recordingAudioSink{}, Runtime: &recordingRuntimeReporter{},
	})
	processor := NewTurnProcessor(TurnProcessorDependencies{
		ASR: provider, Opener: newTestTurnOpener(&fakeLanguageConfigReader{snapshot: session.LanguageConfigSnapshot{
			SessionID: "session-1", Version: 1, Status: "active", LanguagePairs: []session.LanguagePair{{Source: "zh-CN", Target: "en-US"}},
		}}), Pipeline: service, Finals: service, Partials: observer,
	})
	audioTurn, err := processor.StartAudio(t.Context(), TurnProcessRequest{
		SessionID: "session-1", AccountID: "account-1", TraceID: "trace-1", SourceLanguage: "zh-CN",
	})
	if err != nil {
		t.Fatalf("StartAudio() error = %v", err)
	}
	defer audioTurn.Close()
	if err := audioTurn.PushAudio(t.Context(), []byte{1, 2}); err != nil {
		t.Fatalf("PushAudio() error = %v", err)
	}
	select {
	case partial := <-observer.events:
		if partial.Text != "你" || partial.TurnID == "" {
			t.Fatalf("partial = %#v", partial)
		}
	case <-time.After(time.Second):
		t.Fatal("partial was not published before Finish")
	}
	if _, err := audioTurn.Finish(t.Context()); err != nil {
		t.Fatalf("Finish() error = %v", err)
	}
}

func TestFinishStreamingHandsPendingPhraseToSettlementWorker(t *testing.T) {
	phraseStarted := make(chan struct{})
	releasePhrase := make(chan struct{})
	coordinator := NewPhraseTranslationCoordinator(phraseTranslateFunc(func(_ context.Context, _ translate.Request) (translate.Result, error) {
		close(phraseStarted)
		<-releasePhrase
		return translate.Result{Text: "phrase-en", Provider: "phrase", Model: "v1", InputTokens: 3}, nil
	}), "phrase", &recordingPhraseSubtitleObserver{}, nil)
	phrases := NewPhraseSubtitleProcessor(coordinator, PhraseStabilizerOptions{})
	finals := newAsyncFinalSink()
	usage := newAsyncUsageSink()
	service := newTestPipelineService(PipelineDependencies{
		Translator: &translate.FakeProvider{Result: translate.Result{Text: "unexpected", Provider: "final", Model: "v1"}},
		TTS:        tts.NewFakeProvider(tts.FakeProviderConfig{}), FinalTurns: finals, Usage: usage,
		Audio: &recordingAudioSink{}, Runtime: &recordingRuntimeReporter{}, PhraseTranslations: coordinator,
	})
	defer service.Close()
	processor := NewTurnProcessor(TurnProcessorDependencies{
		ASR: &pushEventProvider{stream: &pushEventStream{
			events: make(chan asr.Event), partialSent: make(chan struct{}), partialText: "你好，",
			result: asr.FinalResult{Text: "你好，", SourceLanguage: "zh-CN", Provider: "mock-asr", Model: "v1"},
		}},
		Opener: newTestTurnOpener(&fakeLanguageConfigReader{snapshot: session.LanguageConfigSnapshot{
			SessionID: "session-1", Version: 1, Status: "active", LanguagePairs: []session.LanguagePair{{Source: "zh-CN", Target: "en-US"}},
		}}),
		Pipeline: service, Finals: service, Phrases: phrases,
	})
	audioTurn, err := processor.StartAudio(t.Context(), TurnProcessRequest{
		SessionID: "session-1", AccountID: "account-1", TraceID: "trace-1", SourceLanguage: "zh-CN",
	})
	if err != nil {
		t.Fatalf("StartAudio() error = %v", err)
	}
	if err := audioTurn.PushAudio(t.Context(), []byte{1}); err != nil {
		t.Fatalf("PushAudio() error = %v", err)
	}
	select {
	case <-phraseStarted:
	case <-time.After(time.Second):
		t.Fatal("phrase translation did not start before VAD final")
	}

	finished := make(chan error, 1)
	go func() {
		_, err := audioTurn.FinishStreaming(t.Context())
		finished <- err
	}()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatalf("FinishStreaming() error = %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("FinishStreaming() waited for pending phrase translation")
	}
	audioTurn.Close()
	if !coordinator.HasPendingPhrase(audioTurn.turn.ID) {
		t.Fatal("AudioTurn.Close() discarded the handed-off phrase settlement")
	}

	close(releasePhrase)
	select {
	case <-finals.done:
	case <-time.After(time.Second):
		t.Fatal("settlement worker did not commit FinalTurn")
	}
	select {
	case <-usage.done:
	case <-time.After(time.Second):
		t.Fatal("settlement worker did not record translation usage")
	}
	if event := finals.Event(); event.TranslatedText != "phrase-en" {
		t.Fatalf("FinalTurn = %#v, want reused phrase result", event)
	}
}

func TestStartAudioDiscardsPhraseStateWhenASRStartupFails(t *testing.T) {
	wantErr := errors.New("ASR unavailable")
	phrases := NewPhraseSubtitleProcessor(&recordingPhraseSubtitleObserver{}, PhraseStabilizerOptions{})
	service := newTestPipelineService(PipelineDependencies{
		Translator: &translate.FakeProvider{}, TTS: tts.NewFakeProvider(tts.FakeProviderConfig{}),
		FinalTurns: &recordingFinalSink{}, Usage: &recordingUsageSink{}, Audio: &recordingAudioSink{}, Runtime: &recordingRuntimeReporter{},
	})
	processor := NewTurnProcessor(TurnProcessorDependencies{
		ASR: asr.NewFakeProvider(asr.FakeProviderConfig{StartErr: wantErr}),
		Opener: newTestTurnOpener(&fakeLanguageConfigReader{snapshot: session.LanguageConfigSnapshot{
			SessionID: "session-1", Version: 1, Status: "active", LanguagePairs: []session.LanguagePair{{Source: "zh-CN", Target: "en-US"}},
		}}),
		Pipeline: service, Finals: service, Phrases: phrases,
	})

	if _, err := processor.StartAudio(t.Context(), TurnProcessRequest{SessionID: "session-1"}); !errors.Is(err, wantErr) {
		t.Fatalf("StartAudio() error = %v, want %v", err, wantErr)
	}
	phrases.mu.Lock()
	defer phrases.mu.Unlock()
	if len(phrases.utterances) != 0 {
		t.Fatalf("phrase state = %#v, want no retained utterances", phrases.utterances)
	}
}

type pushEventProvider struct{ stream *pushEventStream }

func (p *pushEventProvider) StartStream(context.Context, asr.StreamRequest) (asr.Stream, error) {
	return p.stream, nil
}

type pushEventStream struct {
	events       chan asr.Event
	partialSent  chan struct{}
	partialText  string
	beforeFinish <-chan struct{}
	partialOnce  sync.Once
	closeOnce    sync.Once
	result       asr.FinalResult
}

func (s *pushEventStream) PushAudio(ctx context.Context, _ []byte) error {
	var err error
	s.partialOnce.Do(func() {
		partialText := s.partialText
		if partialText == "" {
			partialText = "你"
		}
		select {
		case s.events <- asr.Event{Type: asr.EventPartial, Text: partialText}:
			close(s.partialSent)
		case <-ctx.Done():
			err = ctx.Err()
		}
	})
	return err
}

func (s *pushEventStream) Events() <-chan asr.Event { return s.events }

func (s *pushEventStream) Finish(ctx context.Context) (asr.FinalResult, error) {
	if s.beforeFinish != nil {
		select {
		case <-ctx.Done():
			return asr.FinalResult{}, ctx.Err()
		case <-s.beforeFinish:
		}
	}
	s.closeOnce.Do(func() { close(s.events) })
	return s.result, nil
}

func (s *pushEventStream) Close() error {
	s.closeOnce.Do(func() { close(s.events) })
	return nil
}

var _ asr.Provider = (*pushEventProvider)(nil)
var _ asr.Stream = (*pushEventStream)(nil)

type recordingASRPartialObserver struct {
	events chan realtimev1.ASRPartialEvent
}

func (o *recordingASRPartialObserver) ObserveASRPartial(_ context.Context, event realtimev1.ASRPartialEvent) {
	o.events <- event
}

type blockingASRPartialObserver struct {
	started chan<- struct{}
	once    sync.Once
}

func (o *blockingASRPartialObserver) ObserveASRPartial(ctx context.Context, _ realtimev1.ASRPartialEvent) {
	o.once.Do(func() { close(o.started) })
	<-ctx.Done()
}

var _ ASRPartialObserver = (*recordingASRPartialObserver)(nil)
var _ ASRPartialObserver = (*blockingASRPartialObserver)(nil)

type rejectingUsageSink struct{ err error }

func (s rejectingUsageSink) Publish(context.Context, UsageFact) error { return s.err }

var _ UsageFactSink = rejectingUsageSink{}

// recordingASRFinalHandler 只记录公共 ASR 流程交付的 Turn 和 final 结果，不执行翻译、
// FinalTurn 持久化或 TTS。它使测试能够区分 Finals 注入点与 PipelineService 本身。
type recordingASRFinalHandler struct {
	turns   []TurnContext
	results []asr.FinalResult
}

func (h *recordingASRFinalHandler) HandleASRFinal(_ context.Context, turn TurnContext, result asr.FinalResult) error {
	h.turns = append(h.turns, turn)
	h.results = append(h.results, result)
	return nil
}

var _ ASRFinalHandler = (*recordingASRFinalHandler)(nil)
