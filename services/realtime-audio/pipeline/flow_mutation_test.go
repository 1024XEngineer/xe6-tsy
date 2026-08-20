package pipeline

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/asr"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/session"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/translate"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/tts"
)

func TestStartAudioPropagatesTurnOpenAndRuntimeErrors(t *testing.T) {
	wantOpenErr := errors.New("language config unavailable")
	service := newTestPipelineService(PipelineDependencies{
		Translator: &translate.FakeProvider{}, TTS: tts.NewFakeProvider(tts.FakeProviderConfig{}),
		FinalTurns: &recordingFinalSink{}, Usage: &recordingUsageSink{}, Audio: &recordingAudioSink{}, Runtime: &recordingRuntimeReporter{},
	})
	openProcessor := NewTurnProcessor(turnProcessorDependenciesForMutation(service, &errorLanguageReader{err: wantOpenErr}))
	if _, err := openProcessor.StartAudio(t.Context(), TurnProcessRequest{SessionID: "session-1"}); !errors.Is(err, wantOpenErr) {
		t.Fatalf("StartAudio() open error = %v, want %v", err, wantOpenErr)
	}

	wantRuntimeErr := errors.New("runtime unavailable")
	runtime := stateFailingRuntimeReporter{failState: session.RuntimeASRProcessing, err: wantRuntimeErr}
	runtimeService := newTestPipelineService(PipelineDependencies{
		Translator: &translate.FakeProvider{}, TTS: tts.NewFakeProvider(tts.FakeProviderConfig{}),
		FinalTurns: &recordingFinalSink{}, Usage: &recordingUsageSink{}, Audio: &recordingAudioSink{}, Runtime: runtime,
	})
	runtimeProcessor := NewTurnProcessor(turnProcessorDependenciesForMutation(runtimeService, &fakeLanguageConfigReader{snapshot: session.LanguageConfigSnapshot{
		SessionID: "session-1", Version: 1, Status: "active", LanguagePairs: []session.LanguagePair{{Source: "zh-CN", Target: "en-US"}},
	}}))
	if _, err := runtimeProcessor.StartAudio(t.Context(), TurnProcessRequest{SessionID: "session-1"}); !errors.Is(err, wantRuntimeErr) {
		t.Fatalf("StartAudio() runtime error = %v, want %v", err, wantRuntimeErr)
	}
}

func TestStartAudioInitializesInterpretationPhrases(t *testing.T) {
	phrases := NewPhraseSubtitleProcessor(&recordingPhraseSubtitleObserver{}, PhraseStabilizerOptions{StableAfter: time.Hour})
	service := newTestPipelineService(PipelineDependencies{
		Translator: &translate.FakeProvider{}, TTS: tts.NewFakeProvider(tts.FakeProviderConfig{}),
		FinalTurns: &recordingFinalSink{}, Usage: &recordingUsageSink{}, Audio: &recordingAudioSink{}, Runtime: &recordingRuntimeReporter{},
	})
	processor := NewTurnProcessor(turnProcessorDependenciesForMutation(service, &fakeLanguageConfigReader{snapshot: session.LanguageConfigSnapshot{
		SessionID: "session-1", Version: 1, Status: "active", LanguagePairs: []session.LanguagePair{{Source: "zh-CN", Target: "en-US"}},
	}}))
	processor.phrases = phrases
	turn, err := processor.StartAudio(t.Context(), TurnProcessRequest{SessionID: "session-1"})
	if err != nil {
		t.Fatalf("StartAudio() error = %v", err)
	}
	defer turn.Close()
	phrases.mu.Lock()
	defer phrases.mu.Unlock()
	if phrases.utterances[turn.turn.ID] == nil {
		t.Fatalf("phrase utterances = %#v, want initialized turn", phrases.utterances)
	}
}

