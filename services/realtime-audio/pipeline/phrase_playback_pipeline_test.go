package pipeline

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/asr"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/session"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/translate"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/tts"
)

func TestPipelineSkipsFullFinalTTSWhenPhrasesCoverTurn(t *testing.T) {
	ttsProvider := &recordingTTSProvider{}
	audio := &recordingPhraseAudio{}
	scheduler := newTestPhrasePlaybackScheduler(ttsProvider, audio)
	coordinator := NewPhraseTranslationCoordinator(phraseTranslateFunc(func(context.Context, translate.Request) (translate.Result, error) {
		return translate.Result{Text: "hello", Provider: "phrase", Model: "v1", InputTokens: 1}, nil
	}), "phrase", &recordingPhraseSubtitleObserver{}, nil)
	coordinator.SetPhrasePlaybackScheduler(scheduler)
	service := newTestPipelineService(PipelineDependencies{
		Translator:         coordinator.translator,
		TTS:                ttsProvider,
		FinalTurns:         &recordingFinalSink{},
		Usage:              &recordingUsageSink{},
		Audio:              audio,
		Runtime:            phraseRuntimeReporter{},
		PhraseTranslations: coordinator,
		PhrasePlayback:     scheduler,
	})
	turn := testTurn()
	turn.LanguageConfig.OutputRoutes = []session.OutputRoute{{TargetLanguage: "en-US", TTSEnabled: true}}
	coordinator.StartPhraseSubtitleTurn(turn, "zh-CN")
	coordinator.ObservePhraseSubtitle(context.Background(), stablePhraseEvent(turn, 1, "你好"))
	waitForPhraseTranslation(t, coordinator, turn.ID)

	if err := service.HandleASRFinal(context.Background(), turn, asr.FinalResult{Text: "你好", SourceLanguage: "zh-CN"}); err != nil {
		t.Fatalf("HandleASRFinal() error = %v", err)
	}
	if !audio.waitFor(1, time.Second) {
		t.Fatal("phrase playback did not start")
	}
	requests := ttsProvider.requests()
	if len(requests) != 1 || requests[0].Text != "hello" {
		t.Fatalf("TTS requests = %#v, want only the translated phrase", requests)
	}
}

func TestPipelineQueuesFinalResidualAfterStablePhrase(t *testing.T) {
	ttsProvider := &recordingTTSProvider{}
	audio := &recordingPhraseAudio{}
	scheduler := newTestPhrasePlaybackScheduler(ttsProvider, audio)
	coordinator := NewPhraseTranslationCoordinator(phraseTranslateFunc(func(_ context.Context, request translate.Request) (translate.Result, error) {
		text := "hello"
		if request.Text == "尾段" {
			text = "tail"
		}
		return translate.Result{Text: text, Provider: "phrase", Model: "v1", InputTokens: 1}, nil
	}), "phrase", &recordingPhraseSubtitleObserver{}, nil)
	coordinator.SetPhrasePlaybackScheduler(scheduler)
	service := newTestPipelineService(PipelineDependencies{
		Translator:         coordinator.translator,
		TTS:                ttsProvider,
		FinalTurns:         &recordingFinalSink{},
		Usage:              &recordingUsageSink{},
		Audio:              audio,
		Runtime:            phraseRuntimeReporter{},
		PhraseTranslations: coordinator,
		PhrasePlayback:     scheduler,
	})
	turn := testTurn()
	turn.LanguageConfig.OutputRoutes = []session.OutputRoute{{TargetLanguage: "en-US", TTSEnabled: true}}
	coordinator.StartPhraseSubtitleTurn(turn, "zh-CN")
	coordinator.ObservePhraseSubtitle(context.Background(), stablePhraseEvent(turn, 1, "你好"))
	waitForPhraseTranslation(t, coordinator, turn.ID)
	coordinator.BeginPhraseSubtitleFinalFlush(turn.ID)
	coordinator.ObservePhraseSubtitle(context.Background(), stablePhraseEvent(turn, 2, "尾段"))
	coordinator.EndPhraseSubtitleFinalFlush(turn.ID)

	if err := service.HandleASRFinal(context.Background(), turn, asr.FinalResult{Text: "你好尾段", SourceLanguage: "zh-CN"}); err != nil {
		t.Fatalf("HandleASRFinal() error = %v", err)
	}
	if !audio.waitFor(2, time.Second) {
		t.Fatal("final residual playback did not follow the phrase")
	}
	requests := ttsProvider.requests()
	if len(requests) != 2 || requests[0].Text != "hello" || requests[1].Text != "tail" {
		t.Fatalf("TTS requests = %#v, want ordered phrase and residual", requests)
	}
}

