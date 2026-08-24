package pipeline

import (
	"context"
	"sync"
	"testing"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/session"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/translate"
)

func TestPhraseSubtitleProcessorPublishesOrderedPunctuationAndFinalTail(t *testing.T) {
	t.Parallel()
	observer := &recordingPhraseSubtitleObserver{}
	processor := NewPhraseSubtitleProcessor(observer, PhraseStabilizerOptions{StableAfter: time.Hour})
	now := time.Unix(1700000000, 0).UTC()
	processor.now = func() time.Time { return now }
	turn := TurnContext{ID: "turn-1", SessionID: "session-1"}
	processor.Start(turn, "zh-CN")
	processor.Observe(context.Background(), realtimev1.ASRPartialEvent{TurnID: turn.ID, Text: "你好，世界", OccurredAt: now})
	processor.Flush(context.Background(), turn, "你好，世界")

	if got := observer.Events(); len(got) != 2 || got[0].SourceText != "你好，" || got[1].SourceText != "世界" || got[1].PhraseSequence != 2 {
		t.Fatalf("events = %#v", got)
	}
}

func TestPhraseSubtitleProcessorDiscardsLatePartialsAfterFlush(t *testing.T) {
	t.Parallel()
	observer := &recordingPhraseSubtitleObserver{}
	processor := NewPhraseSubtitleProcessor(observer, PhraseStabilizerOptions{StableAfter: time.Hour})
	turn := TurnContext{ID: "turn-1", SessionID: "session-1"}
	processor.Start(turn, "zh-CN")
	processor.Flush(context.Background(), turn, "你好")
	processor.Observe(context.Background(), realtimev1.ASRPartialEvent{TurnID: turn.ID, Text: "你好，"})

	if got := observer.Events(); len(got) != 1 || got[0].SourceText != "你好" {
		t.Fatalf("events = %#v", got)
	}
}

func TestPhraseSubtitleProcessorForwardsDiscardAfterFlush(t *testing.T) {
	coordinator := NewPhraseTranslationCoordinator(phraseTranslateFunc(func(ctx context.Context, _ translate.Request) (translate.Result, error) {
		return translate.Result{}, ctx.Err()
	}), "mock", &recordingPhraseSubtitleObserver{}, nil)
	processor := NewPhraseSubtitleProcessor(coordinator, PhraseStabilizerOptions{})
	turn := TurnContext{ID: "turn-1", SessionID: "session-1", LanguageConfig: session.LanguageConfigSnapshot{LanguagePairs: []session.LanguagePair{{Source: "zh-CN", Target: "en-US"}}}}
	processor.Start(turn, "zh-CN")
	processor.Flush(context.Background(), turn, "嗯")
	processor.Discard(turn.ID)

	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	if len(coordinator.utterances) != 0 {
		t.Fatalf("coordinator utterances = %d, want 0", len(coordinator.utterances))
	}
}

func TestPhraseSubtitleProcessorFinalFlushKeepsTailSourceOnly(t *testing.T) {
	started := make(chan struct{}, 1)
	coordinator := NewPhraseTranslationCoordinator(phraseTranslateFunc(func(context.Context, translate.Request) (translate.Result, error) {
		started <- struct{}{}
		return translate.Result{Text: "unexpected", Provider: "mock", Model: "v1"}, nil
	}), "mock", &recordingPhraseSubtitleObserver{}, nil)
	processor := NewPhraseSubtitleProcessor(coordinator, PhraseStabilizerOptions{})
	turn := TurnContext{ID: "turn-1", SessionID: "session-1", LanguageConfig: session.LanguageConfigSnapshot{LanguagePairs: []session.LanguagePair{{Source: "zh-CN", Target: "en-US"}}}}
	processor.Start(turn, "zh-CN")
	processor.Flush(context.Background(), turn, "尾段")
	select {
	case <-started:
		t.Fatal("final flush tail started phrase translation")
	case <-time.After(20 * time.Millisecond):
	}
	summary, _, _, ok, err := coordinator.FinalizePhraseSubtitleTurn(context.Background(), turn, "尾段")
	if err != nil || !ok || len(summary.ResidualSegments) != 1 || summary.ResidualSegments[0] != "尾段" {
		t.Fatalf("final tail settlement = %#v, %v, %v", summary, ok, err)
	}
}

func TestPhraseSubtitleProcessorLocksLanguageOnFirstConfirmedText(t *testing.T) {
	observer := &recordingPhraseLifecycleObserver{}
	processor := NewPhraseSubtitleProcessor(observer, PhraseStabilizerOptions{StableAfter: time.Hour})
	turn := TurnContext{ID: "turn-language", SessionID: "session-language"}
	processor.Start(turn, "")

	processor.Observe(context.Background(), realtimev1.ASRPartialEvent{
		TurnID: turn.ID, Text: "", Stash: "Thank", SourceLanguage: "en-US",
	})
	if got := observer.Started(); len(got) != 0 {
		t.Fatalf("stash-only language started route: %#v", got)
	}

	processor.Observe(context.Background(), realtimev1.ASRPartialEvent{
		TurnID: turn.ID, Text: "由于", Stash: "宏观因素", SourceLanguage: "zh-CN",
	})
	processor.Observe(context.Background(), realtimev1.ASRPartialEvent{
		TurnID: turn.ID, Text: "由于宏观", Stash: "因素", SourceLanguage: "en-US",
	})
	got := observer.Started()
	if len(got) != 1 || got[0].language != "zh-CN" {
		t.Fatalf("started routes = %#v, want one zh-CN route", got)
	}
}

type recordingPhraseSubtitleObserver struct {
	mu     sync.Mutex
	events []realtimev1.PhraseSubtitleEvent
}

func (o *recordingPhraseSubtitleObserver) ObservePhraseSubtitle(_ context.Context, event realtimev1.PhraseSubtitleEvent) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.events = append(o.events, event)
}

func (o *recordingPhraseSubtitleObserver) Events() []realtimev1.PhraseSubtitleEvent {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]realtimev1.PhraseSubtitleEvent(nil), o.events...)
}

var _ PhraseSubtitleObserver = (*recordingPhraseSubtitleObserver)(nil)

type recordingPhraseLifecycleObserver struct {
	mu      sync.Mutex
	started []struct {
		turnID   string
		language string
	}
}

func (o *recordingPhraseLifecycleObserver) ObservePhraseSubtitle(context.Context, realtimev1.PhraseSubtitleEvent) {
}

func (o *recordingPhraseLifecycleObserver) StartPhraseSubtitleTurn(turn TurnContext, language string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.started = append(o.started, struct {
		turnID   string
		language string
	}{turn.ID, language})
}

func (o *recordingPhraseLifecycleObserver) DiscardPhraseSubtitleTurn(string) {}

func (o *recordingPhraseLifecycleObserver) Started() []struct {
	turnID   string
	language string
} {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]struct {
		turnID   string
		language string
	}(nil), o.started...)
}
