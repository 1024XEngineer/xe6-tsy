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
		return translate.Result{Text: "en-" + request.Text, Provider: "mock", Model: "v1", InputTokens: 1, OutputTokens: 2, Currency: "USD"}, nil
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
	if !ok || summary.Text != "en-你好，en-世界" || summary.InputTokens != 2 || summary.OutputTokens != 4 {
		t.Fatalf("FinalizePhraseSubtitleTurn() = %#v, %v", summary, ok)
	}
	events := observer.Events()
	if len(events) != 4 || events[2].Status != realtimev1.PhraseSubtitleTranslated || events[2].PhraseSequence != 1 || events[3].PhraseSequence != 2 {
		t.Fatalf("events = %#v", events)
	}
}

type phraseTranslateFunc func(context.Context, translate.Request) (translate.Result, error)

func (f phraseTranslateFunc) Translate(ctx context.Context, request translate.Request) (translate.Result, error) {
	return f(ctx, request)
}

var _ translate.Provider = phraseTranslateFunc(nil)
