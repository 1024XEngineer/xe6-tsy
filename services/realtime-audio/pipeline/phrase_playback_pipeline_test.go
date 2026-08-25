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

func TestPipelineQueuesNoPhraseFinalBehindActivePreviousTurn(t *testing.T) {
	provider := newPhraseBlockingTTSProvider()
	defer provider.release()
	audio := &recordingPhraseAudio{}
	scheduler := newTestPhrasePlaybackScheduler(provider, audio)
	translator := phraseTranslateFunc(func(_ context.Context, request translate.Request) (translate.Result, error) {
		translations := map[string]string{"第一句": "first", "第二句": "second"}
		return translate.Result{Text: translations[request.Text], Provider: "phrase", Model: "v1", InputTokens: 1}, nil
	})
	coordinator := NewPhraseTranslationCoordinator(translator, "phrase", &recordingPhraseSubtitleObserver{}, nil)
	coordinator.SetPhrasePlaybackScheduler(scheduler)
	service := newTestPipelineService(PipelineDependencies{
		Translator: translator, TTS: provider,
		FinalTurns: &recordingFinalSink{}, Usage: &recordingUsageSink{},
		Audio: audio, Runtime: phraseRuntimeReporter{},
		PhraseTranslations: coordinator, PhrasePlayback: scheduler,
	})
	first := testTurn()
	first.LanguageConfig.OutputRoutes = []session.OutputRoute{{TargetLanguage: "en-US", TTSEnabled: true}}
	coordinator.StartPhraseSubtitleTurn(first, "zh-CN")
	coordinator.ObservePhraseSubtitle(context.Background(), stablePhraseEvent(first, 1, "第一句"))
	waitForPhraseTranslation(t, coordinator, first.ID)
	select {
	case <-provider.started:
	case <-time.After(time.Second):
		t.Fatal("first Turn TTS did not start")
	}
	if err := service.HandleASRFinal(context.Background(), first, asr.FinalResult{Text: "第一句", SourceLanguage: "zh-CN"}); err != nil {
		t.Fatalf("first HandleASRFinal() error = %v", err)
	}

	second := testTurn()
	second.ID, second.TraceID, second.SequenceNo = "turn-2", "trace-2", 2
	second.LanguageConfig.OutputRoutes = []session.OutputRoute{{TargetLanguage: "en-US", TTSEnabled: true}}
	coordinator.StartPhraseSubtitleTurn(second, "zh-CN")
	secondDone := make(chan error, 1)
	go func() {
		secondDone <- service.HandleASRFinal(context.Background(), second, asr.FinalResult{Text: "第二句", SourceLanguage: "zh-CN"})
	}()
	select {
	case err := <-secondDone:
		if err != nil {
			t.Fatalf("second HandleASRFinal() error = %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("second final blocked behind active TTS instead of joining scheduler")
	}
	if requests := provider.requests(); len(requests) != 1 || requests[0].Text != "first" {
		t.Fatalf("concurrent TTS requests before first playback completed = %#v", requests)
	}

	provider.release()
	if !audio.waitFor(2, time.Second) {
		t.Fatal("queued second Turn did not play after first Turn")
	}
	requests := provider.requests()
	if len(requests) != 2 || requests[0].Text != "first" || requests[1].Text != "second" {
		t.Fatalf("serialized TTS requests = %#v, want first then second", requests)
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

func TestPipelineQueuesUnconfirmedFinalSuffixWithoutRepeatingStablePrefix(t *testing.T) {
	ttsProvider := &recordingTTSProvider{}
	audio := &recordingPhraseAudio{}
	scheduler := newTestPhrasePlaybackScheduler(ttsProvider, audio)
	coordinator := NewPhraseTranslationCoordinator(phraseTranslateFunc(func(_ context.Context, request translate.Request) (translate.Result, error) {
		text := "hello"
		if request.Text == "，尾段" {
			text = "tail"
		}
		return translate.Result{Text: text, Provider: "phrase", Model: "v1", InputTokens: 1}, nil
	}), "phrase", &recordingPhraseSubtitleObserver{}, nil)
	coordinator.SetPhrasePlaybackScheduler(scheduler)
	finals := &recordingFinalSink{}
	service := newTestPipelineService(PipelineDependencies{
		Translator:         coordinator.translator,
		TTS:                ttsProvider,
		FinalTurns:         finals,
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

	if err := service.HandleASRFinal(context.Background(), turn, asr.FinalResult{Text: "你好，尾段", SourceLanguage: "zh-CN"}); err != nil {
		t.Fatalf("HandleASRFinal() error = %v", err)
	}
	if !audio.waitFor(2, time.Second) {
		t.Fatal("final residual playback did not follow the stable phrase")
	}
	requests := ttsProvider.requests()
	if len(requests) != 2 || requests[0].Text != "hello" || requests[1].Text != "tail" {
		t.Fatalf("TTS requests = %#v, want stable prefix followed by residual only", requests)
	}
	if len(finals.events) != 1 || finals.events[0].TranslatedText != "hellotail" {
		t.Fatalf("FinalTurns = %#v, want merged phrase and residual translation", finals.events)
	}
}

func TestPipelineFinalWaitsForConcurrentPhrasePlaybackAdmission(t *testing.T) {
	providerStarted := make(chan struct{})
	releaseProvider := make(chan struct{})
	translator := phraseTranslateFunc(func(context.Context, translate.Request) (translate.Result, error) {
		close(providerStarted)
		<-releaseProvider
		return translate.Result{Text: "hello", Provider: "phrase", Model: "v1", InputTokens: 1}, nil
	})
	scheduler := newBlockingEnqueuePhrasePlaybackScheduler()
	coordinator := NewPhraseTranslationCoordinator(translator, "phrase", &recordingPhraseSubtitleObserver{}, nil)
	coordinator.SetPhrasePlaybackScheduler(scheduler)
	directTTS := &recordingTTSProvider{}
	service := newTestPipelineService(PipelineDependencies{
		Translator: translator, TTS: directTTS,
		FinalTurns: &recordingFinalSink{}, Usage: &recordingUsageSink{},
		Audio: &recordingPhraseAudio{}, Runtime: phraseRuntimeReporter{},
		PhraseTranslations: coordinator, PhrasePlayback: scheduler,
	})
	turn := testTurn()
	turn.LanguageConfig.OutputRoutes = []session.OutputRoute{{TargetLanguage: "en-US", TTSEnabled: true}}
	coordinator.StartPhraseSubtitleTurn(turn, "zh-CN")
	coordinator.ObservePhraseSubtitle(context.Background(), stablePhraseEvent(turn, 1, "你好"))
	<-providerStarted

	finalDone := make(chan error, 1)
	go func() {
		finalDone <- service.HandleASRFinal(context.Background(), turn, asr.FinalResult{Text: "你好", SourceLanguage: "zh-CN"})
	}()
	close(releaseProvider)
	<-scheduler.enqueueStarted
	select {
	case err := <-finalDone:
		t.Fatalf("HandleASRFinal() returned before phrase enqueue completed: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(scheduler.releaseEnqueue)
	if err := <-finalDone; err != nil {
		t.Fatalf("HandleASRFinal() error = %v", err)
	}
	requests := scheduler.enqueued()
	if len(requests) != 1 || requests[0].Text != "hello" || requests[0].Final {
		t.Fatalf("phrase playback requests = %#v, want one live phrase", requests)
	}
	if requests := directTTS.requests(); len(requests) != 0 {
		t.Fatalf("direct final TTS requests = %#v, want none", requests)
	}
}

func TestPipelineRetriesOnlyUnacceptedPhrasePlaybackAtFinal(t *testing.T) {
	scheduler := &rejectNthPhrasePlaybackScheduler{rejectAttempt: 2}
	translator := phraseTranslateFunc(func(_ context.Context, request translate.Request) (translate.Result, error) {
		translations := map[string]string{"你好": "hello", "世界": "world"}
		return translate.Result{Text: translations[request.Text], Provider: "phrase", Model: "v1", InputTokens: 1}, nil
	})
	coordinator := NewPhraseTranslationCoordinator(translator, "phrase", &recordingPhraseSubtitleObserver{}, nil)
	coordinator.SetPhrasePlaybackScheduler(scheduler)
	directTTS := &recordingTTSProvider{}
	finals := &recordingFinalSink{}
	service := newTestPipelineService(PipelineDependencies{
		Translator: translator, TTS: directTTS,
		FinalTurns: finals, Usage: &recordingUsageSink{},
		Audio: &recordingPhraseAudio{}, Runtime: phraseRuntimeReporter{},
		PhraseTranslations: coordinator, PhrasePlayback: scheduler,
	})
	turn := testTurn()
	turn.LanguageConfig.OutputRoutes = []session.OutputRoute{{TargetLanguage: "en-US", TTSEnabled: true}}
	coordinator.StartPhraseSubtitleTurn(turn, "zh-CN")
	coordinator.ObservePhraseSubtitle(context.Background(), stablePhraseEvent(turn, 1, "你好"))
	coordinator.ObservePhraseSubtitle(context.Background(), stablePhraseEvent(turn, 2, "世界"))
	waitForPhraseTranslation(t, coordinator, turn.ID)

	if err := service.HandleASRFinal(context.Background(), turn, asr.FinalResult{Text: "你好世界", SourceLanguage: "zh-CN"}); err != nil {
		t.Fatalf("HandleASRFinal() error = %v", err)
	}
	requests := scheduler.enqueued()
	if len(requests) != 3 || requests[0].Text != "hello" || requests[1].Text != "world" || requests[2].Text != "world" || !requests[2].Final {
		t.Fatalf("phrase playback requests = %#v, want accepted prefix, rejected suffix, final suffix retry", requests)
	}
	if len(finals.events) != 1 || finals.events[0].TranslatedText != "helloworld" {
		t.Fatalf("FinalTurns = %#v, want complete merged translation", finals.events)
	}
	if requests := directTTS.requests(); len(requests) != 0 {
		t.Fatalf("direct final TTS requests = %#v, want none", requests)
	}
}

func TestPipelineRetriesOnlyUnacceptedValidatedStreamChunksAtFinal(t *testing.T) {
	scheduler := &rejectNthPhrasePlaybackScheduler{rejectAttempt: 2}
	translator := streamPhraseTranslateFunc(func(context.Context, translate.Request) (translate.Result, error) {
		return translate.Result{Text: "First,Second.", Provider: "phrase", Model: "v1", InputTokens: 1}, nil
	})
	coordinator := NewPhraseTranslationCoordinator(translator, "phrase", &recordingPhraseSubtitleObserver{}, nil)
	coordinator.SetPhrasePlaybackScheduler(scheduler)
	directTTS := &recordingTTSProvider{}
	service := newTestPipelineService(PipelineDependencies{
		Translator: translator, TTS: directTTS,
		FinalTurns: &recordingFinalSink{}, Usage: &recordingUsageSink{},
		Audio: &recordingPhraseAudio{}, Runtime: phraseRuntimeReporter{},
		PhraseTranslations: coordinator, PhrasePlayback: scheduler,
	})
	turn := testTurn()
	turn.LanguageConfig.OutputRoutes = []session.OutputRoute{{TargetLanguage: "en-US", TTSEnabled: true}}
	coordinator.StartPhraseSubtitleTurn(turn, "zh-CN")
	coordinator.ObservePhraseSubtitle(context.Background(), stablePhraseEvent(turn, 1, "你好"))
	waitForPhraseTranslation(t, coordinator, turn.ID)

	if err := service.HandleASRFinal(context.Background(), turn, asr.FinalResult{Text: "你好", SourceLanguage: "zh-CN"}); err != nil {
		t.Fatalf("HandleASRFinal() error = %v", err)
	}
	requests := scheduler.enqueued()
	if len(requests) != 3 || requests[0].Text != "First," || requests[1].Text != "Second." || requests[2].Text != "Second." || !requests[2].Final {
		t.Fatalf("phrase playback requests = %#v, want only rejected validated chunk retried at final", requests)
	}
	if requests := directTTS.requests(); len(requests) != 0 {
		t.Fatalf("direct final TTS requests = %#v, want none", requests)
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

type blockingEnqueuePhrasePlaybackScheduler struct {
	enqueueStarted chan struct{}
	releaseEnqueue chan struct{}
	once           sync.Once
	mu             sync.Mutex
	requests       []PhrasePlaybackRequest
}

func newBlockingEnqueuePhrasePlaybackScheduler() *blockingEnqueuePhrasePlaybackScheduler {
	return &blockingEnqueuePhrasePlaybackScheduler{enqueueStarted: make(chan struct{}), releaseEnqueue: make(chan struct{})}
}

func (*blockingEnqueuePhrasePlaybackScheduler) ResetUtterance(string, string) {}

func (s *blockingEnqueuePhrasePlaybackScheduler) Enqueue(request PhrasePlaybackRequest) bool {
	s.once.Do(func() { close(s.enqueueStarted) })
	<-s.releaseEnqueue
	s.mu.Lock()
	s.requests = append(s.requests, request)
	s.mu.Unlock()
	return true
}

func (*blockingEnqueuePhrasePlaybackScheduler) InterruptCurrent(context.Context, string, string) error {
	return nil
}

func (*blockingEnqueuePhrasePlaybackScheduler) Stop(context.Context, string) error { return nil }

func (s *blockingEnqueuePhrasePlaybackScheduler) enqueued() []PhrasePlaybackRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]PhrasePlaybackRequest(nil), s.requests...)
}

type rejectNthPhrasePlaybackScheduler struct {
	mu            sync.Mutex
	rejectAttempt int
	requests      []PhrasePlaybackRequest
}

func (*rejectNthPhrasePlaybackScheduler) ResetUtterance(string, string) {}

func (s *rejectNthPhrasePlaybackScheduler) Enqueue(request PhrasePlaybackRequest) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, request)
	return len(s.requests) != s.rejectAttempt
}

func (*rejectNthPhrasePlaybackScheduler) InterruptCurrent(context.Context, string, string) error {
	return nil
}

func (*rejectNthPhrasePlaybackScheduler) Stop(context.Context, string) error { return nil }

func (s *rejectNthPhrasePlaybackScheduler) enqueued() []PhrasePlaybackRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]PhrasePlaybackRequest(nil), s.requests...)
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

func (s *startupPhrasePlaybackScheduler) Enqueue(request PhrasePlaybackRequest) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ready {
		return false
	}
	s.requests = append(s.requests, request)
	return true
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