func TestStartAudioRejectsNilASRStreamAndCleansPhraseState(t *testing.T) {
	phrases := NewPhraseSubtitleProcessor(&recordingPhraseSubtitleObserver{}, PhraseStabilizerOptions{})
	observer := &recordingProviderFailureObserver{}
	service := newTestPipelineService(PipelineDependencies{
		Translator: &translate.FakeProvider{}, TTS: tts.NewFakeProvider(tts.FakeProviderConfig{}),
		FinalTurns: &recordingFinalSink{}, Usage: &recordingUsageSink{}, Audio: &recordingAudioSink{}, Runtime: &recordingRuntimeReporter{},
		Latency: LatencyLogger{Observer: observer},
	})
	processor := NewTurnProcessor(TurnProcessorDependencies{ASR: nilStreamProvider{}, Opener: newTestTurnOpener(&fakeLanguageConfigReader{snapshot: session.LanguageConfigSnapshot{
		SessionID: "session-1", Version: 1, Status: "active", LanguagePairs: []session.LanguagePair{{Source: "zh-CN", Target: "en-US"}},
	}}), Pipeline: service, Finals: service})
	processor.phrases = phrases
	_, err := processor.StartAudio(t.Context(), TurnProcessRequest{SessionID: "session-1"})
	if !errors.Is(err, ErrASRStreamRequired) {
		t.Fatalf("StartAudio() error = %v, want ErrASRStreamRequired", err)
	}
	if observer.stage != "asr_stream" || observer.calls != 1 {
		t.Fatalf("provider failure observer = %#v, want asr_stream", observer)
	}
	phrases.mu.Lock()
	defer phrases.mu.Unlock()
	if len(phrases.utterances) != 0 {
		t.Fatalf("phrase state = %#v, want discarded", phrases.utterances)
	}
}

func TestStartAudioReportsStreamCheckpoint(t *testing.T) {
	var output bytes.Buffer
	service := newTestPipelineService(PipelineDependencies{
		Translator: &translate.FakeProvider{}, TTS: tts.NewFakeProvider(tts.FakeProviderConfig{}),
		FinalTurns: &recordingFinalSink{}, Usage: &recordingUsageSink{}, Audio: &recordingAudioSink{}, Runtime: &recordingRuntimeReporter{},
		Latency: LatencyLogger{Logger: slog.New(slog.NewJSONHandler(&output, nil))},
	})
	processor := NewTurnProcessor(TurnProcessorDependencies{ASR: asr.NewFakeProvider(asr.FakeProviderConfig{}), Opener: newTestTurnOpener(&fakeLanguageConfigReader{snapshot: session.LanguageConfigSnapshot{
		SessionID: "session-1", Version: 1, Status: "active", LanguagePairs: []session.LanguagePair{{Source: "zh-CN", Target: "en-US"}},
	}}), Pipeline: service, Finals: service})
	turn, err := processor.StartAudio(t.Context(), TurnProcessRequest{SessionID: "session-1"})
	if err != nil {
		t.Fatalf("StartAudio() error = %v", err)
	}
	turn.Close()
	if !bytes.Contains(output.Bytes(), []byte(`"stage":"asr_stream_started"`)) {
		t.Fatalf("checkpoint log = %s, want asr_stream_started", output.String())
	}
}

func turnProcessorDependenciesForMutation(service *PipelineService, languages session.LanguageConfigReader) TurnProcessorDependencies {
	return TurnProcessorDependencies{ASR: asr.NewFakeProvider(asr.FakeProviderConfig{}), Opener: newTestTurnOpener(languages), Pipeline: service, Finals: service}
}

type errorLanguageReader struct{ err error }

func (r *errorLanguageReader) GetCurrentConfig(context.Context, string) (session.LanguageConfigSnapshot, error) {
	return session.LanguageConfigSnapshot{}, r.err
}

type nilStreamProvider struct{}

func (nilStreamProvider) StartStream(context.Context, asr.StreamRequest) (asr.Stream, error) {
	return nil, nil
}

