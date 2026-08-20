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
	summary, ok := coordinator.FinalizePhraseSubtitleTurn(context.Background(), turn, "你好，世界")
	if !ok || summary.Text != "en-你好，en-世界" || summary.InputTokens != 2 || summary.OutputTokens != 4 || summary.CostAmount != "0.2" {
		t.Fatalf("FinalizePhraseSubtitleTurn() = %#v, %v", summary, ok)
	}
	events := observer.Events()
	if len(events) != 4 || events[2].Status != realtimev1.PhraseSubtitleTranslated || events[2].PhraseSequence != 1 || events[3].PhraseSequence != 2 {
		t.Fatalf("events = %#v", events)
	}
}

func TestPhraseTranslationCoordinatorDoesNotWaitForPendingPhrase(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	usage := &phraseUsageRecorder{}
	coordinator := NewPhraseTranslationCoordinator(phraseTranslateFunc(func(_ context.Context, request translate.Request) (translate.Result, error) {
		close(started)
		<-release
		return translate.Result{Text: "hello", Provider: "mock", Model: "v1", InputTokens: 3}, nil
	}), "mock", &recordingPhraseSubtitleObserver{}, nil, usage)
	turn := TurnContext{ID: "turn-pending", SessionID: "session-1", AccountID: "account-1", TraceID: "trace-1", LanguageConfig: session.LanguageConfigSnapshot{LanguagePairs: []session.LanguagePair{{Source: "zh-CN", Target: "en-US"}}}}
	coordinator.StartPhraseSubtitleTurn(turn, "zh-CN")
	coordinator.ObservePhraseSubtitle(context.Background(), realtimev1.PhraseSubtitleEvent{Type: realtimev1.PhraseSubtitleTopic, EventVersion: 1, SessionID: turn.SessionID, UtteranceID: turn.ID, PhraseSequence: 1, SourceText: "你好", Status: realtimev1.PhraseSubtitleSourceStable, OccurredAt: time.Now().UTC()})
	<-started
	start := time.Now()
	if _, ok := coordinator.FinalizePhraseSubtitleTurn(context.Background(), turn, "你好"); ok || time.Since(start) > 100*time.Millisecond {
		t.Fatalf("FinalizePhraseSubtitleTurn() = %v, elapsed %v; want immediate fallback", ok, time.Since(start))
	}
	close(release)
	deadline := time.Now().Add(time.Second)
	for len(usage.Facts()) < 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	facts := usage.Facts()
	if len(facts) != 1 || facts[0].IdempotencyKey != "usage:turn-pending:phrase:1" {
		t.Fatalf("phrase usage = %#v", facts)
	}
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
		_, ok := coordinator.FinalizePhraseSubtitleTurn(context.Background(), turn, "你好")
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

func TestPhraseTranslationCoordinatorRecordsCompletedFailedPhraseUsageOnFallback(t *testing.T) {
	usage := &phraseUsageRecorder{}
	coordinator := NewPhraseTranslationCoordinator(phraseTranslateFunc(func(_ context.Context, request translate.Request) (translate.Result, error) {
		if request.Text == "失败" {
			return translate.Result{Provider: "mock", Model: "v1", InputTokens: 2, CostAmount: "0.25", Currency: "USD"}, context.DeadlineExceeded
		}
		return translate.Result{Text: "hello", Provider: "mock", Model: "v1", InputTokens: 1, CostAmount: "0.10", Currency: "USD"}, nil
	}), "mock", &recordingPhraseSubtitleObserver{}, nil, usage)
	turn := TurnContext{ID: "turn-fallback", SessionID: "session-1", AccountID: "account-1", TraceID: "trace-1", LanguageConfig: session.LanguageConfigSnapshot{LanguagePairs: []session.LanguagePair{{Source: "zh-CN", Target: "en-US"}}}}
	coordinator.StartPhraseSubtitleTurn(turn, "zh-CN")
	for sequence, text := range map[int64]string{1: "你好", 2: "失败"} {
		coordinator.ObservePhraseSubtitle(context.Background(), realtimev1.PhraseSubtitleEvent{Type: realtimev1.PhraseSubtitleTopic, EventVersion: 1, SessionID: turn.SessionID, UtteranceID: turn.ID, PhraseSequence: sequence, SourceText: text, Status: realtimev1.PhraseSubtitleSourceStable, OccurredAt: time.Now().UTC()})
	}
	deadline := time.Now().Add(time.Second)
	for len(usage.Facts()) < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if _, ok := coordinator.FinalizePhraseSubtitleTurn(context.Background(), turn, "你好失败"); ok {
		t.Fatal("FinalizePhraseSubtitleTurn() unexpectedly reused failed phrase")
	}
	deadline = time.Now().Add(time.Second)
	for len(usage.Facts()) < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(usage.Facts()) != 2 {
		t.Fatalf("phrase usage facts = %#v, want both completed requests", usage.Facts())
	}
}

func TestPhraseTranslationCoordinatorRetriesPhraseUsagePublication(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	usage := &retryingPhraseUsageRecorder{failures: 1}
	coordinator := NewPhraseTranslationCoordinator(phraseTranslateFunc(func(_ context.Context, _ translate.Request) (translate.Result, error) {
		close(started)
		<-release
		return translate.Result{Text: "hello", Provider: "mock", Model: "v1", InputTokens: 1}, nil
	}), "mock", &recordingPhraseSubtitleObserver{}, nil, usage)
	coordinator.usageRetryDelay = func(int) time.Duration { return time.Millisecond }
	turn := TurnContext{ID: "turn-usage-retry", SessionID: "session-1", AccountID: "account-1", TraceID: "trace-1", LanguageConfig: session.LanguageConfigSnapshot{LanguagePairs: []session.LanguagePair{{Source: "zh-CN", Target: "en-US"}}}}
	coordinator.StartPhraseSubtitleTurn(turn, "zh-CN")
	coordinator.ObservePhraseSubtitle(context.Background(), realtimev1.PhraseSubtitleEvent{Type: realtimev1.PhraseSubtitleTopic, EventVersion: 1, SessionID: turn.SessionID, UtteranceID: turn.ID, PhraseSequence: 1, SourceText: "你好", Status: realtimev1.PhraseSubtitleSourceStable, OccurredAt: time.Now().UTC()})
	<-started
	if _, ok := coordinator.FinalizePhraseSubtitleTurn(context.Background(), turn, "你好"); ok {
		t.Fatal("FinalizePhraseSubtitleTurn() unexpectedly reused pending phrase")
	}
	close(release)
	deadline := time.Now().Add(time.Second)
	for len(usage.Facts()) < 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(usage.Facts()) != 1 || usage.Attempts() != 2 {
		t.Fatalf("phrase usage facts = %#v, attempts = %d; want one fact after two attempts", usage.Facts(), usage.Attempts())
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

type phraseObserverFunc func(context.Context, realtimev1.PhraseSubtitleEvent)

func (f phraseObserverFunc) ObservePhraseSubtitle(ctx context.Context, event realtimev1.PhraseSubtitleEvent) {
	f(ctx, event)
}

func stablePhraseEvent(turn TurnContext, sequence int64, text string) realtimev1.PhraseSubtitleEvent {
	return realtimev1.PhraseSubtitleEvent{Type: realtimev1.PhraseSubtitleTopic, EventVersion: 1, SessionID: turn.SessionID, UtteranceID: turn.ID, PhraseSequence: sequence, SourceText: text, Status: realtimev1.PhraseSubtitleSourceStable, OccurredAt: time.Now().UTC()}
}

type phraseUsageRecorder struct {
	mu    sync.Mutex
	facts []UsageFact
}

func (r *phraseUsageRecorder) Publish(_ context.Context, fact UsageFact) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.facts = append(r.facts, fact)
	return nil
}

func (r *phraseUsageRecorder) Facts() []UsageFact {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]UsageFact(nil), r.facts...)
}

type retryingPhraseUsageRecorder struct {
	mu       sync.Mutex
	facts    []UsageFact
	failures int
	attempts int
}

func (r *retryingPhraseUsageRecorder) Publish(_ context.Context, fact UsageFact) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.attempts++
	if r.failures > 0 {
		r.failures--
		return context.DeadlineExceeded
	}
	r.facts = append(r.facts, fact)
	return nil
}

func (r *retryingPhraseUsageRecorder) Facts() []UsageFact {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]UsageFact(nil), r.facts...)
}

func (r *retryingPhraseUsageRecorder) Attempts() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.attempts
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
