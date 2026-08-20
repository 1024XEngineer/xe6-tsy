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
	deadline := time.Now().Add(time.Second)
	for len(observer.Events()) < 4 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	summary, usage, ok, err := coordinator.FinalizePhraseSubtitleTurn(context.Background(), turn, "你好，世界")
	if err != nil || len(usage) != 0 || !ok || summary.Text != "en-你好，en-世界" || summary.InputTokens != 2 || summary.OutputTokens != 4 || summary.CostAmount != "0.2" {
		t.Fatalf("FinalizePhraseSubtitleTurn() = %#v, %#v, %v, %v", summary, usage, ok, err)
	}
	events := observer.Events()
	if len(events) != 4 || events[2].Status != realtimev1.PhraseSubtitleTranslated || events[2].PhraseSequence != 1 || events[3].PhraseSequence != 2 {
		t.Fatalf("events = %#v", events)
	}
}

func TestPhraseTranslationCoordinatorDoesNotWaitForPendingPhrase(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	coordinator := NewPhraseTranslationCoordinator(phraseTranslateFunc(func(_ context.Context, request translate.Request) (translate.Result, error) {
		close(started)
		<-release
		return translate.Result{Text: "hello", Provider: "mock", Model: "v1", InputTokens: 3}, nil
	}), "mock", &recordingPhraseSubtitleObserver{}, nil)
	turn := TurnContext{ID: "turn-pending", SessionID: "session-1", AccountID: "account-1", TraceID: "trace-1", LanguageConfig: session.LanguageConfigSnapshot{LanguagePairs: []session.LanguagePair{{Source: "zh-CN", Target: "en-US"}}}}
	coordinator.StartPhraseSubtitleTurn(turn, "zh-CN")
	coordinator.ObservePhraseSubtitle(context.Background(), realtimev1.PhraseSubtitleEvent{Type: realtimev1.PhraseSubtitleTopic, EventVersion: 1, SessionID: turn.SessionID, UtteranceID: turn.ID, PhraseSequence: 1, SourceText: "你好", Status: realtimev1.PhraseSubtitleSourceStable, OccurredAt: time.Now().UTC()})
	<-started
	start := time.Now()
	if _, usage, ok, err := coordinator.FinalizePhraseSubtitleTurn(context.Background(), turn, "你好"); err != nil || ok || len(usage) != 0 || time.Since(start) > 100*time.Millisecond {
		t.Fatalf("FinalizePhraseSubtitleTurn() = usage=%#v, reused=%v, err=%v, elapsed=%v; want immediate fallback", usage, ok, err, time.Since(start))
	}
	close(release)
}