func TestPhraseTranslationInitializesPlaybackBeforeFirstPhraseCanEnqueue(t *testing.T) {
	scheduler := newStartupPhrasePlaybackScheduler()
	coordinator := NewPhraseTranslationCoordinator(phraseTranslateFunc(func(context.Context, translate.Request) (translate.Result, error) {
		return translate.Result{Text: "hello", Provider: "phrase", Model: "v1"}, nil
	}), "phrase", &recordingPhraseSubtitleObserver{}, nil)
	coordinator.SetPhrasePlaybackScheduler(scheduler)
	turn := testTurn()
	turn.LanguageConfig.OutputRoutes = []session.OutputRoute{{TargetLanguage: "en-US", TTSEnabled: true}}
	started := make(chan struct{})
	go func() {
		coordinator.StartPhraseSubtitleTurn(turn, "zh-CN")
		close(started)
	}()
	<-scheduler.resetStarted

	observed := make(chan struct{})
	go func() {
		coordinator.ObservePhraseSubtitle(context.Background(), stablePhraseEvent(turn, 1, "你好"))
		close(observed)
	}()
	select {
	case <-observed:
		t.Fatal("first phrase was observed before playback state initialization completed")
	case <-time.After(20 * time.Millisecond):
	}
	close(scheduler.releaseReset)
	<-started
	<-observed

	deadline := time.Now().Add(time.Second)
	for len(scheduler.enqueued()) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if accepted := scheduler.enqueued(); len(accepted) != 1 {
		t.Fatalf("enqueued phrases = %d, want 1", len(accepted))
	}
}

func newTestPhrasePlaybackScheduler(provider tts.Provider, audio *recordingPhraseAudio) *PhrasePlaybackSchedulerService {
	return NewPhrasePlaybackScheduler(PhrasePlaybackSchedulerDependencies{
		Speech: NewSpeechOutput(SpeechOutputDependencies{
			TTS: provider, Audio: audio, Runtime: phraseRuntimeReporter{}, Provider: "fake",
		}),
		Audio: audio,
	})
}

type startupPhrasePlaybackScheduler struct {
	resetStarted chan struct{}
	releaseReset chan struct{}
	once         sync.Once
	mu           sync.Mutex
	ready        bool
	requests     []PhrasePlaybackRequest
}

func newStartupPhrasePlaybackScheduler() *startupPhrasePlaybackScheduler {
	return &startupPhrasePlaybackScheduler{
		resetStarted: make(chan struct{}), releaseReset: make(chan struct{}),
	}
}

func (s *startupPhrasePlaybackScheduler) ResetUtterance(string, string) {
	s.once.Do(func() { close(s.resetStarted) })
	<-s.releaseReset
	s.mu.Lock()
	s.ready = true
	s.mu.Unlock()
}

func (s *startupPhrasePlaybackScheduler) Enqueue(request PhrasePlaybackRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ready {
		return ErrPhrasePlaybackClosed
	}
	s.requests = append(s.requests, request)
	return nil
}

func (*startupPhrasePlaybackScheduler) InterruptCurrent(context.Context, string, string) error {
	return nil
}

func (*startupPhrasePlaybackScheduler) Stop(context.Context, string) error { return nil }

func (s *startupPhrasePlaybackScheduler) enqueued() []PhrasePlaybackRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]PhrasePlaybackRequest(nil), s.requests...)
}

func waitForPhraseTranslation(t *testing.T, coordinator *PhraseTranslationCoordinator, turnID string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		coordinator.mu.Lock()
		utterance := coordinator.utterances[turnID]
		done := utterance != nil && allPhraseTranslationsDone(utterance)
		coordinator.mu.Unlock()
		if done {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("phrase translation did not finish")
}
