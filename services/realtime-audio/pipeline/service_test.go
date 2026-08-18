package pipeline

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/asr"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/session"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/translate"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/tts"
)

func TestSpeechOutputRejectsEmptyPlaybackID(t *testing.T) {
	speech := NewSpeechOutput(SpeechOutputDependencies{
		TTS:   tts.NewFakeProvider(tts.FakeProviderConfig{}),
		Audio: &recordingAudioSink{}, Runtime: &recordingRuntimeReporter{},
	})
	_, err := speech.Play(t.Context(), SpeechOutputRequest{
		Turn: testTurn(), Language: "en-US", Text: "hello",
	})
	if !errors.Is(err, ErrSpeechOutputRequestInvalid) {
		t.Fatalf("Play() error = %v, want ErrSpeechOutputRequestInvalid", err)
	}
}

func TestPipelineFinalFlowCarriesTurnID(t *testing.T) {
	translator := &translate.FakeProvider{Result: translate.Result{Text: "hello", Provider: "mock-translate", Model: "v1", InputTokens: 2, OutputTokens: 1}}
	ttsProvider := tts.NewFakeProvider(tts.FakeProviderConfig{
		Chunks: []tts.AudioChunk{{SequenceNo: 1, Data: []byte{1, 2}}, {SequenceNo: 2, Data: []byte{3}}},
		Result: tts.Result{Provider: "mock-tts", Model: "v1", AudioDuration: 250 * time.Millisecond},
	})
	finalSink := &recordingFinalSink{}
	usageSink := &recordingUsageSink{}
	audioSink := &recordingAudioSink{}
	service := newTestPipelineService(PipelineDependencies{
		Translator: translator, TTS: ttsProvider,
		FinalTurns: finalSink, Usage: usageSink, Audio: audioSink, Runtime: &recordingRuntimeReporter{}, VoiceID: "voice-1",
		Now: func() time.Time { return time.Unix(1700000000, 0).UTC() },
	})
	turn := testTurn()
	err := service.HandleASRFinal(context.Background(), turn, asr.FinalResult{
		Text: "你好", SourceLanguage: "zh-CN", ProviderSpeakerID: "speaker-1", Provider: "mock-asr", Model: "v1",
		AudioStart: 30 * time.Second, AudioEnd: 31 * time.Second, AudioDuration: time.Second,
	})
	if err != nil {
		t.Fatalf("HandleASRFinal() error = %v", err)
	}
	if requests := translator.Requests(); len(requests) != 1 || requests[0].TurnID != turn.ID {
		t.Fatalf("translation requests = %#v", requests)
	}
	if requests := ttsProvider.Requests(); len(requests) != 1 || requests[0].TurnID != turn.ID || requests[0].VoiceID != "voice-1" {
		t.Fatalf("TTS requests = %#v", requests)
	}
	if len(finalSink.events) != 1 || finalSink.events[0].EventVersion != recordsv1.FinalTurnEventVersion || finalSink.events[0].TurnID != turn.ID || finalSink.events[0].TargetLanguage != "en-US" || finalSink.events[0].LanguageConfigVersion != 3 || finalSink.events[0].AttributionStatus != recordsv1.AttributionPending {
		t.Fatalf("FinalTurn events = %#v", finalSink.events)
	}
	if finalSink.events[0].ParticipantID != nil || finalSink.events[0].SpeakerCode != recordsv1.PendingSpeakerCode || finalSink.events[0].SpeakerLabelSnapshot != nil {
		t.Fatalf("FinalTurn speaker snapshot = %#v", finalSink.events[0])
	}
	if finalSink.events[0].StartedAt != turn.StartedAt || finalSink.events[0].EndedAt != turn.StartedAt.Add(time.Second) {
		t.Fatalf("FinalTurn bounds = %v..%v", finalSink.events[0].StartedAt, finalSink.events[0].EndedAt)
	}
	if finalSink.events[0].ProviderSpeakerID == nil || *finalSink.events[0].ProviderSpeakerID != "speaker-1" {
		t.Fatalf("FinalTurn provider speaker evidence = %#v", finalSink.events[0])
	}
	if len(usageSink.facts) != 2 || usageSink.facts[0].TurnID != turn.ID || usageSink.facts[1].TurnID != turn.ID || usageSink.facts[0].EventVersion != 1 ||
		usageSink.facts[0].ServiceType != "translation" || usageSink.facts[1].ServiceType != "tts" {
		t.Fatalf("UsageFacts = %#v", usageSink.facts)
	}
	if len(audioSink.chunks) != 2 || audioSink.chunks[0].TurnID != turn.ID {
		t.Fatalf("audio chunks = %#v", audioSink.chunks)
	}
}

func TestPipelineRejectsUnsupportedSourceBeforeTranslation(t *testing.T) {
	translator := &translate.FakeProvider{Result: translate.Result{Text: "unused"}}
	ttsProvider := tts.NewFakeProvider(tts.FakeProviderConfig{})
	service := newTestPipelineService(PipelineDependencies{Translator: translator, TTS: ttsProvider, FinalTurns: &recordingFinalSink{}, Usage: &recordingUsageSink{}, Audio: &recordingAudioSink{}, Runtime: &recordingRuntimeReporter{}})
	err := service.HandleASRFinal(context.Background(), testTurn(), asr.FinalResult{Text: "bonjour", SourceLanguage: "fr-FR", Provider: "mock-asr", Model: "v1"})
	if !errors.Is(err, ErrUnsupportedSourceLanguage) {
		t.Fatalf("error = %v, want ErrUnsupportedSourceLanguage", err)
	}
	if len(translator.Requests()) != 0 || len(ttsProvider.Requests()) != 0 {
		t.Fatalf("providers were called for unsupported source")
	}
}