func TestPhraseTranslationCoordinatorFinalizeDoesNotWaitForSubtitleObserver(t *testing.T) {
	observer := newBlockingTranslatedObserver()
	defer close(observer.release)
	coordinator := NewPhraseTranslationCoordinator(phraseTranslateFunc(func(_ context.Context, _ translate.Request) (translate.Result, error) {
		return translate.Result{Text: "hello", Provider: "mock", Model: "v1", InputTokens: 1}, nil
	}), "mock", observer, nil)
	turn := TurnContext{ID: "turn-blocked-observer", SessionID: "session-1", LanguageConfig: session.LanguageConfigSnapshot{LanguagePairs: []session.LanguagePair{{Source: "zh-CN", Target: "en-US"}}}}
	coordinator.StartPhraseSubtitleTurn(turn, "zh-CN")
	coordinator.ObservePhraseSubtitle(context.Background(), realtimev1.PhraseSubtitleEvent{Type: realtimev1.PhraseSubtitleTopic, EventVersion: 1, SessionID: turn.SessionID, UtteranceID: turn.ID, PhraseSequence: 1, SourceText: "你好", Status: realtimev1.PhraseSubtitleSourceStable, OccurredAt: time.Now().UTC()})
	<-observer.started

	done := make(chan bool, 1)
	go func() {
		_, _, ok, _ := coordinator.FinalizePhraseSubtitleTurn(context.Background(), turn, "你好")
		done <- ok
	}()
	select {
	case ok := <-done:
		if !ok {
			t.Fatal("FinalizePhraseSubtitleTurn() did not reuse the completed phrase")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("FinalizePhraseSubtitleTurn() waited for the subtitle observer")
	}
}

func TestPhraseTranslationCoordinatorDoesNotBlockOtherTurnsOnSubtitleObserver(t *testing.T) {
	blocked := newBlockingTranslatedObserver()
	defer close(blocked.release)
	other := &recordingPhraseSubtitleObserver{}
	observer := phraseObserverFunc(func(ctx context.Context, event realtimev1.PhraseSubtitleEvent) {
		if event.UtteranceID == "turn-blocked" {
			blocked.ObservePhraseSubtitle(ctx, event)
			return
		}
		other.ObservePhraseSubtitle(ctx, event)
	})
	coordinator := NewPhraseTranslationCoordinator(phraseTranslateFunc(func(_ context.Context, request translate.Request) (translate.Result, error) {
		return translate.Result{Text: "en-" + request.Text, Provider: "mock", Model: "v1", InputTokens: 1}, nil
	}), "mock", observer, nil)
	blockedTurn := TurnContext{ID: "turn-blocked", SessionID: "session-1", LanguageConfig: session.LanguageConfigSnapshot{LanguagePairs: []session.LanguagePair{{Source: "zh-CN", Target: "en-US"}}}}
	otherTurn := TurnContext{ID: "turn-other", SessionID: "session-2", LanguageConfig: blockedTurn.LanguageConfig}
	coordinator.StartPhraseSubtitleTurn(blockedTurn, "zh-CN")
	coordinator.StartPhraseSubtitleTurn(otherTurn, "zh-CN")
	coordinator.ObservePhraseSubtitle(context.Background(), stablePhraseEvent(blockedTurn, 1, "你好"))
	<-blocked.started
	coordinator.ObservePhraseSubtitle(context.Background(), stablePhraseEvent(otherTurn, 1, "世界"))

	deadline := time.Now().Add(100 * time.Millisecond)
	for len(other.Events()) < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	events := other.Events()
	if len(events) != 2 || events[1].Status != realtimev1.PhraseSubtitleTranslated {
		t.Fatalf("other turn events = %#v; want translated event while first turn is blocked", events)
	}
}

func TestPhraseTranslationCoordinatorReturnsCompletedPhraseUsageOnFallback(t *testing.T) {
	coordinator := NewPhraseTranslationCoordinator(phraseTranslateFunc(func(_ context.Context, request translate.Request) (translate.Result, error) {
		if request.Text == "失败" {
			return translate.Result{Provider: "mock", Model: "v1", InputTokens: 2, CostAmount: "0.25", Currency: "USD"}, context.DeadlineExceeded
		}
		return translate.Result{Text: "hello", Provider: "mock", Model: "v1", InputTokens: 1, CostAmount: "0.10", Currency: "USD"}, nil
	}), "mock", &recordingPhraseSubtitleObserver{}, nil)
	turn := TurnContext{ID: "turn-fallback", SessionID: "session-1", AccountID: "account-1", TraceID: "trace-1", LanguageConfig: session.LanguageConfigSnapshot{LanguagePairs: []session.LanguagePair{{Source: "zh-CN", Target: "en-US"}}}}
	coordinator.StartPhraseSubtitleTurn(turn, "zh-CN")
	for sequence, text := range map[int64]string{1: "你好", 2: "失败"} {
		coordinator.ObservePhraseSubtitle(context.Background(), realtimev1.PhraseSubtitleEvent{Type: realtimev1.PhraseSubtitleTopic, EventVersion: 1, SessionID: turn.SessionID, UtteranceID: turn.ID, PhraseSequence: sequence, SourceText: text, Status: realtimev1.PhraseSubtitleSourceStable, OccurredAt: time.Now().UTC()})
	}
	deadline := time.Now().Add(time.Second)
	for {
		coordinator.mu.Lock()
		done := allPhraseTranslationsDone(coordinator.utterances[turn.ID])
		coordinator.mu.Unlock()
		if done || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	_, usage, ok, err := coordinator.FinalizePhraseSubtitleTurn(context.Background(), turn, "你好失败")
	if err != nil || ok {
		t.Fatal("FinalizePhraseSubtitleTurn() unexpectedly reused failed phrase")
	}
	if len(usage) != 2 {
		t.Fatalf("phrase usage facts = %#v, want both completed requests", usage)
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
	if _, _, ok, err := coordinator.FinalizePhraseSubtitleTurn(ctx, turn, "你好"); err != nil || ok {
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

type phraseObserverFunc func(context.Context, realtimev1.PhraseSubtitleEvent)

func (f phraseObserverFunc) ObservePhraseSubtitle(ctx context.Context, event realtimev1.PhraseSubtitleEvent) {
	f(ctx, event)
}

func stablePhraseEvent(turn TurnContext, sequence int64, text string) realtimev1.PhraseSubtitleEvent {
	return realtimev1.PhraseSubtitleEvent{Type: realtimev1.PhraseSubtitleTopic, EventVersion: 1, SessionID: turn.SessionID, UtteranceID: turn.ID, PhraseSequence: sequence, SourceText: text, Status: realtimev1.PhraseSubtitleSourceStable, OccurredAt: time.Now().UTC()}
}

type blockingTranslatedObserver struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBlockingTranslatedObserver() *blockingTranslatedObserver {
	return &blockingTranslatedObserver{started: make(chan struct{}), release: make(chan struct{})}
}

func (o *blockingTranslatedObserver) ObservePhraseSubtitle(_ context.Context, event realtimev1.PhraseSubtitleEvent) {
	if event.Status != realtimev1.PhraseSubtitleTranslated {
		return
	}
	o.once.Do(func() { close(o.started) })
	<-o.release
}
