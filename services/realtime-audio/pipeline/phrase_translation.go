package pipeline

import (
	"context"
	"fmt"
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
	usage      UsageFactSink
	now        func() time.Time

	mu         sync.Mutex
	observerMu sync.Mutex
	utterances map[string]*phraseTranslationUtterance
}

type phraseTranslationUtterance struct {
	turn           TurnContext
	source, target string
	ctx            context.Context
	cancel         context.CancelFunc
	phrases        map[int64]*translatedPhrase
	next           int64
	recordUsage    bool
}

type translatedPhrase struct {
	event          realtimev1.PhraseSubtitleEvent
	result         translate.Result
	err            error
	done           bool
	usagePublished bool
}

func NewPhraseTranslationCoordinator(translator translate.Provider, provider string, observer PhraseSubtitleObserver, now func() time.Time, usage ...UsageFactSink) *PhraseTranslationCoordinator {
	if translator == nil || observer == nil {
		return nil
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	var usageSink UsageFactSink
	if len(usage) > 0 {
		usageSink = usage[0]
	}
	return &PhraseTranslationCoordinator{translator: translator, provider: provider, observer: observer, usage: usageSink, now: now, utterances: make(map[string]*phraseTranslationUtterance)}
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
	c.mu.Unlock()
	go c.translate(utterance, phrase)
}

func (c *PhraseTranslationCoordinator) translate(utterance *phraseTranslationUtterance, phrase *translatedPhrase) {
	result, err := c.translator.Translate(utterance.ctx, translate.Request{SessionID: utterance.turn.SessionID, TurnID: utterance.turn.ID, Text: phrase.event.SourceText, SourceLanguage: utterance.source, TargetLanguage: utterance.target})
	c.mu.Lock()
	phrase.result, phrase.err, phrase.done = result, err, true
	events := c.publishReadyLocked(utterance)
	recordUsage := utterance.recordUsage
	c.mu.Unlock()
	c.publishPhraseEvents(events)
	if recordUsage {
		c.publishPhraseUsage(utterance.turn, phrase)
	}
}

func (c *PhraseTranslationCoordinator) publishReadyLocked(utterance *phraseTranslationUtterance) []realtimev1.PhraseSubtitleEvent {
	var events []realtimev1.PhraseSubtitleEvent
	for {
		phrase := utterance.phrases[utterance.next]
		if phrase == nil || !phrase.done {
			return events
		}
		event := phrase.event
		event.OccurredAt = c.now()
		if phrase.err != nil || strings.TrimSpace(phrase.result.Text) == "" {
			event.Status = realtimev1.PhraseSubtitleTranslationFailed
		} else {
			event.Status, event.TranslatedText = realtimev1.PhraseSubtitleTranslated, phrase.result.Text
		}
		events = append(events, event)
		utterance.next++
	}
}

func (c *PhraseTranslationCoordinator) publishPhraseEvents(events []realtimev1.PhraseSubtitleEvent) {
	if len(events) == 0 {
		return
	}
	// Keep translated notifications in sequence without holding state ownership
	// while a transport observer waits for a client channel.
	c.observerMu.Lock()
	defer c.observerMu.Unlock()
	for _, event := range events {
		c.observer.ObservePhraseSubtitle(context.Background(), event)
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
	c.mu.Lock()
	allDone := allPhraseTranslationsDone(utterance)
	if allDone {
		summary, ok := phraseSummary(finalText, utterance)
		if ok {
			c.mu.Unlock()
			c.discardPhraseSubtitleTurn(turn.ID, false)
			return summary, true
		}
	}
	toPublish := c.detachPhraseSubtitleTurnLocked(turn.ID, true)
	c.mu.Unlock()
	c.publishPhraseUsageList(turn, toPublish)
	return PhraseTranslationSummary{}, false
}

func (c *PhraseTranslationCoordinator) DiscardPhraseSubtitleTurn(turnID string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	utterance := c.utterances[turnID]
	if utterance == nil {
		c.mu.Unlock()
		return
	}
	toPublish := c.detachPhraseSubtitleTurnLocked(turnID, true)
	turn := utterance.turn
	c.mu.Unlock()
	c.publishPhraseUsageList(turn, toPublish)
}

func (c *PhraseTranslationCoordinator) discardPhraseSubtitleTurn(turnID string, recordUsage bool) {
	c.mu.Lock()
	utterance := c.utterances[turnID]
	var turn TurnContext
	if utterance != nil {
		turn = utterance.turn
	}
	toPublish := c.detachPhraseSubtitleTurnLocked(turnID, recordUsage)
	c.mu.Unlock()
	c.publishPhraseUsageList(turn, toPublish)
}

func (c *PhraseTranslationCoordinator) detachPhraseSubtitleTurnLocked(turnID string, recordUsage bool) []*translatedPhrase {
	utterance := c.utterances[turnID]
	if utterance == nil {
		return nil
	}
	utterance.recordUsage = recordUsage
	var toPublish []*translatedPhrase
	if recordUsage {
		for _, phrase := range utterance.phrases {
			if phrase.done && !phrase.usagePublished && hasPhraseUsage(phrase.result) {
				phrase.usagePublished = true
				toPublish = append(toPublish, phrase)
			}
		}
	}
	utterance.cancel()
	delete(c.utterances, turnID)
	return toPublish
}

func allPhraseTranslationsDone(utterance *phraseTranslationUtterance) bool {
	for _, phrase := range utterance.phrases {
		if !phrase.done {
			return false
		}
	}
	return true
}

func hasPhraseUsage(result translate.Result) bool {
	return strings.TrimSpace(result.Provider) != "" && strings.TrimSpace(result.Model) != "" && (result.InputTokens != 0 || result.OutputTokens != 0 || result.CostAmount != "")
}

func (c *PhraseTranslationCoordinator) publishPhraseUsageList(turn TurnContext, phrases []*translatedPhrase) {
	for _, phrase := range phrases {
		c.publishPhraseUsage(turn, phrase)
	}
}

func (c *PhraseTranslationCoordinator) publishPhraseUsage(turn TurnContext, phrase *translatedPhrase) {
	if c == nil || c.usage == nil || !hasPhraseUsage(phrase.result) {
		return
	}
	fact, err := buildUsageFactWithIdentity(
		turn, "translation", phrase.result.Provider, phrase.result.Model, 0,
		phrase.result.InputTokens, phrase.result.OutputTokens, phrase.result.CostAmount, phrase.result.Currency,
		fmt.Sprintf("usage_%s_phrase_%d", turn.ID, phrase.event.PhraseSequence),
		fmt.Sprintf("usage:%s:phrase:%d", turn.ID, phrase.event.PhraseSequence), c.now(),
	)
	if err == nil {
		_ = c.usage.Publish(context.Background(), fact)
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
