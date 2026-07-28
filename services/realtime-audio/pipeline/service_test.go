package pipeline

import (
	"context"
	"errors"
	"testing"
	"time"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/asr"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/session"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/translate"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/tts"
)

func TestPipelineFinalFlowCarriesTurnID(t *testing.T) {
	translator := &translate.FakeProvider{Result: translate.Result{Text: "hello", Provider: "mock-translate", Model: "v1", InputTokens: 2, OutputTokens: 1}}
	ttsProvider := tts.NewFakeProvider(tts.FakeProviderConfig{
		Chunks: []tts.AudioChunk{{SequenceNo: 1, Data: []byte{1, 2}}, {SequenceNo: 2, Data: []byte{3}}},
		Result: tts.Result{Provider: "mock-tts", Model: "v1", AudioDuration: 250 * time.Millisecond},
	})
	finalSink := &recordingFinalSink{}
	usageSink := &recordingUsageSink{}
	audioSink := &recordingAudioSink{}
	speakerReader := &fixedSpeakerReader{participantID: "participant-1"}
	service := NewPipelineService(PipelineDependencies{
		Translator: translator, TTS: ttsProvider, Speakers: speakerReader,
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
	if len(finalSink.events) != 1 || finalSink.events[0].EventVersion != recordsv1.FinalTurnEventVersion || finalSink.events[0].TurnID != turn.ID || finalSink.events[0].TargetLanguage != "en-US" || finalSink.events[0].LanguageConfigVersion != 3 || finalSink.events[0].AttributionStatus != recordsv1.AttributionProvisional {
		t.Fatalf("FinalTurn events = %#v", finalSink.events)
	}
	if finalSink.events[0].SpeakerCode != "speaker-1" || finalSink.events[0].SpeakerLabelSnapshot == nil || *finalSink.events[0].SpeakerLabelSnapshot != "Speaker 1" {
		t.Fatalf("FinalTurn speaker snapshot = %#v", finalSink.events[0])
	}
	if finalSink.events[0].StartedAt != turn.StartedAt || finalSink.events[0].EndedAt != turn.StartedAt.Add(time.Second) {
		t.Fatalf("FinalTurn bounds = %v..%v", finalSink.events[0].StartedAt, finalSink.events[0].EndedAt)
	}
	if speakerReader.observation.TurnID != turn.ID || speakerReader.observation.AudioStartMS != 30000 || speakerReader.observation.AudioEndMS != 31000 {
		t.Fatalf("speaker observation = %#v", speakerReader.observation)
	}
	if len(usageSink.facts) != 3 || usageSink.facts[0].TurnID != turn.ID || usageSink.facts[1].TurnID != turn.ID || usageSink.facts[2].TurnID != turn.ID || usageSink.facts[0].EventVersion != 1 {
		t.Fatalf("UsageFacts = %#v", usageSink.facts)
	}
	if len(audioSink.chunks) != 2 || audioSink.chunks[0].TurnID != turn.ID {
		t.Fatalf("audio chunks = %#v", audioSink.chunks)
	}
}

func TestPipelineRejectsUnsupportedSourceBeforeTranslation(t *testing.T) {
	translator := &translate.FakeProvider{Result: translate.Result{Text: "unused"}}
	ttsProvider := tts.NewFakeProvider(tts.FakeProviderConfig{})
	service := NewPipelineService(PipelineDependencies{Translator: translator, TTS: ttsProvider, FinalTurns: &recordingFinalSink{}, Usage: &recordingUsageSink{}, Audio: &recordingAudioSink{}, Runtime: &recordingRuntimeReporter{}})
	err := service.HandleASRFinal(context.Background(), testTurn(), asr.FinalResult{Text: "bonjour", SourceLanguage: "fr-FR", Provider: "mock-asr", Model: "v1"})
	if !errors.Is(err, ErrUnsupportedSourceLanguage) {
		t.Fatalf("error = %v, want ErrUnsupportedSourceLanguage", err)
	}
	if len(translator.Requests()) != 0 || len(ttsProvider.Requests()) != 0 {
		t.Fatalf("providers were called for unsupported source")
	}
}

func TestPipelineSpeakerTimeoutProducesPendingAttribution(t *testing.T) {
	service := NewPipelineService(PipelineDependencies{
		Translator: &translate.FakeProvider{Result: translate.Result{Text: "hello", Provider: "mock-translate", Model: "v1"}},
		TTS:        tts.NewFakeProvider(tts.FakeProviderConfig{Result: tts.Result{Provider: "mock-tts", Model: "v1"}}),
		Speakers:   blockingSpeakerReader{}, FinalTurns: &recordingFinalSink{}, Usage: &recordingUsageSink{}, Audio: &recordingAudioSink{},
		Runtime:        &recordingRuntimeReporter{},
		SpeakerTimeout: 5 * time.Millisecond,
	})
	started := time.Now()
	err := service.HandleASRFinal(context.Background(), testTurn(), asr.FinalResult{Text: "你好", SourceLanguage: "zh-CN", ProviderSpeakerID: "speaker-1", Provider: "mock-asr", Model: "v1"})
	if err != nil {
		t.Fatalf("HandleASRFinal() error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > 200*time.Millisecond {
		t.Fatalf("speaker lookup blocked pipeline for %v", elapsed)
	}
	event := service.finalTurns.(*recordingFinalSink).events[0]
	if event.ParticipantID != nil || event.AttributionStatus != recordsv1.AttributionPending || event.SpeakerCode != recordsv1.PendingSpeakerCode {
		t.Fatalf("FinalTurn attribution = %#v", event)
	}
}

func TestPipelineMissingSpeakerInputsProducePendingAttribution(t *testing.T) {
	tests := []struct {
		name     string
		speakers recordsv1.SpeakerAttributionReader
		result   asr.FinalResult
	}{
		{
			name:   "speaker reader missing",
			result: asr.FinalResult{Text: "你好", SourceLanguage: "zh-CN", ProviderSpeakerID: "speaker-1", Provider: "mock-asr", Model: "v1"},
		},
		{
			name:     "provider speaker ID missing",
			speakers: &fixedSpeakerReader{participantID: "participant-1"},
			result:   asr.FinalResult{Text: "你好", SourceLanguage: "zh-CN", Provider: "mock-asr", Model: "v1"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			finalSink := &recordingFinalSink{}
			service := NewPipelineService(PipelineDependencies{
				Translator: &translate.FakeProvider{Result: translate.Result{Text: "hello", Provider: "mock-translate", Model: "v1"}},
				TTS:        tts.NewFakeProvider(tts.FakeProviderConfig{Result: tts.Result{Provider: "mock-tts", Model: "v1"}}),
				Speakers:   test.speakers, FinalTurns: finalSink, Usage: &recordingUsageSink{}, Audio: &recordingAudioSink{},
				Runtime: &recordingRuntimeReporter{},
			})

			if err := service.HandleASRFinal(context.Background(), testTurn(), test.result); err != nil {
				t.Fatalf("HandleASRFinal() error = %v", err)
			}
			event := finalSink.events[0]
			if event.ParticipantID != nil || event.AttributionStatus != recordsv1.AttributionPending || event.SpeakerCode != recordsv1.PendingSpeakerCode {
				t.Fatalf("FinalTurn attribution = %#v", event)
			}
		})
	}
}

func TestPipelineRejectsInvalidUsageBeforePublication(t *testing.T) {
	translator := &translate.FakeProvider{Result: translate.Result{Text: "unused"}}
	usageSink := &recordingUsageSink{}
	service := NewPipelineService(PipelineDependencies{
		Translator: translator, TTS: tts.NewFakeProvider(tts.FakeProviderConfig{}),
		FinalTurns: &recordingFinalSink{}, Usage: usageSink, Audio: &recordingAudioSink{}, Runtime: &recordingRuntimeReporter{},
	})
	turn := testTurn()
	turn.TraceID = ""

	err := service.HandleASRFinal(context.Background(), turn, asr.FinalResult{Text: "你好", SourceLanguage: "zh-CN", Provider: "mock-asr", Model: "v1"})
	if !errors.Is(err, ErrInvalidUsageFact) {
		t.Fatalf("HandleASRFinal() error = %v, want ErrInvalidUsageFact", err)
	}
	if len(usageSink.facts) != 0 || len(translator.Requests()) != 0 {
		t.Fatalf("invalid UsageFact reached dependencies")
	}
}

func TestPipelineCancellationClosesBlockedTTSStream(t *testing.T) {
	stream := &blockingTTSStream{chunks: make(chan tts.AudioChunk), closed: make(chan struct{})}
	provider := &blockingTTSProvider{stream: stream, started: make(chan struct{})}
	service := NewPipelineService(PipelineDependencies{
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

func TestPipelineIgnoresPartialASREvents(t *testing.T) {
	translator := &translate.FakeProvider{Result: translate.Result{Text: "unused"}}
	service := NewPipelineService(PipelineDependencies{Translator: translator, TTS: tts.NewFakeProvider(tts.FakeProviderConfig{}), FinalTurns: &recordingFinalSink{}, Usage: &recordingUsageSink{}, Audio: &recordingAudioSink{}, Runtime: &recordingRuntimeReporter{}})
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

type recordingFinalSink struct{ events []FinalTurnEvent }

func (s *recordingFinalSink) Publish(_ context.Context, event FinalTurnEvent) error {
	s.events = append(s.events, event)
	return nil
}

type recordingUsageSink struct{ facts []UsageFact }

func (s *recordingUsageSink) Publish(_ context.Context, fact UsageFact) error {
	s.facts = append(s.facts, fact)
	return nil
}

type recordingAudioSink struct{ chunks []AudioChunk }

func (s *recordingAudioSink) Publish(_ context.Context, chunk AudioChunk) error {
	s.chunks = append(s.chunks, chunk)
	return nil
}

type recordingRuntimeReporter struct {
	updates []session.RuntimeStateUpdate
}

func (r *recordingRuntimeReporter) ReportRuntimeState(_ context.Context, update session.RuntimeStateUpdate) error {
	r.updates = append(r.updates, update)
	return nil
}

type fixedSpeakerReader struct {
	participantID string
	observation   recordsv1.SpeakerObservation
}

func (r *fixedSpeakerReader) GetProvisionalAttribution(_ context.Context, observation recordsv1.SpeakerObservation) (recordsv1.SpeakerAttribution, error) {
	r.observation = observation
	label := "Speaker 1"
	confidence := .9
	return recordsv1.SpeakerAttribution{
		ParticipantID: &r.participantID, SpeakerCode: "speaker-1", DisplayName: &label,
		Confidence: &confidence, AttributionStatus: recordsv1.AttributionProvisional,
	}, nil
}

type blockingSpeakerReader struct{}

func (blockingSpeakerReader) GetProvisionalAttribution(ctx context.Context, _ recordsv1.SpeakerObservation) (recordsv1.SpeakerAttribution, error) {
	<-ctx.Done()
	return recordsv1.SpeakerAttribution{}, ctx.Err()
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

func (s *blockingTTSStream) Chunks() <-chan tts.AudioChunk { return s.chunks }

func (*blockingTTSStream) Finish(context.Context) (tts.Result, error) {
	return tts.Result{}, errors.New("Finish must not be called while chunks are blocked")
}

func (s *blockingTTSStream) Close() error {
	close(s.closed)
	return nil
}

var _ recordsv1.FinalTurnSink = (*recordingFinalSink)(nil)
var _ UsageFactSink = (*recordingUsageSink)(nil)
var _ AudioChunkSink = (*recordingAudioSink)(nil)
var _ session.RuntimeStateReporter = (*recordingRuntimeReporter)(nil)
var _ recordsv1.SpeakerAttributionReader = (*fixedSpeakerReader)(nil)
var _ tts.Provider = (*blockingTTSProvider)(nil)
var _ tts.Stream = (*blockingTTSStream)(nil)