func TestIsRecoverableUnsupportedSourceLanguage(t *testing.T) {
	recoveryErr := errors.New("runtime store unavailable")
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "sentinel", err: ErrUnsupportedSourceLanguage, want: true},
		{name: "single wrapped", err: fmt.Errorf("process Turn: %w", ErrUnsupportedSourceLanguage), want: true},
		{name: "unrelated", err: recoveryErr, want: false},
		{name: "joined recovery failure", err: errors.Join(ErrUnsupportedSourceLanguage, recoveryErr), want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := IsRecoverableUnsupportedSourceLanguage(test.err); got != test.want {
				t.Fatalf("IsRecoverableUnsupportedSourceLanguage(%v) = %t, want %t", test.err, got, test.want)
			}
		})
	}
}

func TestPipelineRequiresFinalTurnCommitGate(t *testing.T) {
	service := NewPipelineService(PipelineDependencies{
		Translator: &translate.FakeProvider{},
		TTS:        tts.NewFakeProvider(tts.FakeProviderConfig{}),
		FinalTurns: &recordingFinalSink{},
		Usage:      &recordingUsageSink{},
		Audio:      &recordingAudioSink{},
		Runtime:    &recordingRuntimeReporter{},
	})

	err := service.HandleASRFinal(t.Context(), testTurn(), asr.FinalResult{Text: "你好", SourceLanguage: "zh-CN"})
	if !errors.Is(err, ErrPipelineDependencyRequired) {
		t.Fatalf("HandleASRFinal() error = %v, want ErrPipelineDependencyRequired", err)
	}
}

func TestPipelineSkipsTTSAcrossDisabledOutputRoute(t *testing.T) {
	ttsProvider := tts.NewFakeProvider(tts.FakeProviderConfig{})
	finalSink := &recordingFinalSink{}
	usageSink := &recordingUsageSink{}
	service := newTestPipelineService(PipelineDependencies{
		Translator: &translate.FakeProvider{Result: translate.Result{Text: "hello", Provider: "mock-translate", Model: "v1"}},
		TTS:        ttsProvider,
		FinalTurns: finalSink,
		Usage:      usageSink,
		Audio:      &recordingAudioSink{},
		Runtime:    &recordingRuntimeReporter{},
	})
	turn := testTurn()
	turn.LanguageConfig.OutputRoutes = []session.OutputRoute{{TargetLanguage: "en-US", DeliveryEnabled: true}}
	if err := service.HandleASRFinal(context.Background(), turn, asr.FinalResult{Text: "你好", SourceLanguage: "zh-CN", Provider: "mock-asr", Model: "v1"}); err != nil {
		t.Fatalf("HandleASRFinal() error = %v", err)
	}
	if len(ttsProvider.Requests()) != 0 {
		t.Fatalf("TTS requests = %#v, want none", ttsProvider.Requests())
	}
	if len(finalSink.events) != 1 || finalSink.events[0].TTSEnabled || !finalSink.events[0].DeliveryEnabled {
		t.Fatalf("FinalTurn output route = %#v", finalSink.events[0])
	}
	if len(usageSink.facts) != 1 || usageSink.facts[0].ServiceType != "translation" {
		t.Fatalf("usage facts = %#v, want translation only", usageSink.facts)
	}
}

func TestPipelineRoutesLongSourcesToDeliveryBeforeTTS(t *testing.T) {
	tests := []struct {
		name                 string
		sourceText           string
		audioDuration        time.Duration
		longSentenceDelivery bool
		wantLong             bool
	}{
		{name: "text threshold", sourceText: strings.Repeat("字", recordsv1.LongSourceTextThreshold+1), audioDuration: time.Second, longSentenceDelivery: true, wantLong: true},
		{name: "audio threshold", sourceText: "短句", audioDuration: recordsv1.LongSourceAudioThreshold, longSentenceDelivery: true, wantLong: true},
		{name: "below thresholds", sourceText: strings.Repeat("字", recordsv1.LongSourceTextThreshold), audioDuration: recordsv1.LongSourceAudioThreshold - time.Millisecond, longSentenceDelivery: true},
		{name: "capability disabled", sourceText: strings.Repeat("字", recordsv1.LongSourceTextThreshold+1), audioDuration: recordsv1.LongSourceAudioThreshold},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ttsProvider := tts.NewFakeProvider(tts.FakeProviderConfig{Result: tts.Result{Provider: "mock-tts", Model: "v1"}})
			finalSink := &recordingFinalSink{}
			usageSink := &recordingUsageSink{}
			audioSink := &recordingAudioSink{}
			service := newTestPipelineService(PipelineDependencies{
				Translator: &translate.FakeProvider{Result: translate.Result{Text: "translation", Provider: "mock-translate", Model: "v1"}},
				TTS:        ttsProvider, FinalTurns: finalSink, Usage: usageSink, Audio: audioSink,
				Runtime: &recordingRuntimeReporter{}, LongDeliveryEnabled: test.longSentenceDelivery,
			})

			if err := service.HandleASRFinal(t.Context(), testTurn(), asr.FinalResult{
				Text: test.sourceText, SourceLanguage: "zh-CN", AudioDuration: test.audioDuration,
			}); err != nil {
				t.Fatalf("HandleASRFinal() error = %v", err)
			}

			if len(finalSink.events) != 1 {
				t.Fatalf("FinalTurn events = %d, want 1", len(finalSink.events))
			}
			event := finalSink.events[0]
			if event.TTSEnabled == test.wantLong || event.DeliveryEnabled != test.wantLong {
				t.Fatalf("FinalTurn output = tts:%v delivery:%v, want long:%v", event.TTSEnabled, event.DeliveryEnabled, test.wantLong)
			}
			if got := event.DeliveryTrigger == recordsv1.FinalTurnDeliveryTriggerLongSentence; got != test.wantLong {
				t.Fatalf("long-sentence trigger = %v, want %v", got, test.wantLong)
			}
			wantTTSRequests := 1
			wantUsageFacts := 2
			if test.wantLong {
				wantTTSRequests = 0
				wantUsageFacts = 1
			}
			if got := len(ttsProvider.Requests()); got != wantTTSRequests {
				t.Fatalf("TTS requests = %d, want %d", got, wantTTSRequests)
			}
			if got := len(usageSink.facts); got != wantUsageFacts {
				t.Fatalf("usage facts = %#v, want %d", usageSink.facts, wantUsageFacts)
			}
			if len(audioSink.chunks) != 0 {
				t.Fatalf("audio chunks = %#v, want none", audioSink.chunks)
			}
		})
	}
}