func TestTurnProcessorRejectsEveryMissingDependency(t *testing.T) {
	valid := func() TurnProcessorDependencies {
		service := newTestPipelineService(PipelineDependencies{
			Translator: &translate.FakeProvider{}, TTS: tts.NewFakeProvider(tts.FakeProviderConfig{}),
			FinalTurns: &recordingFinalSink{}, Usage: &recordingUsageSink{}, Audio: &recordingAudioSink{}, Runtime: &recordingRuntimeReporter{},
		})
		return TurnProcessorDependencies{
			ASR:    asr.NewFakeProvider(asr.FakeProviderConfig{}),
			Opener: newTestTurnOpener(&fakeLanguageConfigReader{}), Pipeline: service, Finals: service,
		}
	}

	tests := []struct {
		name  string
		build func() *TurnProcessor
	}{
		{name: "nil processor", build: func() *TurnProcessor { return nil }},
		{name: "recognizer", build: func() *TurnProcessor { deps := valid(); deps.ASR = nil; return NewTurnProcessor(deps) }},
		{name: "opener", build: func() *TurnProcessor { deps := valid(); deps.Opener = nil; return NewTurnProcessor(deps) }},
		{name: "pipeline", build: func() *TurnProcessor { deps := valid(); deps.Pipeline = nil; return NewTurnProcessor(deps) }},
		{name: "final handler", build: func() *TurnProcessor { deps := valid(); deps.Finals = nil; return NewTurnProcessor(deps) }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := test.build().StartAudio(t.Context(), TurnProcessRequest{})
			if !errors.Is(err, ErrTurnProcessorDependencyRequired) {
				t.Fatalf("StartAudio() error = %v, want ErrTurnProcessorDependencyRequired", err)
			}
		})
	}
}

