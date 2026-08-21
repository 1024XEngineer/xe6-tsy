package runtime

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/asr"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/pipeline"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/session"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/translate"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/tts"
)

func TestModeRouterUsesTurnModeSnapshot(t *testing.T) {
	interpretation := &recordingModeHandler{}
	router := mustModeRouter(t, map[realtimev1.Mode]pipeline.ASRFinalHandler{
		realtimev1.ModeInterpretation: interpretation,
	})
	turn := pipeline.TurnContext{ID: "turn-1", SessionID: "session-1", Mode: pipeline.TurnModeSnapshot{Mode: realtimev1.ModeInterpretation}}
	result := asr.FinalResult{Text: "hello", SourceLanguage: "en-US"}

	if err := router.HandleASRFinal(t.Context(), turn, result); err != nil {
		t.Fatalf("HandleASRFinal() error = %v", err)
	}
	if interpretation.calls != 1 || interpretation.turn.ID != turn.ID ||
		interpretation.turn.SessionID != turn.SessionID || interpretation.result != result {
		t.Fatalf("snapshot handler call = %#v", interpretation)
	}
}

func TestModeRouterDispatchesExplicitRegisteredMode(t *testing.T) {
	interpretation := &recordingModeHandler{}
	assistant := &recordingModeHandler{}
	router := mustModeRouter(t, map[realtimev1.Mode]pipeline.ASRFinalHandler{
		realtimev1.ModeInterpretation: interpretation,
		realtimev1.ModeAssistant:      assistant,
	})

	if err := router.Dispatch(t.Context(), realtimev1.ModeAssistant, pipeline.TurnContext{ID: "turn-1"}, asr.FinalResult{Text: "hello"}); err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if assistant.calls != 1 || interpretation.calls != 0 {
		t.Fatalf("handler calls = assistant %d, interpretation %d", assistant.calls, interpretation.calls)
	}
}

func TestModeRouterRejectsUnavailableModeWithoutFallback(t *testing.T) {
	interpretation := &recordingModeHandler{}
	registrations := map[realtimev1.Mode]pipeline.ASRFinalHandler{
		realtimev1.ModeInterpretation: interpretation,
	}
	router := mustModeRouter(t, registrations)
	registrations[realtimev1.ModeAssistant] = &recordingModeHandler{}

	if err := router.Dispatch(t.Context(), realtimev1.ModeAssistant, pipeline.TurnContext{}, asr.FinalResult{}); !errors.Is(err, ErrModeNotAvailable) {
		t.Fatalf("Dispatch() error = %v, want ErrModeNotAvailable", err)
	}
	if interpretation.calls != 0 {
		t.Fatalf("unavailable mode fell back to interpretation %d times", interpretation.calls)
	}
}