func TestPipelineAlwaysProducesPendingAttribution(t *testing.T) {
	finalSink := &recordingFinalSink{}
	service := newTestPipelineService(PipelineDependencies{
		Translator: &translate.FakeProvider{Result: translate.Result{Text: "hello", Provider: "mock-translate", Model: "v1"}},
		TTS:        tts.NewFakeProvider(tts.FakeProviderConfig{Result: tts.Result{Provider: "mock-tts", Model: "v1"}}),
		FinalTurns: finalSink, Usage: &recordingUsageSink{}, Audio: &recordingAudioSink{},
		Runtime: &recordingRuntimeReporter{},
	})
	if err := service.HandleASRFinal(context.Background(), testTurn(), asr.FinalResult{
		Text: "你好", SourceLanguage: "zh-CN", ProviderSpeakerID: "speaker-1", Provider: "mock-asr", Model: "v1",
	}); err != nil {
		t.Fatalf("HandleASRFinal() error = %v", err)
	}
	event := finalSink.events[0]
	if event.ParticipantID != nil || event.AttributionStatus != recordsv1.AttributionPending || event.SpeakerCode != recordsv1.PendingSpeakerCode {
		t.Fatalf("FinalTurn attribution = %#v", event)
	}
}

func TestPipelineKeepsPendingWithoutProviderSpeakerID(t *testing.T) {
	finalSink := &recordingFinalSink{}
	service := newTestPipelineService(PipelineDependencies{
		Translator: &translate.FakeProvider{Result: translate.Result{Text: "hello", Provider: "mock-translate", Model: "v1"}},
		TTS:        tts.NewFakeProvider(tts.FakeProviderConfig{Result: tts.Result{Provider: "mock-tts", Model: "v1"}}),
		FinalTurns: finalSink, Usage: &recordingUsageSink{}, Audio: &recordingAudioSink{},
		Runtime: &recordingRuntimeReporter{},
	})
	if err := service.HandleASRFinal(context.Background(), testTurn(), asr.FinalResult{
		Text: "你好", SourceLanguage: "zh-CN", Provider: "mock-asr", Model: "v1",
	}); err != nil {
		t.Fatalf("HandleASRFinal() error = %v", err)
	}
	event := finalSink.events[0]
	if event.ParticipantID != nil || event.AttributionStatus != recordsv1.AttributionPending {
		t.Fatalf("FinalTurn attribution = %#v", event)
	}
	if event.ProviderSpeakerID != nil {
		t.Fatalf("ProviderSpeakerID = %v, want nil without evidence", *event.ProviderSpeakerID)
	}
}

func TestPipelineCarriesProviderSpeakerIDIntoFinalTurn(t *testing.T) {
	finalSink := &recordingFinalSink{}
	service := newTestPipelineService(PipelineDependencies{
		Translator: &translate.FakeProvider{Result: translate.Result{Text: "hello", Provider: "mock-translate", Model: "v1"}},
		TTS:        tts.NewFakeProvider(tts.FakeProviderConfig{Result: tts.Result{Provider: "mock-tts", Model: "v1"}}),
		FinalTurns: finalSink, Usage: &recordingUsageSink{}, Audio: &recordingAudioSink{},
		Runtime: &recordingRuntimeReporter{},
	})
	if err := service.HandleASRFinal(context.Background(), testTurn(), asr.FinalResult{
		Text: "你好", SourceLanguage: "zh-CN", ProviderSpeakerID: "diar_01", Provider: "mock-asr", Model: "v1",
	}); err != nil {
		t.Fatalf("HandleASRFinal() error = %v", err)
	}
	event := finalSink.events[0]
	if event.ProviderSpeakerID == nil || *event.ProviderSpeakerID != "diar_01" {
		t.Fatalf("ProviderSpeakerID = %v, want diar_01", event.ProviderSpeakerID)
	}
	if event.ParticipantID != nil || event.AttributionStatus != recordsv1.AttributionPending || event.SpeakerCode != recordsv1.PendingSpeakerCode {
		t.Fatalf("FinalTurn attribution = %#v", event)
	}
}

func TestPipelineRejectsInvalidTranslationUsageBeforePublication(t *testing.T) {
	translator := &translate.FakeProvider{Result: translate.Result{Text: "unused"}}
	usageSink := &recordingUsageSink{}
	service := newTestPipelineService(PipelineDependencies{
		Translator: translator, TTS: tts.NewFakeProvider(tts.FakeProviderConfig{}),
		FinalTurns: &recordingFinalSink{}, Usage: usageSink, Audio: &recordingAudioSink{}, Runtime: &recordingRuntimeReporter{},
	})
	turn := testTurn()
	turn.TraceID = ""

	err := service.HandleASRFinal(context.Background(), turn, asr.FinalResult{Text: "你好", SourceLanguage: "zh-CN", Provider: "mock-asr", Model: "v1"})
	if !errors.Is(err, ErrInvalidUsageFact) {
		t.Fatalf("HandleASRFinal() error = %v, want ErrInvalidUsageFact", err)
	}
	if len(usageSink.facts) != 0 || len(translator.Requests()) != 1 {
		t.Fatalf("usage facts = %#v, translation requests = %#v", usageSink.facts, translator.Requests())
	}
}

