package pipeline

import (
	"context"
	"reflect"
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
	summary, residual, usage, ok, err := coordinator.FinalizePhraseSubtitleTurn(context.Background(), turn, "你好，世界")
	if err != nil || residual != "" || len(usage) != 0 || !ok || summary.Text != "en-你好，en-世界" || summary.InputTokens != 2 || summary.OutputTokens != 4 || summary.CostAmount != "0.2" {
		t.Fatalf("FinalizePhraseSubtitleTurn() = %#v, %q, %#v, %v, %v", summary, residual, usage, ok, err)
	}
	events := observer.Events()
	if len(events) != 4 {
		t.Fatalf("events = %#v", events)
	}
	sourceIndex := map[int64]int{}
	var translated []int64
	for index, event := range events {
		switch event.Status {
		case realtimev1.PhraseSubtitleSourceStable:
			sourceIndex[event.PhraseSequence] = index
		case realtimev1.PhraseSubtitleTranslated:
			if sourceIndex[event.PhraseSequence] >= index {
				t.Fatalf("translated event preceded source event: %#v", events)
			}
			translated = append(translated, event.PhraseSequence)
		}
	}
	if len(sourceIndex) != 2 || len(translated) != 2 || translated[0] != 1 || translated[1] != 2 {
		t.Fatalf("events = %#v", events)
	}
}

func TestSplitStreamTTSKeepsValidatedChunksOrdered(t *testing.T) {
	got := splitStreamTTS("hello,world这是一段较长的尾部")
	want := []string{"hello,", "world这是一段较长的", "尾部"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("splitStreamTTS() = %#v, want %#v", got, want)
	}
}