func TestModeRouterValidatesRegistrations(t *testing.T) {
	tests := []struct {
		name     string
		handlers map[realtimev1.Mode]pipeline.ASRFinalHandler
		wantErr  error
	}{
		{
			name:     "empty registrations",
			handlers: map[realtimev1.Mode]pipeline.ASRFinalHandler{},
			wantErr:  ErrModeNotAvailable,
		},
		{
			name: "invalid registered mode",
			handlers: map[realtimev1.Mode]pipeline.ASRFinalHandler{
				realtimev1.ModeInterpretation: &recordingModeHandler{},
				realtimev1.Mode("unknown"):    &recordingModeHandler{},
			},
			wantErr: ErrModeNotAvailable,
		},
		{
			name: "nil handler",
			handlers: map[realtimev1.Mode]pipeline.ASRFinalHandler{
				realtimev1.ModeInterpretation: nil,
			},
			wantErr: ErrDependencyRequired,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := newModeRouter(test.handlers); !errors.Is(err, test.wantErr) {
				t.Fatalf("newModeRouter() error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestModeRouterPropagatesHandlerAndContextErrors(t *testing.T) {
	wantErr := errors.New("handler unavailable")
	handler := &recordingModeHandler{err: wantErr}
	router := mustModeRouter(t, map[realtimev1.Mode]pipeline.ASRFinalHandler{
		realtimev1.ModeInterpretation: handler,
	})
	turn := pipeline.TurnContext{Mode: pipeline.TurnModeSnapshot{Mode: realtimev1.ModeInterpretation}}

	if err := router.HandleASRFinal(t.Context(), turn, asr.FinalResult{}); !errors.Is(err, wantErr) {
		t.Fatalf("HandleASRFinal() error = %v, want %v", err, wantErr)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := router.HandleASRFinal(canceled, turn, asr.FinalResult{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled HandleASRFinal() error = %v, want context.Canceled", err)
	}
	if handler.calls != 1 {
		t.Fatalf("handler calls after canceled dispatch = %d, want 1", handler.calls)
	}
}

func TestModeRouterForwardsAsyncPipelineSettlement(t *testing.T) {
	translator := &blockingPhraseTranslator{started: make(chan struct{}), release: make(chan struct{})}
	phraseTranslations := pipeline.NewPhraseTranslationCoordinator(translator, "mock", noopPhraseSubtitleObserver{}, nil)
	turn := modeTurn(realtimev1.ModeInterpretation, 1)
	turn.ID, turn.AccountID, turn.TraceID, turn.SequenceNo = "turn-async-router", "account-1", "trace-1", 1
	turn.StartedAt = time.Now().UTC()
	turn.LanguageConfig = session.LanguageConfigSnapshot{
		SessionID: "session-1", Version: 1, Status: "active",
		LanguagePairs: []session.LanguagePair{{Source: "zh-CN", Target: "en-US"}},
		OutputRoutes:  []session.OutputRoute{{TargetLanguage: "en-US"}},
	}
	phraseTranslations.StartPhraseSubtitleTurn(turn, "zh-CN")
	phraseTranslations.ObservePhraseSubtitle(t.Context(), realtimev1.PhraseSubtitleEvent{
		Type: realtimev1.PhraseSubtitleTopic, EventVersion: realtimev1.PhraseSubtitleEventVersion,
		SessionID: turn.SessionID, UtteranceID: turn.ID, PhraseSequence: 1, SourceText: "你好，",
		Status: realtimev1.PhraseSubtitleSourceStable, OccurredAt: time.Now().UTC(),
	})
	select {
	case <-translator.started:
	case <-time.After(time.Second):
		t.Fatal("phrase translation did not start")
	}

	finals := &recordingFinalSink{events: make(chan recordsv1.FinalTurnEvent, 1)}
	service := pipeline.NewPipelineService(pipeline.PipelineDependencies{
		Translator: translator, TTS: tts.NewFakeProvider(tts.FakeProviderConfig{}),
		FinalTurns: finals, FinalGate: mustModeCoordinator(t, realtimev1.ModeInterpretation, []realtimev1.Mode{realtimev1.ModeInterpretation}),
		Usage: &recordingUsageSink{}, Audio: &recordingAudioSink{}, Runtime: &recordingRuntimeReporter{},
		PhraseTranslations: phraseTranslations,
	})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(translator.release) }) }
	t.Cleanup(func() {
		release()
		service.Close()
	})
	router := mustModeRouter(t, map[realtimev1.Mode]pipeline.ASRFinalHandler{
		realtimev1.ModeInterpretation: service,
	})

	returned := make(chan error, 1)
	go func() {
		returned <- router.HandleASRFinalAsync(t.Context(), turn, asr.FinalResult{
			Text: "你好，", SourceLanguage: "zh-CN", Provider: "mock-asr", Model: "v1",
		})
	}()
	select {
	case err := <-returned:
		if err != nil {
			t.Fatalf("HandleASRFinalAsync() error = %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("mode router waited for pending phrase translation")
	}
	if calls := translator.Calls(); calls != 1 {
		t.Fatalf("translation calls before phrase completion = %d, want 1", calls)
	}

	release()
	select {
	case event := <-finals.events:
		if event.TranslatedText != "hello" {
			t.Fatalf("FinalTurn = %#v, want reused phrase translation", event)
		}
	case <-time.After(time.Second):
		t.Fatal("async settlement did not publish FinalTurn")
	}
	if calls := translator.Calls(); calls != 1 {
		t.Fatalf("translation calls after settlement = %d, want 1", calls)
	}
}

func TestModeRouterAsyncFallsBackToSynchronousHandler(t *testing.T) {
	handler := &recordingModeHandler{}
	router := mustModeRouter(t, map[realtimev1.Mode]pipeline.ASRFinalHandler{
		realtimev1.ModeAssistant: handler,
	})
	turn := pipeline.TurnContext{Mode: pipeline.TurnModeSnapshot{Mode: realtimev1.ModeAssistant}}
	if err := router.HandleASRFinalAsync(t.Context(), turn, asr.FinalResult{Text: "hello"}); err != nil {
		t.Fatalf("HandleASRFinalAsync() error = %v", err)
	}
	if handler.calls != 1 {
		t.Fatalf("synchronous fallback calls = %d, want 1", handler.calls)
	}
}

func mustModeRouter(
	t *testing.T,
	handlers map[realtimev1.Mode]pipeline.ASRFinalHandler,
) *modeRouter {
	t.Helper()
	router, err := newModeRouter(handlers)
	if err != nil {
		t.Fatalf("newModeRouter() error = %v", err)
	}
	return router
}

type recordingModeHandler struct {
	calls  int
	turn   pipeline.TurnContext
	result asr.FinalResult
	err    error
}

func (h *recordingModeHandler) HandleASRFinal(_ context.Context, turn pipeline.TurnContext, result asr.FinalResult) error {
	h.calls++
	h.turn = turn
	h.result = result
	return h.err
}

type noopPhraseSubtitleObserver struct{}

func (noopPhraseSubtitleObserver) ObservePhraseSubtitle(context.Context, realtimev1.PhraseSubtitleEvent) {
}

type blockingPhraseTranslator struct {
	mu      sync.Mutex
	calls   int
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (t *blockingPhraseTranslator) Translate(ctx context.Context, _ translate.Request) (translate.Result, error) {
	t.mu.Lock()
	t.calls++
	t.mu.Unlock()
	t.once.Do(func() { close(t.started) })
	select {
	case <-t.release:
		return translate.Result{Text: "hello", Provider: "mock", Model: "v1", InputTokens: 1}, nil
	case <-ctx.Done():
		return translate.Result{}, ctx.Err()
	}
}

func (t *blockingPhraseTranslator) Calls() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.calls
}

var _ pipeline.AsyncASRFinalHandler = (*modeRouter)(nil)
var _ pipeline.PhraseSubtitleObserver = noopPhraseSubtitleObserver{}
var _ translate.Provider = (*blockingPhraseTranslator)(nil)