func TestPipelineFinalTurnFailureStopsLaterStages(t *testing.T) {
	wantErr := errors.New("outbox unavailable")
	finalSink := &recordingFinalSink{err: wantErr}
	usageSink := &recordingUsageSink{}
	ttsProvider := tts.NewFakeProvider(tts.FakeProviderConfig{})
	service := newTestPipelineService(PipelineDependencies{
		Translator: &translate.FakeProvider{Result: translate.Result{Text: "hello", Provider: "mock-translate", Model: "v1"}},
		TTS:        ttsProvider,
		FinalTurns: finalSink,
		Usage:      usageSink,
		Audio:      &recordingAudioSink{},
		Runtime:    &recordingRuntimeReporter{},
	})

	err := service.HandleASRFinal(context.Background(), testTurn(), asr.FinalResult{
		Text: "你好", SourceLanguage: "zh-CN", Provider: "mock-asr", Model: "v1",
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("HandleASRFinal() error = %v, want %v", err, wantErr)
	}
	if errors.Is(err, ErrFinalTurnAccepted) {
		t.Fatalf("HandleASRFinal() error = %v, did not durably accept FinalTurn", err)
	}
	if len(finalSink.events) != 1 {
		t.Fatalf("FinalTurn attempts = %d, want 1", len(finalSink.events))
	}
	if len(usageSink.facts) != 0 {
		t.Fatalf("UsageFacts = %#v, want none before FinalTurn acceptance", usageSink.facts)
	}
	if requests := ttsProvider.Requests(); len(requests) != 0 {
		t.Fatalf("TTS requests = %#v, want none", requests)
	}
}

func TestPipelineDropsSupersededTurnBeforeFinalTurnAndTTS(t *testing.T) {
	translator := &translate.FakeProvider{Result: translate.Result{
		Text: "hello", Provider: "mock-translate", Model: "v1", InputTokens: 2, OutputTokens: 1,
	}}
	finalSink := &recordingFinalSink{}
	usageSink := &recordingUsageSink{}
	ttsProvider := tts.NewFakeProvider(tts.FakeProviderConfig{})
	runtimeReporter := &recordingRuntimeReporter{}
	gate := &supersededFinalTurnGate{}
	service := newTestPipelineService(PipelineDependencies{
		Translator: translator,
		TTS:        ttsProvider,
		FinalTurns: finalSink,
		FinalGate:  gate,
		Usage:      usageSink,
		Audio:      &recordingAudioSink{},
		Runtime:    runtimeReporter,
	})

	err := service.HandleASRFinal(t.Context(), testTurn(), asr.FinalResult{
		Text: "你好", SourceLanguage: "zh-CN", Provider: "mock-asr", Model: "v1",
	})
	if err != nil {
		t.Fatalf("HandleASRFinal() error = %v", err)
	}
	if gate.calls != 1 || len(finalSink.events) != 0 || len(ttsProvider.Requests()) != 0 || len(usageSink.facts) != 1 {
		t.Fatalf("superseded Turn side effects: gate calls %d, FinalTurns %d, TTS %d, Usage %#v", gate.calls, len(finalSink.events), len(ttsProvider.Requests()), usageSink.facts)
	}
	if usageSink.facts[0].ServiceType != "translation" || usageSink.facts[0].InputTokens != 2 || usageSink.facts[0].OutputTokens != 1 {
		t.Fatalf("superseded translation usage = %#v", usageSink.facts[0])
	}
	if len(translator.Requests()) != 1 {
		t.Fatalf("translation requests = %#v, want completed pre-commit translation", translator.Requests())
	}
	if len(runtimeReporter.updates) != 2 || runtimeReporter.updates[0].RuntimeState != session.RuntimeTranslating || runtimeReporter.updates[1].RuntimeState != session.RuntimeListening {
		t.Fatalf("runtime updates = %#v, want translating then listening", runtimeReporter.updates)
	}
}

func TestPipelineReturnsSupersededTranslationUsageFailure(t *testing.T) {
	wantErr := errors.New("usage outbox unavailable")
	usageSink := &recordingUsageSink{failService: "translation", err: wantErr}
	finalSink := &recordingFinalSink{}
	ttsProvider := tts.NewFakeProvider(tts.FakeProviderConfig{})
	service := newTestPipelineService(PipelineDependencies{
		Translator: &translate.FakeProvider{Result: translate.Result{Text: "hello", Provider: "mock-translate", Model: "v1"}},
		TTS:        ttsProvider,
		FinalTurns: finalSink,
		FinalGate:  &supersededFinalTurnGate{},
		Usage:      usageSink,
		Audio:      &recordingAudioSink{},
		Runtime:    &recordingRuntimeReporter{},
	})

	err := service.HandleASRFinal(context.Background(), testTurn(), asr.FinalResult{
		Text: "你好", SourceLanguage: "zh-CN", Provider: "mock-asr", Model: "v1",
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("HandleASRFinal() error = %v, want %v", err, wantErr)
	}
	if len(finalSink.events) != 0 || len(ttsProvider.Requests()) != 0 {
		t.Fatalf("superseded Turn published later-stage side effects: FinalTurns %d, TTS %d", len(finalSink.events), len(ttsProvider.Requests()))
	}
}

func TestPipelinePublishesTranslationUsageWhenTranslateRejects(t *testing.T) {
	usageSink := &recordingUsageSink{}
	finalSink := &recordingFinalSink{}
	ttsProvider := tts.NewFakeProvider(tts.FakeProviderConfig{})
	service := newTestPipelineService(PipelineDependencies{
		Translator: &translate.FakeProvider{
			Result: translate.Result{Provider: "mock-translate", Model: "v1", InputTokens: 9, OutputTokens: 4},
			Err:    translate.ErrUnexpectedBehavior,
		},
		TTS:        ttsProvider,
		FinalTurns: finalSink,
		Usage:      usageSink,
		Audio:      &recordingAudioSink{},
		Runtime:    &recordingRuntimeReporter{},
	})

	err := service.HandleASRFinal(context.Background(), testTurn(), asr.FinalResult{
		Text: "你好", SourceLanguage: "zh-CN", Provider: "mock-asr", Model: "v1",
	})
	if !errors.Is(err, translate.ErrUnexpectedBehavior) {
		t.Fatalf("HandleASRFinal() error = %v, want %v", err, translate.ErrUnexpectedBehavior)
	}
	if len(finalSink.events) != 0 {
		t.Fatalf("FinalTurn events = %#v, want none", finalSink.events)
	}
	if len(usageSink.facts) != 1 {
		t.Fatalf("UsageFacts = %#v, want translation", usageSink.facts)
	}
	if usageSink.facts[0].ServiceType != "translation" {
		t.Fatalf("UsageFacts = %#v", usageSink.facts)
	}
	if usageSink.facts[0].InputTokens != 9 || usageSink.facts[0].OutputTokens != 4 {
		t.Fatalf("translation usage = %#v", usageSink.facts[0])
	}
	if requests := ttsProvider.Requests(); len(requests) != 0 {
		t.Fatalf("TTS requests = %#v, want none", requests)
	}
}

func TestPipelineTranslationUsageFailureKeepsAcceptedFinalTurn(t *testing.T) {
	wantErr := errors.New("usage outbox unavailable")
	finalSink := &recordingFinalSink{}
	usageSink := &recordingUsageSink{failService: "translation", err: wantErr}
	ttsProvider := tts.NewFakeProvider(tts.FakeProviderConfig{})
	service := newTestPipelineService(PipelineDependencies{
		Translator: &translate.FakeProvider{Result: translate.Result{Text: "hello", Provider: "mock-translate", Model: "v1"}},
		TTS:        ttsProvider,
		FinalTurns: finalSink,
		Usage:      usageSink,
		Audio:      &recordingAudioSink{},
		Runtime:    &recordingRuntimeReporter{},
	})

	err := service.HandleASRFinal(context.Background(), testTurn(), asr.FinalResult{
		Text: "你好", SourceLanguage: "zh-CN", Provider: "mock-asr", Model: "v1",
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("HandleASRFinal() error = %v, want %v", err, wantErr)
	}
	if !errors.Is(err, ErrFinalTurnAccepted) {
		t.Fatalf("HandleASRFinal() error = %v, want ErrFinalTurnAccepted", err)
	}
	if len(finalSink.events) != 1 {
		t.Fatalf("accepted FinalTurns = %d, want 1", len(finalSink.events))
	}
	if len(usageSink.facts) != 0 {
		t.Fatalf("accepted UsageFacts = %#v, want none after rejected translation usage", usageSink.facts)
	}
	if requests := ttsProvider.Requests(); len(requests) != 0 {
		t.Fatalf("TTS requests = %#v, want none", requests)
	}
}

func TestPipelineRejectsInvalidTranslationUsageBeforeFinalTurn(t *testing.T) {
	finalSink := &recordingFinalSink{}
	usageSink := &recordingUsageSink{}
	ttsProvider := tts.NewFakeProvider(tts.FakeProviderConfig{})
	service := newTestPipelineService(PipelineDependencies{
		Translator: &translate.FakeProvider{Result: translate.Result{Text: "hello", Model: "v1"}},
		TTS:        ttsProvider,
		FinalTurns: finalSink,
		Usage:      usageSink,
		Audio:      &recordingAudioSink{},
		Runtime:    &recordingRuntimeReporter{},
	})

	err := service.HandleASRFinal(context.Background(), testTurn(), asr.FinalResult{
		Text: "你好", SourceLanguage: "zh-CN", Provider: "mock-asr", Model: "v1",
	})
	if !errors.Is(err, ErrInvalidUsageFact) {
		t.Fatalf("HandleASRFinal() error = %v, want ErrInvalidUsageFact", err)
	}
	if errors.Is(err, ErrFinalTurnAccepted) {
		t.Fatalf("HandleASRFinal() error = %v, FinalTurn was not accepted", err)
	}
	if len(finalSink.events) != 0 {
		t.Fatalf("FinalTurn attempts = %d, want 0", len(finalSink.events))
	}
	if len(usageSink.facts) != 0 {
		t.Fatalf("UsageFacts = %#v, want none", usageSink.facts)
	}
	if requests := ttsProvider.Requests(); len(requests) != 0 {
		t.Fatalf("TTS requests = %#v, want none", requests)
	}
}

func TestPipelineTTSFailureReportsAcceptedFinalTurn(t *testing.T) {
	startErr := errors.New("start failed")
	audioErr := errors.New("audio failed")
	finishErr := errors.New("finish failed")
	usageErr := errors.New("usage failed")
	tests := []struct {
		name      string
		config    tts.FakeProviderConfig
		usageSink *recordingUsageSink
		audioSink *recordingAudioSink
		wantErr   error
	}{
		{
			name:      "start",
			wantErr:   startErr,
			config:    tts.FakeProviderConfig{StartErr: startErr},
			usageSink: &recordingUsageSink{},
			audioSink: &recordingAudioSink{},
		},
		{
			name:      "audio",
			wantErr:   audioErr,
			config:    tts.FakeProviderConfig{Chunks: []tts.AudioChunk{{SequenceNo: 1, Data: []byte{1}}}},
			usageSink: &recordingUsageSink{},
			audioSink: &recordingAudioSink{err: audioErr},
		},
		{
			name:      "finish",
			wantErr:   finishErr,
			config:    tts.FakeProviderConfig{FinishErr: finishErr},
			usageSink: &recordingUsageSink{},
			audioSink: &recordingAudioSink{},
		},
		{
			name:      "usage",
			wantErr:   usageErr,
			config:    tts.FakeProviderConfig{Result: tts.Result{Provider: "mock-tts", Model: "v1"}},
			usageSink: &recordingUsageSink{failService: "tts", err: usageErr},
			audioSink: &recordingAudioSink{},
		},
		{
			name:      "invalid usage",
			wantErr:   ErrInvalidUsageFact,
			config:    tts.FakeProviderConfig{Result: tts.Result{Model: "v1"}},
			usageSink: &recordingUsageSink{},
			audioSink: &recordingAudioSink{},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			finalSink := &recordingFinalSink{}
			service := newTestPipelineService(PipelineDependencies{
				Translator: &translate.FakeProvider{Result: translate.Result{Text: "hello", Provider: "mock-translate", Model: "v1"}},
				TTS:        tts.NewFakeProvider(test.config),
				FinalTurns: finalSink,
				Usage:      test.usageSink,
				Audio:      test.audioSink,
				Runtime:    &recordingRuntimeReporter{},
			})

			err := service.HandleASRFinal(context.Background(), testTurn(), asr.FinalResult{
				Text: "你好", SourceLanguage: "zh-CN", Provider: "mock-asr", Model: "v1",
			})
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("HandleASRFinal() error = %v, want %v", err, test.wantErr)
			}
			if !errors.Is(err, ErrFinalTurnAccepted) {
				t.Fatalf("HandleASRFinal() error = %v, want ErrFinalTurnAccepted", err)
			}
			if len(finalSink.events) != 1 {
				t.Fatalf("accepted FinalTurns = %d, want 1", len(finalSink.events))
			}
		})
	}
}

func TestPipelineRuntimeFailureAfterFinalTurnIsClassified(t *testing.T) {
	wantErr := errors.New("runtime store unavailable")
	tests := []struct {
		name      string
		failState session.RuntimeState
	}{
		{name: "TTS processing", failState: session.RuntimeTTSProcessing},
		{name: "restore listening", failState: session.RuntimeListening},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			finalSink := &recordingFinalSink{}
			service := newTestPipelineService(PipelineDependencies{
				Translator: &translate.FakeProvider{Result: translate.Result{Text: "hello", Provider: "mock-translate", Model: "v1"}},
				TTS:        tts.NewFakeProvider(tts.FakeProviderConfig{Result: tts.Result{Provider: "mock-tts", Model: "v1"}}),
				FinalTurns: finalSink, Usage: &recordingUsageSink{}, Audio: &recordingAudioSink{},
				Runtime: stateFailingRuntimeReporter{failState: test.failState, err: wantErr},
			})

			err := service.HandleASRFinal(context.Background(), testTurn(), asr.FinalResult{
				Text: "你好", SourceLanguage: "zh-CN", Provider: "mock-asr", Model: "v1",
			})
			if !errors.Is(err, wantErr) || !errors.Is(err, ErrFinalTurnAccepted) {
				t.Fatalf("HandleASRFinal() error = %v, want runtime error and ErrFinalTurnAccepted", err)
			}
			if len(finalSink.events) != 1 {
				t.Fatalf("accepted FinalTurns = %d, want 1", len(finalSink.events))
			}
		})
	}
}

