package pipeline

import (
	"context"
	"testing"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/session"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/translate"
)

func TestPhraseTranslationCoordinatorPublishesAndReusesOrderedPhrases(t *testing.T) {
	observer := &recordingPhraseSubtitleObserver{}
	coordinator := NewPhraseTranslationCoordinator(phraseTranslateFunc(func(_ context.Context, request translate.Request) (translate.Result, error) {
		return translate.Result{Text: "en-" + request.Text, Provider: "mock", Model: "v1", InputTokens: 1, OutputTokens: 2, CostAmount: "0.10", Currency: "USD"}, nil
	}), "mock", observer, func() time.Time { return time.Unix(2, 0).UTC() })
	turn := TurnContext{ID: "turn-1", SessionID: "session-1", LanguageConfig: session.LanguageConfigSnapshot{LanguagePairs: []session.LanguagePair{{Source: "zh-CN", Target: "en-US"}}}}
	coordinator.StartPhraseSubtitleTurn(turn, "zh-CN")
	for _, event := range []realtimev1.PhraseSubtitleEvent{
		{Type: realtimev1.PhraseSubtitleTopic, EventVersion: 1, SessionID: turn.SessionID, UtteranceID: turn.ID, PhraseSequence: 1, SourceText: "你好，", Status: realtimev1.PhraseSubtitleSourceStable, OccurredAt: time.Unix(1, 0).UTC()},
		{Type: realtimev1.PhraseSubtitleTopic, EventVersion: 1, SessionID: turn.SessionID, UtteranceID: turn.ID, PhraseSequence: 2, SourceText: "世界", Status: realtimev1.PhraseSubtitleSourceStable, OccurredAt: time.Unix(1, 0).UTC()},
	} {
		coordinator.ObservePhraseSubtitle(context.Background(), event)
	}
	summary, ok := coordinator.FinalizePhraseSubtitleTurn(context.Background(), turn, "你好，世界")
	if !ok || summary.Text != "en-你好，en-世界" || summary.InputTokens != 2 || summary.OutputTokens != 4 || summary.CostAmount != "0.2" {
		t.Fatalf("FinalizePhraseSubtitleTurn() = %#v, %v", summary, ok)
	}
	events := observer.Events()
	if len(events) != 4 || events[2].Status != realtimev1.PhraseSubtitleTranslated || events[2].PhraseSequence != 1 || events[3].PhraseSequence != 2 {
		t.Fatalf("events = %#v", events)
	}
}

func TestPhraseTranslationCoordinatorDiscardsStateWhenFinalizeIsCanceled(t *testing.T) {
	coordinator := NewPhraseTranslationCoordinator(phraseTranslateFunc(func(ctx context.Context, _ translate.Request) (translate.Result, error) {
		return translate.Result{}, ctx.Err()
	}), "mock", &recordingPhraseSubtitleObserver{}, nil)
	turn := TurnContext{ID: "turn-canceled", SessionID: "session-1", LanguageConfig: session.LanguageConfigSnapshot{LanguagePairs: []session.LanguagePair{{Source: "zh-CN", Target: "en-US"}}}}
	coordinator.StartPhraseSubtitleTurn(turn, "zh-CN")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, ok := coordinator.FinalizePhraseSubtitleTurn(ctx, turn, "你好"); ok {
		t.Fatal("FinalizePhraseSubtitleTurn() unexpectedly reused canceled context")
	}
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	if len(coordinator.utterances) != 0 {
		t.Fatalf("coordinator utterances = %d, want 0", len(coordinator.utterances))
	}
}

type phraseTranslateFunc func(context.Context, translate.Request) (translate.Result, error)

func (f phraseTranslateFunc) Translate(ctx context.Context, request translate.Request) (translate.Result, error) {
	return f(ctx, request)
}

var _ translate.Provider = phraseTranslateFunc(nil)
