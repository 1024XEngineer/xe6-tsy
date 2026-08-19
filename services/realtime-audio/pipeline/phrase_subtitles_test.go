package pipeline

import (
	"context"
	"sync"
	"testing"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
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