func TestPipelineRejectsInvalidFinalTurnBeforePublication(t *testing.T) {
	finalSink := &recordingFinalSink{}
	usageSink := &recordingUsageSink{}
	ttsProvider := tts.NewFakeProvider(tts.FakeProviderConfig{})
	service := newTestPipelineService(PipelineDependencies{
		Translator: &translate.FakeProvider{Result: translate.Result{Provider: "mock-translate", Model: "v1"}},
		TTS:        ttsProvider,
		FinalTurns: finalSink,
		Usage:      usageSink,
		Audio:      &recordingAudioSink{},
		Runtime:    &recordingRuntimeReporter{},
	})

	err := service.HandleASRFinal(context.Background(), testTurn(), asr.FinalResult{
		Text: "你好", SourceLanguage: "zh-CN", Provider: "mock-asr", Model: "v1",
	})
	if !errors.Is(err, recordsv1.ErrInvalidFinalTurnEvent) {
		t.Fatalf("HandleASRFinal() error = %v, want ErrInvalidFinalTurnEvent", err)
	}
	if len(finalSink.events) != 0 {
		t.Fatalf("FinalTurn attempts = %d, want 0", len(finalSink.events))
	}
	if len(usageSink.facts) != 0 {
		t.Fatalf("UsageFacts = %#v, want none", usageSink.facts)
	}
	if requests := ttsProvider.Requests(); len(requests) != 0 {
		t.Fatalf("TTS requests = %#v, want none", requests)
	}
}

