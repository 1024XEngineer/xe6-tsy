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

func TestPipelineFinalFlowCarriesTurnID(t *testing.T) {
	translator := &translate.FakeProvider{Result: translate.Result{Text: "hello", Provider: "mock-translate", Model: "v1", InputTokens: 2, OutputTokens: 1}}
	ttsProvider := tts.NewFakeProvider(tts.FakeProviderConfig{
		Chunks: []tts.AudioChunk{{SequenceNo: 1, Data: []byte{1, 2}}, {SequenceNo: 2, Data: []byte{3}}},
		Result: tts.Result{Provider: "mock-tts", Model: "v1", AudioDuration: 250 * time.Millisecond},
	})
	finalSink := &recordingFinalSink{}
	usageSink := &recordingUsageSink{}
	audioSink := &recordingAudioSink{}
	service := NewPipelineService(PipelineDependencies{
		Translator: translator, TTS: ttsProvider, Speakers: fixedSpeakerReader{participantID: "participant-1"},
		FinalTurns: finalSink, Usage: usageSink, Audio: audioSink, VoiceID: "voice-1",
		Now: func() time.Time { return time.Unix(1700000000, 0).UTC() },
	})
	turn := testTurn()
	err := service.HandleASRFinal(context.Background(), turn, asr.FinalResult{
		Text: "你好", SourceLanguage: "zh-CN", Provider: "mock-asr", Model: "v1", AudioDuration: time.Second,
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
	if len(finalSink.events) != 1 || finalSink.events[0].TurnID != turn.ID || finalSink.events[0].TargetLanguage != "en-US" || finalSink.events[0].LanguageConfigVersion != 3 {
		t.Fatalf("FinalTurn events = %#v", finalSink.events)
	}
	if len(usageSink.facts) != 3 || usageSink.facts[0].TurnID != turn.ID || usageSink.facts[1].TurnID != turn.ID || usageSink.facts[2].TurnID != turn.ID {
		t.Fatalf("UsageFacts = %#v", usageSink.facts)
	}
	if len(audioSink.chunks) != 2 || audioSink.chunks[0].TurnID != turn.ID {
		t.Fatalf("audio chunks = %#v", audioSink.chunks)
	}
}

func TestPipelineRejectsUnsupportedSourceBeforeTranslation(t *testing.T) {
	translator := &translate.FakeProvider{Result: translate.Result{Text: "unused"}}
	ttsProvider := tts.NewFakeProvider(tts.FakeProviderConfig{})
	service := NewPipelineService(PipelineDependencies{Translator: translator, TTS: ttsProvider, FinalTurns: &recordingFinalSink{}, Usage: &recordingUsageSink{}, Audio: &recordingAudioSink{}})
	err := service.HandleASRFinal(context.Background(), testTurn(), asr.FinalResult{Text: "bonjour", SourceLanguage: "fr-FR"})
	if !errors.Is(err, ErrUnsupportedSourceLanguage) {
		t.Fatalf("error = %v, want ErrUnsupportedSourceLanguage", err)
	}
	if len(translator.Requests()) != 0 || len(ttsProvider.Requests()) != 0 {
		t.Fatalf("providers were called for unsupported source")
	}
}

func TestPipelineSpeakerTimeoutProducesPendingAttribution(t *testing.T) {
	service := NewPipelineService(PipelineDependencies{
		Translator: &translate.FakeProvider{Result: translate.Result{Text: "hello"}},
		TTS:        tts.NewFakeProvider(tts.FakeProviderConfig{}),
		Speakers:   blockingSpeakerReader{}, FinalTurns: &recordingFinalSink{}, Usage: &recordingUsageSink{}, Audio: &recordingAudioSink{},
		SpeakerTimeout: 5 * time.Millisecond,
	})
	started := time.Now()
	err := service.HandleASRFinal(context.Background(), testTurn(), asr.FinalResult{Text: "你好", SourceLanguage: "zh-CN", ProviderSpeakerID: "speaker-1"})
	if err != nil {
		t.Fatalf("HandleASRFinal() error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > 200*time.Millisecond {
		t.Fatalf("speaker lookup blocked pipeline for %v", elapsed)
	}
	event := service.finalTurns.(*recordingFinalSink).events[0]
	if event.ParticipantID != nil || event.AttributionStatus != "pending" {
		t.Fatalf("FinalTurn attribution = %#v", event)
	}
}

func TestPipelineIgnoresPartialASREvents(t *testing.T) {
	translator := &translate.FakeProvider{Result: translate.Result{Text: "unused"}}
	service := NewPipelineService(PipelineDependencies{Translator: translator, TTS: tts.NewFakeProvider(tts.FakeProviderConfig{}), FinalTurns: &recordingFinalSink{}, Usage: &recordingUsageSink{}, Audio: &recordingAudioSink{}})
	if err := service.HandleASREvent(context.Background(), testTurn(), asr.Event{Type: asr.EventPartial, Text: "你"}); err != nil {
		t.Fatalf("HandleASREvent() error = %v", err)
	}
	if len(translator.Requests()) != 0 {
		t.Fatalf("partial event triggered translation")
	}
}

func testTurn() TurnContext {
	return TurnContext{ID: "turn-1", SessionID: "session-1", AccountID: "account-1", TraceID: "trace-1", SequenceNo: 1, LanguageConfig: session.LanguageConfigSnapshot{SessionID: "session-1", Version: 3, Status: "active", LanguagePairs: []session.LanguagePair{{Source: "zh-CN", Target: "en-US"}}}}
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

type fixedSpeakerReader struct{ participantID string }

func (r fixedSpeakerReader) Resolve(context.Context, string, string) (SpeakerAttribution, error) {
	return SpeakerAttribution{ParticipantID: r.participantID, Confidence: .9}, nil
}

type blockingSpeakerReader struct{}

func (blockingSpeakerReader) Resolve(ctx context.Context, _, _ string) (SpeakerAttribution, error) {
	<-ctx.Done()
	return SpeakerAttribution{}, ctx.Err()
}

var _ FinalTurnSink = (*recordingFinalSink)(nil)
var _ UsageFactSink = (*recordingUsageSink)(nil)
var _ AudioChunkSink = (*recordingAudioSink)(nil)