func TestPhraseTranslationCoordinatorWaitsAndReusesPendingPhrase(t *testing.T) {
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
	finalized := make(chan struct {
		summary  PhraseTranslationSummary
		residual string
		ok       bool
		err      error
	}, 1)
	go func() {
		summary, residual, _, ok, err := coordinator.FinalizePhraseSubtitleTurn(context.Background(), turn, "你好")
		finalized <- struct {
			summary  PhraseTranslationSummary
			residual string
			ok       bool
			err      error
		}{summary: summary, residual: residual, ok: ok, err: err}
	}()
	select {
	case result := <-finalized:
		t.Fatalf("FinalizePhraseSubtitleTurn() returned before pending phrase: %#v", result)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	select {
	case result := <-finalized:
		if result.err != nil || result.residual != "" || !result.ok || result.summary.Text != "hello" {
			t.Fatalf("FinalizePhraseSubtitleTurn() = %#v, want reused pending result", result)
		}
	case <-time.After(time.Second):
		t.Fatal("FinalizePhraseSubtitleTurn() did not settle after pending phrase completed")
	}
}

func TestPhraseTranslationCoordinatorReusesCompletedPrefixWithPendingTail(t *testing.T) {
	tailStarted := make(chan struct{})
	coordinator := NewPhraseTranslationCoordinator(phraseTranslateFunc(func(ctx context.Context, request translate.Request) (translate.Result, error) {
		if request.Text == "你好" {
			return translate.Result{Text: "hello", Provider: "mock", Model: "v1", InputTokens: 1}, nil
		}
		close(tailStarted)
		return translate.Result{Text: "world", Provider: "mock", Model: "v1", InputTokens: 1}, nil
	}), "mock", &recordingPhraseSubtitleObserver{}, nil)
	turn := TurnContext{ID: "turn-prefix", SessionID: "session-1", AccountID: "account-1", TraceID: "trace-1", LanguageConfig: session.LanguageConfigSnapshot{LanguagePairs: []session.LanguagePair{{Source: "zh-CN", Target: "en-US"}}}}
	coordinator.StartPhraseSubtitleTurn(turn, "zh-CN")
	coordinator.ObservePhraseSubtitle(context.Background(), stablePhraseEvent(turn, 1, "你好"))

	deadline := time.Now().Add(time.Second)
	for {
		coordinator.mu.Lock()
		phrase := coordinator.utterances[turn.ID].phrases[1]
		done := phrase.done
		coordinator.mu.Unlock()
		if done || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	coordinator.ObservePhraseSubtitle(context.Background(), stablePhraseEvent(turn, 2, "，世界"))
	<-tailStarted

	summary, residual, usage, reused, err := coordinator.FinalizePhraseSubtitleTurn(context.Background(), turn, "你好，世界")
	if err != nil || !reused || summary.Text != "helloworld" || residual != "" || len(usage) != 0 {
		t.Fatalf("FinalizePhraseSubtitleTurn() = %#v, %q, %#v, %v, %v", summary, residual, usage, reused, err)
	}
}

func TestPhraseTranslationCoordinatorReportsLateUsageAfterCanceledFinalize(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	reported := make(chan UsageFact, 1)
	coordinator := NewPhraseTranslationCoordinator(phraseTranslateFunc(func(_ context.Context, _ translate.Request) (translate.Result, error) {
		close(started)
		<-release
		return translate.Result{Text: "hello", Provider: "mock", Model: "v1", InputTokens: 7}, nil
	}), "mock", &recordingPhraseSubtitleObserver{}, nil)
	coordinator.SetLatePhraseUsageReporter(func(fact UsageFact) { reported <- fact })
	turn := TurnContext{ID: "turn-late-usage", SessionID: "session-1", AccountID: "account-1", TraceID: "trace-1", LanguageConfig: session.LanguageConfigSnapshot{LanguagePairs: []session.LanguagePair{{Source: "zh-CN", Target: "en-US"}}}}
	coordinator.StartPhraseSubtitleTurn(turn, "zh-CN")
	coordinator.ObservePhraseSubtitle(context.Background(), stablePhraseEvent(turn, 1, "你好"))
	<-started
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, _, reused, err := coordinator.FinalizePhraseSubtitleTurn(ctx, turn, "你好"); err != nil || reused {
		t.Fatalf("FinalizePhraseSubtitleTurn() = reused=%v, err=%v; want canceled finalization", reused, err)
	}
	close(release)
	select {
	case fact := <-reported:
		if fact.IdempotencyKey != "usage:turn-late-usage:phrase:1" || fact.InputTokens != 7 {
			t.Fatalf("late usage = %#v", fact)
		}
	case <-time.After(time.Second):
		t.Fatal("late phrase usage was not reported")
	}
}

func TestPhraseTranslationCoordinatorReportsCompletedUsageOnDiscard(t *testing.T) {
	reported := make(chan UsageFact, 1)
	coordinator := NewPhraseTranslationCoordinator(phraseTranslateFunc(func(_ context.Context, _ translate.Request) (translate.Result, error) {
		return translate.Result{Text: "hello", Provider: "mock", Model: "v1", InputTokens: 3}, nil
	}), "mock", &recordingPhraseSubtitleObserver{}, nil)
	coordinator.SetLatePhraseUsageReporter(func(fact UsageFact) { reported <- fact })
	turn := TurnContext{ID: "turn-discard-usage", SessionID: "session-1", AccountID: "account-1", TraceID: "trace-1", LanguageConfig: session.LanguageConfigSnapshot{LanguagePairs: []session.LanguagePair{{Source: "zh-CN", Target: "en-US"}}}}
	coordinator.StartPhraseSubtitleTurn(turn, "zh-CN")
	coordinator.ObservePhraseSubtitle(context.Background(), stablePhraseEvent(turn, 1, "你好"))
	deadline := time.Now().Add(time.Second)
	for {
		coordinator.mu.Lock()
		phrase := coordinator.utterances[turn.ID].phrases[1]
		done := phrase.done
		coordinator.mu.Unlock()
		if done || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	coordinator.DiscardPhraseSubtitleTurn(turn.ID)
	select {
	case fact := <-reported:
		if fact.IdempotencyKey != "usage:turn-discard-usage:phrase:1" || fact.InputTokens != 3 {
			t.Fatalf("discard usage = %#v", fact)
		}
	case <-time.After(time.Second):
		t.Fatal("discarded phrase usage was not reported")
	}
}

func TestPhraseTranslationCoordinatorStartsTranslationBeforeSourceDelivery(t *testing.T) {
	observer := newBlockingSourceObserver()
	defer close(observer.release)
	started := make(chan struct{})
	coordinator := NewPhraseTranslationCoordinator(phraseTranslateFunc(func(_ context.Context, _ translate.Request) (translate.Result, error) {
		close(started)
		return translate.Result{Text: "hello", Provider: "mock", Model: "v1", InputTokens: 1}, nil
	}), "mock", observer, nil)
	turn := TurnContext{ID: "turn-blocked-source", SessionID: "session-1", LanguageConfig: session.LanguageConfigSnapshot{LanguagePairs: []session.LanguagePair{{Source: "zh-CN", Target: "en-US"}}}}
	coordinator.StartPhraseSubtitleTurn(turn, "zh-CN")

	returned := make(chan struct{})
	go func() {
		coordinator.ObservePhraseSubtitle(context.Background(), stablePhraseEvent(turn, 1, "你好"))
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("ObservePhraseSubtitle() waited for source delivery")
	}
	select {
	case <-started:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("translation did not start while source delivery was blocked")
	}
	if _, residual, _, reused, err := coordinator.FinalizePhraseSubtitleTurn(context.Background(), turn, "你好"); err != nil || residual != "" || !reused {
		t.Fatalf("FinalizePhraseSubtitleTurn() = reused=%v, err=%v; want phrase reuse", reused, err)
	}
}

func TestPhraseTranslationCoordinatorDoesNotPublishTerminalEventsAfterDiscard(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	observer := &recordingPhraseSubtitleObserver{}
	coordinator := NewPhraseTranslationCoordinator(phraseTranslateFunc(func(_ context.Context, _ translate.Request) (translate.Result, error) {
		close(started)
		<-release
		return translate.Result{Text: "hello", Provider: "mock", Model: "v1", InputTokens: 1}, nil
	}), "mock", observer, nil)
	turn := TurnContext{ID: "turn-discard-events", SessionID: "session-1", LanguageConfig: session.LanguageConfigSnapshot{LanguagePairs: []session.LanguagePair{{Source: "zh-CN", Target: "en-US"}}}}
	coordinator.StartPhraseSubtitleTurn(turn, "zh-CN")
	coordinator.ObservePhraseSubtitle(context.Background(), stablePhraseEvent(turn, 1, "你好"))
	<-started
	coordinator.DiscardPhraseSubtitleTurn(turn.ID)
	close(release)
	time.Sleep(20 * time.Millisecond)
	for _, event := range observer.Events() {
		if event.Status == realtimev1.PhraseSubtitleTranslated || event.Status == realtimev1.PhraseSubtitleTranslationFailed {
			t.Fatalf("terminal event after discard = %#v", event)
		}
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
		_, _, _, ok, _ := coordinator.FinalizePhraseSubtitleTurn(context.Background(), turn, "你好")
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
	summary, residual, usage, ok, err := coordinator.FinalizePhraseSubtitleTurn(context.Background(), turn, "你好失败")
	if err != nil || !ok || residual != "" {
		t.Fatalf("FinalizePhraseSubtitleTurn() = ok=%v residual=%q err=%v, want residual settlement", ok, residual, err)
	}
	if summary.Text != "hello\x00" || len(summary.ResidualSegments) != 1 || summary.ResidualSegments[0] != "失败" {
		t.Fatalf("summary = %#v, want marker for 失败", summary)
	}
	if len(usage) != 0 {
		t.Fatalf("phrase usage facts = %#v, want aggregate settlement", usage)
	}
}

func TestPhraseTranslationCoordinatorDoesNotRetranslateAfterMiddleFailure(t *testing.T) {
	var requestsMu sync.Mutex
	var requests []string
	coordinator := NewPhraseTranslationCoordinator(phraseTranslateFunc(func(_ context.Context, request translate.Request) (translate.Result, error) {
		requestsMu.Lock()
		requests = append(requests, request.Text)
		requestsMu.Unlock()
		if request.Text == "失败" {
			return translate.Result{Provider: "mock", Model: "v1", InputTokens: 2}, context.DeadlineExceeded
		}
		return translate.Result{Text: "en-" + request.Text, Provider: "mock", Model: "v1", InputTokens: 1}, nil
	}), "mock", &recordingPhraseSubtitleObserver{}, nil)
	turn := TurnContext{ID: "turn-middle-failure", SessionID: "session-1", LanguageConfig: session.LanguageConfigSnapshot{LanguagePairs: []session.LanguagePair{{Source: "zh-CN", Target: "en-US"}}}}
	coordinator.StartPhraseSubtitleTurn(turn, "zh-CN")
	for sequence, text := range map[int64]string{1: "你好", 2: "失败", 3: "世界"} {
		coordinator.ObservePhraseSubtitle(context.Background(), stablePhraseEvent(turn, sequence, text))
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		coordinator.mu.Lock()
		utterance := coordinator.utterances[turn.ID]
		done := utterance != nil && allPhraseTranslationsDone(utterance)
		coordinator.mu.Unlock()
		if done {
			break
		}
		time.Sleep(time.Millisecond)
	}
	summary, residual, _, reused, err := coordinator.FinalizePhraseSubtitleTurn(context.Background(), turn, "你好失败世界")
	if err != nil || !reused || residual != "" || summary.Text != "en-你好\x00en-世界" || len(summary.ResidualSegments) != 1 || summary.ResidualSegments[0] != "失败" {
		t.Fatalf("FinalizePhraseSubtitleTurn() = %#v, %q, %v, %v", summary, residual, reused, err)
	}
	requestsMu.Lock()
	defer requestsMu.Unlock()
	if len(requests) != 3 || requests[0] == "你好失败世界" || requests[1] == "你好失败世界" || requests[2] == "你好失败世界" {
		t.Fatalf("translation requests = %#v, want only individual phrases", requests)
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
	if _, _, _, ok, err := coordinator.FinalizePhraseSubtitleTurn(ctx, turn, "你好"); err != nil || ok {
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

type blockingSourceObserver struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBlockingSourceObserver() *blockingSourceObserver {
	return &blockingSourceObserver{started: make(chan struct{}), release: make(chan struct{})}
}

func (o *blockingSourceObserver) ObservePhraseSubtitle(_ context.Context, event realtimev1.PhraseSubtitleEvent) {
	if event.Status != realtimev1.PhraseSubtitleSourceStable {
		return
	}
	o.once.Do(func() { close(o.started) })
	<-o.release
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
