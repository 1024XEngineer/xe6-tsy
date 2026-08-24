package pipeline

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/asr"
)

// PhraseSubtitleObserver publishes best-effort phrase subtitle updates. Delivery must never
// affect recognition finalization or any durable turn side effect.
type PhraseSubtitleObserver interface {
	ObservePhraseSubtitle(context.Context, realtimev1.PhraseSubtitleEvent)
}

type PhraseSubtitleTurnObserver interface {
	StartPhraseSubtitleTurn(TurnContext, string)
	DiscardPhraseSubtitleTurn(string)
}

// PhraseSubtitleFinalFlushObserver distinguishes final-tail source delivery from
// live-stabilized phrases so the tail is finalized by the whole-turn translator.
type PhraseSubtitleFinalFlushObserver interface {
	BeginPhraseSubtitleFinalFlush(string)
	EndPhraseSubtitleFinalFlush(string)
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
	turn           TurnContext
	stabilizer     *PhraseStabilizer
	timer          *time.Timer
	sourceLanguage string
	routeStarted   bool
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
func (p *PhraseSubtitleProcessor) Start(turn TurnContext, sourceLanguage string) {
	if p == nil || turn.ID == "" {
		return
	}
	p.mu.Lock()
	initialLanguage := asr.NormalizeLanguage(sourceLanguage)
	utterance := &phraseUtterance{turn: turn, stabilizer: NewPhraseStabilizer(p.options), sourceLanguage: initialLanguage}
	utterance.routeStarted = initialLanguage != ""
	p.utterances[turn.ID] = utterance
	slog.Info("phrase_turn_started", "session_id", turn.SessionID, "turn_id", turn.ID,
		"mode", turn.Mode.Mode, "source_language", sourceLanguage, "observer_configured", p.observer != nil)
	shouldStart := utterance.routeStarted
	p.mu.Unlock()
	if shouldStart {
		if lifecycle, ok := p.observer.(PhraseSubtitleTurnObserver); ok {
			lifecycle.StartPhraseSubtitleTurn(turn, initialLanguage)
		}
	}
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
		slog.Warn("phrase_partial_ignored", "session_id", event.SessionID, "turn_id", event.TurnID,
			"reason", "turn_not_started", "text_runes", len([]rune(event.Text)), "stash_runes", len([]rune(event.Stash)))
		return
	}
	now := p.clock()
	startLanguage := ""
	if !utterance.routeStarted && strings.TrimSpace(event.Text) != "" {
		if language := asr.NormalizeLanguage(event.SourceLanguage); language != "" {
			utterance.sourceLanguage = language
			utterance.routeStarted = true
			startLanguage = language
		}
	}
	slog.Info("phrase_partial_observed", "session_id", event.SessionID, "turn_id", event.TurnID,
		"text", event.Text, "stash", event.Stash, "source_language", event.SourceLanguage)
	phrases := utterance.stabilizer.Observe(event.Text, now)
	p.resetTimerLocked(event.TurnID, utterance, now)
	p.mu.Unlock()
	if startLanguage != "" {
		slog.Info("phrase_source_language_locked", "session_id", utterance.turn.SessionID, "turn_id", event.TurnID,
			"source_language", startLanguage)
		if lifecycle, ok := p.observer.(PhraseSubtitleTurnObserver); ok {
			lifecycle.StartPhraseSubtitleTurn(utterance.turn, startLanguage)
		}
	}
	if len(phrases) > 0 {
		for _, phrase := range phrases {
			slog.Info("phrase_source_stable", "session_id", turnSessionID(utterance.turn), "turn_id", event.TurnID,
				"phrase_sequence", phrase.SequenceNo, "text", phrase.Text, "trigger", "punctuation_or_stability")
		}
	}
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
	if lifecycle, ok := p.observer.(PhraseSubtitleFinalFlushObserver); ok {
		lifecycle.BeginPhraseSubtitleFinalFlush(turn.ID)
		defer lifecycle.EndPhraseSubtitleFinalFlush(turn.ID)
	}
	p.publish(ctx, utterance.turn, phrases)
}

// Discard releases an aborted turn without publishing an unstable tail.
func (p *PhraseSubtitleProcessor) Discard(turnID string) {
	if p == nil || turnID == "" {
		return
	}
	p.mu.Lock()
	utterance := p.utterances[turnID]
	if utterance != nil {
		delete(p.utterances, turnID)
		if utterance.timer != nil {
			utterance.timer.Stop()
		}
	}
	p.mu.Unlock()
	if lifecycle, ok := p.observer.(PhraseSubtitleTurnObserver); ok {
		lifecycle.DiscardPhraseSubtitleTurn(turnID)
	}
}

func (p *PhraseSubtitleProcessor) resetTimerLocked(turnID string, utterance *phraseUtterance, now time.Time) {
	if utterance.timer != nil {
		utterance.timer.Stop()
	}
	delay, ok := utterance.stabilizer.stabilityDelay(now)
	if !ok {
		utterance.timer = nil
		return
	}
	utterance.timer = time.AfterFunc(delay, func() {
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
	for _, phrase := range phrases {
		slog.Info("phrase_source_stable", "session_id", utterance.turn.SessionID, "turn_id", turnID,
			"phrase_sequence", phrase.SequenceNo, "text", phrase.Text, "trigger", "stability_window")
	}
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

func turnSessionID(turn TurnContext) string { return turn.SessionID }

func (p *PhraseSubtitleProcessor) clock() time.Time {
	if p.now == nil {
		return time.Now().UTC()
	}
	return p.now()
}
