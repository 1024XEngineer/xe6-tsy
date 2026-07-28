package pipeline

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

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
	service := NewPipelineService(PipelineDependencies{
		Translator: translator, TTS: ttsProvider, FinalTurns: finalSink,
		Usage: usageSink, Audio: audioSink, Runtime: runtimeReporter, VoiceID: "voice-1",
	})
	processor := NewTurnProcessor(TurnProcessorDependencies{
		ASR: asrProvider,
		Opener: NewTurnOpener(NewMemoryTurnAllocator(), &fakeLanguageConfigReader{snapshot: session.LanguageConfigSnapshot{
			SessionID: "session-1", Version: 3, Status: "active",
			LanguagePairs: []session.LanguagePair{{Source: "zh-CN", Target: "en-US"}},
		}}),
		Pipeline: service,
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
	if len(usageSink.facts) != 3 {
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

func TestTurnProcessorPropagatesUsageAcceptanceFailure(t *testing.T) {
	wantErr := errors.New("usage sink unavailable")
	service := NewPipelineService(PipelineDependencies{
		Translator: &translate.FakeProvider{Result: translate.Result{Text: "hello", Provider: "mock-translate", Model: "v1"}},
		TTS:        tts.NewFakeProvider(tts.FakeProviderConfig{}),
		FinalTurns: &recordingFinalSink{}, Usage: rejectingUsageSink{err: wantErr}, Audio: &recordingAudioSink{}, Runtime: &recordingRuntimeReporter{},
	})
	processor := NewTurnProcessor(TurnProcessorDependencies{
		ASR: asr.NewFakeProvider(asr.FakeProviderConfig{Final: asr.FinalResult{Text: "你好", SourceLanguage: "zh-CN", Provider: "mock-asr", Model: "v1"}}),
		Opener: NewTurnOpener(NewMemoryTurnAllocator(), &fakeLanguageConfigReader{snapshot: session.LanguageConfigSnapshot{
			SessionID: "session-1", Version: 1, Status: "active",
			LanguagePairs: []session.LanguagePair{{Source: "zh-CN", Target: "en-US"}},
		}}),
		Pipeline: service,
	})

	_, err := processor.ProcessAudio(context.Background(), TurnProcessRequest{SessionID: "session-1", AccountID: "account-1", TraceID: "trace-1"})
	if !errors.Is(err, wantErr) {
		t.Fatalf("ProcessAudio() error = %v, want %v", err, wantErr)
	}
}

func TestTurnProcessorConsumesASREventsBeforePushReturns(t *testing.T) {
	stream := &pushEventStream{
		events:      make(chan asr.Event),
		partialSent: make(chan struct{}),
		result:      asr.FinalResult{Text: "你好", SourceLanguage: "zh-CN", Provider: "mock-asr", Model: "v1"},
	}
	processor := NewTurnProcessor(TurnProcessorDependencies{
		ASR: &pushEventProvider{stream: stream},
		Opener: NewTurnOpener(NewMemoryTurnAllocator(), &fakeLanguageConfigReader{snapshot: session.LanguageConfigSnapshot{
			SessionID: "session-1", Version: 1, Status: "active",
			LanguagePairs: []session.LanguagePair{{Source: "zh-CN", Target: "en-US"}},
		}}),
		Pipeline: NewPipelineService(PipelineDependencies{
			Translator: &translate.FakeProvider{Result: translate.Result{Text: "hello", Provider: "mock-translate", Model: "v1"}},
			TTS:        tts.NewFakeProvider(tts.FakeProviderConfig{Result: tts.Result{Provider: "mock-tts", Model: "v1"}}),
			FinalTurns: &recordingFinalSink{},
			Usage:      &recordingUsageSink{}, Audio: &recordingAudioSink{}, Runtime: &recordingRuntimeReporter{},
		}),
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

type pushEventProvider struct{ stream *pushEventStream }

func (p *pushEventProvider) StartStream(context.Context, asr.StreamRequest) (asr.Stream, error) {
	return p.stream, nil
}

type pushEventStream struct {
	events      chan asr.Event
	partialSent chan struct{}
	partialOnce sync.Once
	closeOnce   sync.Once
	result      asr.FinalResult
}

func (s *pushEventStream) PushAudio(ctx context.Context, _ []byte) error {
	var err error
	s.partialOnce.Do(func() {
		select {
		case s.events <- asr.Event{Type: asr.EventPartial, Text: "你"}:
			close(s.partialSent)
		case <-ctx.Done():
			err = ctx.Err()
		}
	})
	return err
}

func (s *pushEventStream) Events() <-chan asr.Event { return s.events }

func (s *pushEventStream) Finish(context.Context) (asr.FinalResult, error) {
	s.closeOnce.Do(func() { close(s.events) })
	return s.result, nil
}

func (s *pushEventStream) Close() error {
	s.closeOnce.Do(func() { close(s.events) })
	return nil
}

var _ asr.Provider = (*pushEventProvider)(nil)
var _ asr.Stream = (*pushEventStream)(nil)

type rejectingUsageSink struct{ err error }

func (s rejectingUsageSink) Publish(context.Context, UsageFact) error { return s.err }

var _ UsageFactSink = rejectingUsageSink{}