func TestStartAudioReturnsCanceledContextBeforeOpeningTurn(t *testing.T) {
	service := newTestPipelineService(PipelineDependencies{
		Translator: &translate.FakeProvider{}, TTS: tts.NewFakeProvider(tts.FakeProviderConfig{}),
		FinalTurns: &recordingFinalSink{}, Usage: &recordingUsageSink{}, Audio: &recordingAudioSink{}, Runtime: &recordingRuntimeReporter{},
	})
	processor := NewTurnProcessor(TurnProcessorDependencies{
		ASR: asr.NewFakeProvider(asr.FakeProviderConfig{}), Opener: newTestTurnOpener(&fakeLanguageConfigReader{}), Pipeline: service, Finals: service,
	})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := processor.StartAudio(ctx, TurnProcessRequest{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("StartAudio() error = %v, want context.Canceled", err)
	}
}

func TestDispatchASRPartialsUpdatesInterpretationPhraseState(t *testing.T) {
	turn := TurnContext{ID: "turn-1", SessionID: "session-1", Mode: TurnModeSnapshot{Mode: realtimev1.ModeInterpretation}}
	phrases := NewPhraseSubtitleProcessor(&recordingPhraseSubtitleObserver{}, PhraseStabilizerOptions{StableAfter: time.Hour})
	phrases.Start(turn)
	events := make(chan asr.Event, 1)
	events <- asr.Event{Type: asr.EventPartial, Text: "partial text"}
	close(events)

	dispatchASRPartials(t.Context(), nil, phrases, turn, "en-US", events, make(chan struct{}))

	phrases.mu.Lock()
	defer phrases.mu.Unlock()
	utterance := phrases.utterances[turn.ID]
	if utterance == nil || utterance.stabilizer.candidate != "partial text" {
		t.Fatalf("phrase state = %#v, want observed partial candidate", utterance)
	}
}

func TestIsTrivialASRTextPreservesBoundaryBetweenNoiseAndSpeech(t *testing.T) {
	tests := []struct {
		text string
		want bool
	}{
		{text: "  。！？  ", want: true},
		{text: "你", want: true},
		{text: "x", want: true},
		{text: "okay", want: true},
		{text: "你好", want: false},
		{text: "hello", want: false},
	}

	for _, test := range tests {
		if got := isTrivialASRText(test.text); got != test.want {
			t.Errorf("isTrivialASRText(%q) = %v, want %v", test.text, got, test.want)
		}
	}
}

func TestMergeFinalResultBackfillsEveryFinishOnlyField(t *testing.T) {
	got := mergeFinalResult(asr.FinalResult{}, asr.FinalResult{
		Text:              "recognized text",
		SourceLanguage:    "zh-CN",
		Provider:          "mock-asr",
		Model:             "v1",
		AudioDuration:     time.Second,
		Confidence:        .91,
		ProviderSpeakerID: "speaker-1",
		AudioStart:        2 * time.Second,
		AudioEnd:          3 * time.Second,
		CostAmount:        "0.01",
		Currency:          "USD",
	})

	if got.Text != "recognized text" || got.SourceLanguage != "zh-CN" || got.Provider != "mock-asr" || got.Model != "v1" ||
		got.AudioDuration != time.Second || got.Confidence != .91 || got.ProviderSpeakerID != "speaker-1" ||
		got.AudioStart != 2*time.Second || got.AudioEnd != 3*time.Second || got.CostAmount != "0.01" || got.Currency != "USD" {
		t.Fatalf("mergeFinalResult() = %#v", got)
	}
}

func TestCollectFinalASREventKeepsOnlyOneFinal(t *testing.T) {
	first := asr.FinalResult{Text: "first", SourceLanguage: "zh-CN"}
	events := make(chan asr.Event, 3)
	events <- asr.Event{Type: asr.EventPartial, Text: "partial"}
	events <- asr.Event{Type: asr.EventFinal, Final: &first}
	events <- asr.Event{Type: asr.EventFinal, Final: &asr.FinalResult{Text: "duplicate"}}
	close(events)
	finals := make(chan *asr.FinalResult, 1)
	errs := make(chan error, 1)
	partials := make(chan asr.Event, 1)
	settled := make(chan struct{})

	go collectFinalASREvent(context.Background(), LatencyLogger{}, TurnContext{}, time.Now(), events, finals, errs, partials, func() { close(settled) })

	if result := <-finals; result == nil || result.Text != "first" {
		t.Fatalf("final result = %#v, want first final", result)
	}
	if err := <-errs; !errors.Is(err, ErrDuplicateASRFinal) {
		t.Fatalf("event error = %v, want ErrDuplicateASRFinal", err)
	}
	select {
	case <-settled:
	default:
		t.Fatal("final event did not settle partial delivery")
	}
}

func TestTurnProcessorUsesRequestedSourceLanguageWhenASROmitsIt(t *testing.T) {
	service := newTestPipelineService(PipelineDependencies{
		Translator: &translate.FakeProvider{}, TTS: tts.NewFakeProvider(tts.FakeProviderConfig{}),
		FinalTurns: &recordingFinalSink{}, Usage: &recordingUsageSink{}, Audio: &recordingAudioSink{}, Runtime: &recordingRuntimeReporter{},
	})
	finals := &recordingASRFinalHandler{}
	processor := NewTurnProcessor(TurnProcessorDependencies{
		ASR: asr.NewFakeProvider(asr.FakeProviderConfig{Final: asr.FinalResult{Text: "hello", Provider: "mock-asr", Model: "v1"}}),
		Opener: newTestTurnOpener(&fakeLanguageConfigReader{snapshot: session.LanguageConfigSnapshot{
			SessionID: "session-1", Version: 1, Status: "active", LanguagePairs: []session.LanguagePair{{Source: "en-US", Target: "zh-CN"}},
		}}),
		Pipeline: service, Finals: finals,
	})

	if _, err := processor.ProcessAudio(t.Context(), TurnProcessRequest{
		SessionID: "session-1", AccountID: "account-1", TraceID: "trace-1", SourceLanguage: "en-US",
	}); err != nil {
		t.Fatalf("ProcessAudio() error = %v", err)
	}
	if len(finals.results) != 1 || finals.results[0].SourceLanguage != "en-US" {
		t.Fatalf("final handler results = %#v, want requested source language", finals.results)
	}
}
