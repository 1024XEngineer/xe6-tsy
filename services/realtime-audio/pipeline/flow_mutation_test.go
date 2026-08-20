package pipeline

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/asr"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/session"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/translate"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/tts"
)

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
