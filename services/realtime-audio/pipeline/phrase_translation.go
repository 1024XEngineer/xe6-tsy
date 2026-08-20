package pipeline

import (
	"context"
	"math/big"
	"strings"
	"sync"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/asr"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/translate"
)

// PhraseTranslationSummary is the aggregate provider result reused by the final Turn.
type PhraseTranslationSummary struct {
	Text, Provider, Model, CostAmount, Currency string
	InputTokens, OutputTokens                   int64
}

// PhraseTranslationCoordinator translates stable source phrases without blocking ASR reads.
// It owns only ephemeral per-utterance state; FinalTurn persistence remains in PipelineService.
type PhraseTranslationCoordinator struct {
	translator translate.Provider
	provider   string
	observer   PhraseSubtitleObserver
	now        func() time.Time

	mu         sync.Mutex
	utterances map[string]*phraseTranslationUtterance
}

type phraseTranslationUtterance struct {
	turn           TurnContext
	source, target string
	ctx            context.Context
	cancel         context.CancelFunc
	pending        sync.WaitGroup
	phrases        map[int64]*translatedPhrase
	next           int64
}

type translatedPhrase struct {
	event  realtimev1.PhraseSubtitleEvent
	result translate.Result
	err    error
	done   bool
}

func NewPhraseTranslationCoordinator(translator translate.Provider, provider string, observer PhraseSubtitleObserver, now func() time.Time) *PhraseTranslationCoordinator {
	if translator == nil || observer == nil {
		return nil
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &PhraseTranslationCoordinator{translator: translator, provider: provider, observer: observer, now: now, utterances: make(map[string]*phraseTranslationUtterance)}
}

func (c *PhraseTranslationCoordinator) StartPhraseSubtitleTurn(turn TurnContext, sourceLanguage string) {
	if c == nil || turn.ID == "" {
		return
	}
	target, _, ok := targetRoute(turn.LanguageConfig, asr.NormalizeLanguage(sourceLanguage))
	if !ok {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.mu.Lock()
	c.utterances[turn.ID] = &phraseTranslationUtterance{turn: turn, source: asr.NormalizeLanguage(sourceLanguage), target: target, ctx: ctx, cancel: cancel, phrases: make(map[int64]*translatedPhrase), next: 1}
	c.mu.Unlock()
}

func (c *PhraseTranslationCoordinator) ObservePhraseSubtitle(ctx context.Context, event realtimev1.PhraseSubtitleEvent) {
	if c == nil || event.Status != realtimev1.PhraseSubtitleSourceStable {
		return
	}
	c.observer.ObservePhraseSubtitle(ctx, event)
	c.mu.Lock()
	utterance := c.utterances[event.UtteranceID]
	if utterance == nil || utterance.phrases[event.PhraseSequence] != nil {
		c.mu.Unlock()
		return
	}
	phrase := &translatedPhrase{event: event}
	utterance.phrases[event.PhraseSequence] = phrase
	utterance.pending.Add(1)
	c.mu.Unlock()
	go c.translate(utterance, phrase)
}

func (c *PhraseTranslationCoordinator) translate(utterance *phraseTranslationUtterance, phrase *translatedPhrase) {
	defer utterance.pending.Done()
	result, err := c.translator.Translate(utterance.ctx, translate.Request{SessionID: utterance.turn.SessionID, TurnID: utterance.turn.ID, Text: phrase.event.SourceText, SourceLanguage: utterance.source, TargetLanguage: utterance.target})
	c.mu.Lock()
	phrase.result, phrase.err, phrase.done = result, err, true
	c.publishReadyLocked(utterance)
	c.mu.Unlock()
}

func (c *PhraseTranslationCoordinator) publishReadyLocked(utterance *phraseTranslationUtterance) {
	for {
		phrase := utterance.phrases[utterance.next]
		if phrase == nil || !phrase.done {
			return
		}
		event := phrase.event
		event.OccurredAt = c.now()
		if phrase.err != nil || strings.TrimSpace(phrase.result.Text) == "" {
			event.Status = realtimev1.PhraseSubtitleTranslationFailed
		} else {
			event.Status, event.TranslatedText = realtimev1.PhraseSubtitleTranslated, phrase.result.Text
		}
		c.observer.ObservePhraseSubtitle(context.Background(), event)
		utterance.next++
	}
}

func (c *PhraseTranslationCoordinator) FinalizePhraseSubtitleTurn(ctx context.Context, turn TurnContext, finalText string) (PhraseTranslationSummary, bool) {
	if c == nil {
		return PhraseTranslationSummary{}, false
	}
	c.mu.Lock()
	utterance := c.utterances[turn.ID]
	c.mu.Unlock()
	if utterance == nil {
		return PhraseTranslationSummary{}, false
	}
	defer c.DiscardPhraseSubtitleTurn(turn.ID)
	done := make(chan struct{})
	go func() { utterance.pending.Wait(); close(done) }()
	select {
	case <-ctx.Done():
		return PhraseTranslationSummary{}, false
	case <-done:
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return phraseSummary(finalText, utterance)
}

func (c *PhraseTranslationCoordinator) DiscardPhraseSubtitleTurn(turnID string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.discardLocked(turnID)
}

func (c *PhraseTranslationCoordinator) discardLocked(turnID string) {
	if utterance := c.utterances[turnID]; utterance != nil {
		utterance.cancel()
		delete(c.utterances, turnID)
	}
}

func phraseSummary(finalText string, utterance *phraseTranslationUtterance) (PhraseTranslationSummary, bool) {
	var summary PhraseTranslationSummary
	cursor := 0
	for sequence := int64(1); ; sequence++ {
		phrase := utterance.phrases[sequence]
		if phrase == nil {
			break
		}
		if !phrase.done || phrase.err != nil || strings.TrimSpace(phrase.result.Text) == "" {
			return PhraseTranslationSummary{}, false
		}
		index := strings.Index(finalText[cursor:], phrase.event.SourceText)
		if index < 0 || strings.TrimSpace(finalText[cursor:cursor+index]) != "" {
			return PhraseTranslationSummary{}, false
		}
		summary.Text += finalText[cursor:cursor+index] + phrase.result.Text
		cursor += index + len(phrase.event.SourceText)
		if summary.Provider == "" {
			summary.Provider, summary.Model, summary.CostAmount, summary.Currency = phrase.result.Provider, phrase.result.Model, phrase.result.CostAmount, phrase.result.Currency
		}
		if summary.Provider != phrase.result.Provider || summary.Model != phrase.result.Model || summary.Currency != phrase.result.Currency {
			return PhraseTranslationSummary{}, false
		}
		if sequence > 1 {
			var ok bool
			summary.CostAmount, ok = addPhraseCost(summary.CostAmount, phrase.result.CostAmount)
			if !ok {
				return PhraseTranslationSummary{}, false
			}
		}
		summary.InputTokens += phrase.result.InputTokens
		summary.OutputTokens += phrase.result.OutputTokens
	}
	if cursor == 0 || strings.TrimSpace(finalText[cursor:]) != "" {
		return PhraseTranslationSummary{}, false
	}
	return summary, true
}

func addPhraseCost(left, right string) (string, bool) {
	if left == "" || right == "" {
		return "", true
	}
	leftValue, leftOK := new(big.Rat).SetString(left)
	rightValue, rightOK := new(big.Rat).SetString(right)
	if !leftOK || !rightOK {
		return "", false
	}
	scale := decimalScale(left)
	if rightScale := decimalScale(right); rightScale > scale {
		scale = rightScale
	}
	result := new(big.Rat).Add(leftValue, rightValue).FloatString(scale)
	return strings.TrimRight(strings.TrimRight(result, "0"), "."), true
}

func decimalScale(value string) int {
	if point := strings.IndexByte(value, '.'); point >= 0 {
		return len(value) - point - 1
	}
	return 0
}