func TestPipelineCancellationClosesBlockedTTSStream(t *testing.T) {
	stream := &blockingTTSStream{chunks: make(chan tts.AudioChunk), closed: make(chan struct{})}
	provider := &blockingTTSProvider{stream: stream, started: make(chan struct{})}
	service := newTestPipelineService(PipelineDependencies{
		Translator: &translate.FakeProvider{Result: translate.Result{Text: "hello", Provider: "mock-translate", Model: "v1"}},
		TTS:        provider, FinalTurns: &recordingFinalSink{}, Usage: &recordingUsageSink{}, Audio: &recordingAudioSink{}, Runtime: &recordingRuntimeReporter{},
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- service.HandleASRFinal(ctx, testTurn(), asr.FinalResult{
			Text: "你好", SourceLanguage: "zh-CN", Provider: "mock-asr", Model: "v1",
		})
	}()

	select {
	case <-provider.started:
		cancel()
	case <-time.After(time.Second):
		t.Fatal("TTS stream did not start")
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("HandleASRFinal() error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("HandleASRFinal() did not return after cancellation")
	}
	select {
	case <-stream.closed:
	default:
		t.Fatal("TTS stream was not closed")
	}
}

func TestPipelineCompletesPlaybackAfterTTSFinishes(t *testing.T) {
	audioSink := &recordingPlaybackSink{}
	service := newTestPipelineService(PipelineDependencies{
		Translator: &translate.FakeProvider{Result: translate.Result{Text: "hello", Provider: "mock-translate", Model: "v1"}},
		TTS: tts.NewFakeProvider(tts.FakeProviderConfig{
			Chunks: []tts.AudioChunk{{SequenceNo: 1, Data: []byte{1, 2}}},
			Result: tts.Result{Provider: "mock-tts", Model: "v1"},
		}),
		FinalTurns: &recordingFinalSink{}, Usage: &recordingUsageSink{}, Audio: audioSink, Runtime: &recordingRuntimeReporter{},
	})
	if err := service.HandleASRFinal(context.Background(), testTurn(), asr.FinalResult{Text: "你好", SourceLanguage: "zh-CN", Provider: "mock-asr", Model: "v1"}); err != nil {
		t.Fatalf("HandleASRFinal() error = %v", err)
	}
	if !reflect.DeepEqual(audioSink.completed, []string{"playback_turn-1"}) || len(audioSink.cancelled) != 0 {
		t.Fatalf("completed = %#v, cancelled = %#v", audioSink.completed, audioSink.cancelled)
	}
}

func TestPipelinePlaysFallbackWithoutPublishingAnotherFinalTurn(t *testing.T) {
	finalSink := &recordingFinalSink{}
	usageSink := &recordingUsageSink{}
	ttsProvider := tts.NewFakeProvider(tts.FakeProviderConfig{
		Chunks: []tts.AudioChunk{{SequenceNo: 1, Data: []byte{1, 2}}},
		Result: tts.Result{Provider: "mock-tts", Model: "v1"},
	})
	service := newTestPipelineService(PipelineDependencies{
		Translator: &translate.FakeProvider{}, TTS: ttsProvider,
		FinalTurns: finalSink, Usage: usageSink, Audio: &recordingAudioSink{}, Runtime: &recordingRuntimeReporter{},
	})

	err := service.PlayFallback(t.Context(), FallbackPlayback{
		SessionID: "session-1", TurnID: "turn-1", AccountID: "account-1", TraceID: "trace-1",
		TargetLanguage: "zh-CN", TranslatedText: "补播译文", LanguageConfigVersion: 3, PlaybackID: "fallback-operation-1",
	})
	if err != nil {
		t.Fatalf("PlayFallback() error = %v", err)
	}
	if len(finalSink.events) != 0 {
		t.Fatalf("fallback published FinalTurns = %d, want 0", len(finalSink.events))
	}
	requests := ttsProvider.Requests()
	if len(requests) != 1 || requests[0].Text != "补播译文" || requests[0].PlaybackID != "fallback-operation-1" {
		t.Fatalf("TTS requests = %#v", requests)
	}
	if len(usageSink.facts) != 1 || usageSink.facts[0].ServiceType != "tts" {
		t.Fatalf("UsageFacts = %#v, want one TTS fact", usageSink.facts)
	}
}

func TestPipelineMarksPrePlaybackFallbackFailures(t *testing.T) {
	tests := []struct {
		name string
		svc  *PipelineService
	}{
		{
			name: "missing dependency",
			svc:  &PipelineService{},
		},
		{
			name: "runtime report failure",
			svc: newTestPipelineService(PipelineDependencies{
				Translator: &translate.FakeProvider{Result: translate.Result{Text: "hello"}},
				TTS: tts.NewFakeProvider(tts.FakeProviderConfig{
					Chunks: []tts.AudioChunk{{SequenceNo: 1, Data: []byte{1, 2}}},
				}),
				FinalTurns: &recordingFinalSink{}, Usage: &recordingUsageSink{}, Audio: &recordingAudioSink{},
				Runtime: stateFailingRuntimeReporter{failState: session.RuntimeTTSProcessing, err: errors.New("runtime unavailable")},
			}),
		},
		{
			name: "tts start failure",
			svc: newTestPipelineService(PipelineDependencies{
				Translator: &translate.FakeProvider{Result: translate.Result{Text: "hello"}},
				TTS:        tts.NewFakeProvider(tts.FakeProviderConfig{StartErr: errors.New("tts start unavailable")}),
				FinalTurns: &recordingFinalSink{}, Usage: &recordingUsageSink{}, Audio: &recordingAudioSink{}, Runtime: &recordingRuntimeReporter{},
			}),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.svc.PlayFallback(t.Context(), FallbackPlayback{
				SessionID: "session-1", TurnID: "turn-1", AccountID: "account-1", TraceID: "trace-1",
				TargetLanguage: "zh-CN", TranslatedText: "补播译文", LanguageConfigVersion: 3, PlaybackID: "fallback-operation-1",
			})
			if err == nil {
				t.Fatal("PlayFallback() error = nil")
			}
			if !hasFallbackPlaybackNotStarted(err) {
				t.Fatalf("PlayFallback() error = %v, want not-started marker", err)
			}
		})
	}
}

func TestPipelineDoesNotMarkPostStartFallbackFailures(t *testing.T) {
	service := newTestPipelineService(PipelineDependencies{
		Translator: &translate.FakeProvider{Result: translate.Result{Text: "hello"}},
		TTS: tts.NewFakeProvider(tts.FakeProviderConfig{
			Chunks:    []tts.AudioChunk{{SequenceNo: 1, Data: []byte{1, 2}}},
			FinishErr: errors.New("finish unavailable"),
		}),
		FinalTurns: &recordingFinalSink{}, Usage: &recordingUsageSink{}, Audio: &recordingAudioSink{}, Runtime: &recordingRuntimeReporter{},
	})
	err := service.PlayFallback(t.Context(), FallbackPlayback{
		SessionID: "session-1", TurnID: "turn-1", AccountID: "account-1", TraceID: "trace-1",
		TargetLanguage: "zh-CN", TranslatedText: "补播译文", LanguageConfigVersion: 3, PlaybackID: "fallback-operation-1",
	})
	if err == nil {
		t.Fatal("PlayFallback() error = nil")
	}
	if hasFallbackPlaybackNotStarted(err) {
		t.Fatalf("PlayFallback() error = %v, unexpectedly marked not-started", err)
	}
}

func TestPipelineCancelsPlaybackAfterTTSError(t *testing.T) {
	wantErr := errors.New("TTS finish failed")
	audioSink := &recordingPlaybackSink{}
	service := newTestPipelineService(PipelineDependencies{
		Translator: &translate.FakeProvider{Result: translate.Result{Text: "hello", Provider: "mock-translate", Model: "v1"}},
		TTS: tts.NewFakeProvider(tts.FakeProviderConfig{
			Chunks: []tts.AudioChunk{{SequenceNo: 1, Data: []byte{1, 2}}}, FinishErr: wantErr,
		}),
		FinalTurns: &recordingFinalSink{}, Usage: &recordingUsageSink{}, Audio: audioSink, Runtime: &recordingRuntimeReporter{},
	})
	err := service.HandleASRFinal(context.Background(), testTurn(), asr.FinalResult{Text: "你好", SourceLanguage: "zh-CN", Provider: "mock-asr", Model: "v1"})
	if !errors.Is(err, wantErr) {
		t.Fatalf("HandleASRFinal() error = %v", err)
	}
	if len(audioSink.completed) != 0 || !reflect.DeepEqual(audioSink.cancelled, []string{"playback_turn-1"}) {
		t.Fatalf("completed = %#v, cancelled = %#v", audioSink.completed, audioSink.cancelled)
	}
}

func TestPipelineIgnoresPartialASREvents(t *testing.T) {
	translator := &translate.FakeProvider{Result: translate.Result{Text: "unused"}}
	service := newTestPipelineService(PipelineDependencies{Translator: translator, TTS: tts.NewFakeProvider(tts.FakeProviderConfig{}), FinalTurns: &recordingFinalSink{}, Usage: &recordingUsageSink{}, Audio: &recordingAudioSink{}, Runtime: &recordingRuntimeReporter{}})
	if err := service.HandleASREvent(context.Background(), testTurn(), asr.Event{Type: asr.EventPartial, Text: "你"}); err != nil {
		t.Fatalf("HandleASREvent() error = %v", err)
	}
	if len(translator.Requests()) != 0 {
		t.Fatalf("partial event triggered translation")
	}
}

func testTurn() TurnContext {
	return TurnContext{ID: "turn-1", SessionID: "session-1", AccountID: "account-1", TraceID: "trace-1", SequenceNo: 1, LanguageConfig: session.LanguageConfigSnapshot{SessionID: "session-1", Version: 3, Status: "active", LanguagePairs: []session.LanguagePair{{Source: "zh-CN", Target: "en-US"}}}, StartedAt: time.Unix(1700000000, 0).UTC()}
}

type recordingFinalSink struct {
	events []FinalTurnEvent
	err    error
}

type acceptingFinalTurnGate struct{}

type supersededFinalTurnGate struct {
	calls int
}

func (acceptingFinalTurnGate) CommitFinalTurn(ctx context.Context, _ TurnContext, commit FinalTurnCommit) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if commit == nil {
		return false, ErrPipelineDependencyRequired
	}
	if err := commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func (g *supersededFinalTurnGate) CommitFinalTurn(_ context.Context, _ TurnContext, _ FinalTurnCommit) (bool, error) {
	g.calls++
	return false, nil
}

func newTestPipelineService(deps PipelineDependencies) *PipelineService {
	if deps.FinalGate == nil {
		deps.FinalGate = acceptingFinalTurnGate{}
	}
	return NewPipelineService(deps)
}

func (s *recordingFinalSink) Publish(_ context.Context, event FinalTurnEvent) error {
	s.events = append(s.events, event)
	return s.err
}

type recordingUsageSink struct {
	facts       []UsageFact
	failService string
	err         error
}

func (s *recordingUsageSink) Publish(_ context.Context, fact UsageFact) error {
	if fact.ServiceType == s.failService {
		return s.err
	}
	s.facts = append(s.facts, fact)
	return nil
}

type recordingAudioSink struct {
	chunks []AudioChunk
	err    error
}

type recordingPlaybackSink struct {
	recordingAudioSink
	completed []string
	cancelled []string
}

func (s *recordingPlaybackSink) Complete(_ context.Context, _, playbackID string) error {
	s.completed = append(s.completed, playbackID)
	return nil
}

func (s *recordingPlaybackSink) Cancel(_ context.Context, _, playbackID, _ string) error {
	s.cancelled = append(s.cancelled, playbackID)
	return nil
}

func (s *recordingAudioSink) Publish(_ context.Context, chunk AudioChunk) error {
	if s.err != nil {
		return s.err
	}
	s.chunks = append(s.chunks, chunk)
	return nil
}

type recordingRuntimeReporter struct {
	updates []session.ProcessingStateUpdate
}

func (r *recordingRuntimeReporter) SetProcessingState(_ context.Context, update session.ProcessingStateUpdate) error {
	r.updates = append(r.updates, update)
	return nil
}

type stateFailingRuntimeReporter struct {
	failState session.RuntimeState
	err       error
}

func (r stateFailingRuntimeReporter) SetProcessingState(_ context.Context, update session.ProcessingStateUpdate) error {
	if update.RuntimeState == r.failState {
		return r.err
	}
	return nil
}

type blockingTTSProvider struct {
	stream  *blockingTTSStream
	started chan struct{}
}

func (p *blockingTTSProvider) StartStream(context.Context, tts.Request) (tts.Stream, error) {
	close(p.started)
	return p.stream, nil
}

type blockingTTSStream struct {
	chunks <-chan tts.AudioChunk
	closed chan struct{}
}

func TestTargetLanguageMatchesRussianProviderShortCode(t *testing.T) {
	config := session.LanguageConfigSnapshot{LanguagePairs: []session.LanguagePair{
		{Source: "zh-CN", Target: "ru-RU"},
		{Source: "ru-RU", Target: "zh-CN"},
	}}
	if target, _, ok := targetRoute(config, "ru"); !ok || target != "zh-CN" {
		t.Fatalf("targetLanguage(ru) = %q, %v; want zh-CN, true", target, ok)
	}
}

func (s *blockingTTSStream) Chunks() <-chan tts.AudioChunk { return s.chunks }

func (*blockingTTSStream) Finish(context.Context) (tts.Result, error) {
	return tts.Result{}, errors.New("Finish must not be called while chunks are blocked")
}

func (s *blockingTTSStream) Close() error {
	close(s.closed)
	return nil
}

func hasFallbackPlaybackNotStarted(err error) bool {
	type fallbackPlaybackNotStarted interface {
		FallbackPlaybackNotStarted()
	}
	var marker fallbackPlaybackNotStarted
	return errors.As(err, &marker)
}

var _ recordsv1.FinalTurnSink = (*recordingFinalSink)(nil)
var _ FinalTurnCommitGate = acceptingFinalTurnGate{}
var _ FinalTurnCommitGate = (*supersededFinalTurnGate)(nil)
var _ UsageFactSink = (*recordingUsageSink)(nil)
var _ AudioChunkSink = (*recordingAudioSink)(nil)
var _ session.RuntimeStateReporter = (*recordingRuntimeReporter)(nil)
var _ session.RuntimeStateReporter = stateFailingRuntimeReporter{}
var _ tts.Provider = (*blockingTTSProvider)(nil)
var _ tts.Stream = (*blockingTTSStream)(nil)
