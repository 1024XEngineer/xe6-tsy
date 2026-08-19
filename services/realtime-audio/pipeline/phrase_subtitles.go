package pipeline

import (
	"context"
	"sync"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
)

// PhraseSubtitleObserver publishes best-effort phrase subtitle updates. Delivery must never
// affect recognition finalization or any durable turn side effect.
type PhraseSubtitleObserver interface {
	ObservePhraseSubtitle(context.Context, realtimev1.PhraseSubtitleEvent)
}

// PhraseSubtitleProcessor owns the in-memory stabilizer state for active interpretation turns.
type PhraseSubtitleProcessor struct {
	observer PhraseSubtitleObserver
	now      func() time.Time
	options  PhraseStabilizerOptions

	mu         sync.Mutex
	utterances map[string]*phraseUtterance
}

type phraseUtterance struct {
	turn       TurnContext
	stabilizer *PhraseStabilizer
	timer      *time.Timer
}

// NewPhraseSubtitleProcessor returns nil when no subtitle observer is configured, letting
// callers retain the existing partial-only behaviour without a no-op transport dependency.
func NewPhraseSubtitleProcessor(observer PhraseSubtitleObserver, options PhraseStabilizerOptions) *PhraseSubtitleProcessor {
	if observer == nil {
		return nil
	}
	return &PhraseSubtitleProcessor{
		observer:   observer,
		now:        func() time.Time { return time.Now().UTC() },
		options:    options,
		utterances: make(map[string]*phraseUtterance),
	}
}

// Start begins subtitle stabilization for one newly opened interpretation turn.
func (p *PhraseSubtitleProcessor) Start(turn TurnContext) {
	if p == nil || turn.ID == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.utterances[turn.ID] = &phraseUtterance{turn: turn, stabilizer: NewPhraseStabilizer(p.options)}
}

// Observe accepts one replaceable ASR snapshot and schedules the stability-window check.
func (p *PhraseSubtitleProcessor) Observe(ctx context.Context, event realtimev1.ASRPartialEvent) {
	if p == nil || event.TurnID == "" {
		return
	}
	p.mu.Lock()
	utterance := p.utterances[event.TurnID]
	if utterance == nil {
		p.mu.Unlock()
		return
	}
	phrases := utterance.stabilizer.Observe(event.Text, p.clock())
	p.resetTimerLocked(event.TurnID, utterance)
	p.mu.Unlock()
	p.publish(ctx, utterance.turn, phrases)
}

// Flush commits the final unconsumed source text and removes the utterance before a late
// partial or timer can emit a duplicate subtitle.
func (p *PhraseSubtitleProcessor) Flush(ctx context.Context, turn TurnContext, text string) {
	if p == nil || turn.ID == "" {
		return
	}
	p.mu.Lock()
	utterance := p.utterances[turn.ID]
	if utterance == nil {
		p.mu.Unlock()
		return
	}
	delete(p.utterances, turn.ID)
	if utterance.timer != nil {
		utterance.timer.Stop()
	}
	phrases := utterance.stabilizer.Flush(text)
	p.mu.Unlock()
	p.publish(ctx, utterance.turn, phrases)
}

// Discard releases an aborted turn without publishing an unstable tail.
func (p *PhraseSubtitleProcessor) Discard(turnID string) {
	if p == nil || turnID == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	utterance := p.utterances[turnID]
	if utterance == nil {
		return
	}
	delete(p.utterances, turnID)
	if utterance.timer != nil {
		utterance.timer.Stop()
	}
}

func (p *PhraseSubtitleProcessor) resetTimerLocked(turnID string, utterance *phraseUtterance) {
	if utterance.timer != nil {
		utterance.timer.Stop()
	}
	stableAfter := utterance.stabilizer.stableAfter
	utterance.timer = time.AfterFunc(stableAfter, func() {
		p.advance(turnID)
	})
}

func (p *PhraseSubtitleProcessor) advance(turnID string) {
	p.mu.Lock()
	utterance := p.utterances[turnID]
	if utterance == nil {
		p.mu.Unlock()
		return
	}
	phrases := utterance.stabilizer.Advance(p.clock())
	p.mu.Unlock()
	p.publish(context.Background(), utterance.turn, phrases)
}

func (p *PhraseSubtitleProcessor) publish(ctx context.Context, turn TurnContext, phrases []StablePhrase) {
	if p == nil || p.observer == nil || len(phrases) == 0 {
		return
	}
	for _, phrase := range phrases {
		p.observer.ObservePhraseSubtitle(ctx, realtimev1.PhraseSubtitleEvent{
			Type: realtimev1.PhraseSubtitleTopic, EventVersion: realtimev1.PhraseSubtitleEventVersion,
			SessionID: turn.SessionID, UtteranceID: turn.ID, PhraseSequence: phrase.SequenceNo,
			SourceText: phrase.Text, Status: realtimev1.PhraseSubtitleSourceStable,
			OccurredAt: p.clock(),
		})
	}
}

func (p *PhraseSubtitleProcessor) clock() time.Time {
	if p.now == nil {
		return time.Now().UTC()
	}
	return p.now()
}
