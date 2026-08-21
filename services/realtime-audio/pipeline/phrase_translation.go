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
	ResidualSegments                            []string
}

// PhraseTranslationCoordinator translates stable source phrases without blocking ASR reads.
// It owns only ephemeral per-utterance state; FinalTurn persistence remains in PipelineService.
type PhraseTranslationCoordinator struct {
	translator translate.Provider
	provider   string
	observer   PhraseSubtitleObserver
	now        func() time.Time
	lateUsage  func(UsageFact)

	mu         sync.Mutex
	utterances map[string]*phraseTranslationUtterance
}

type phraseTranslationUtterance struct {
	turn           TurnContext
	source, target string
	ctx            context.Context
	cancel         context.CancelFunc
	phrases        map[int64]*translatedPhrase
	next           int64
	observerMu     sync.Mutex
	sourceTail     chan struct{}
	sourceOnly     bool
	// Final residual settlement owns unresolved phrases. A provider result that
	// arrives after that handoff must not create a second usage fact.
	suppressLateUsage bool
}

type translatedPhrase struct {
	event           realtimev1.PhraseSubtitleEvent
	result          translate.Result
	err             error
	done            bool
	doneCh          chan struct{}
	sourceDelivered chan struct{}
	usageHanded     bool
}

func NewPhraseTranslationCoordinator(translator translate.Provider, provider string, observer PhraseSubtitleObserver, now func() time.Time) *PhraseTranslationCoordinator {
	if translator == nil || observer == nil {
		return nil
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &PhraseTranslationCoordinator{
		translator: translator, provider: provider, observer: observer, now: now,
		utterances: make(map[string]*phraseTranslationUtterance),
	}
}

// SetLatePhraseUsageReporter installs the PipelineService-owned durable usage
// boundary for results that arrive after finalization has returned.
func (c *PhraseTranslationCoordinator) SetLatePhraseUsageReporter(reporter func(UsageFact)) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.lateUsage = reporter
	c.mu.Unlock()
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
	firstSource := make(chan struct{})
	close(firstSource)
	c.mu.Lock()
	c.utterances[turn.ID] = &phraseTranslationUtterance{
		turn: turn, source: asr.NormalizeLanguage(sourceLanguage), target: target,
		ctx: ctx, cancel: cancel, phrases: make(map[int64]*translatedPhrase), next: 1, sourceTail: firstSource,
	}
	c.mu.Unlock()
}

func (c *PhraseTranslationCoordinator) BeginPhraseSubtitleFinalFlush(turnID string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	if utterance := c.utterances[turnID]; utterance != nil {
		utterance.sourceOnly = true
	}
	c.mu.Unlock()
}

func (c *PhraseTranslationCoordinator) EndPhraseSubtitleFinalFlush(turnID string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	if utterance := c.utterances[turnID]; utterance != nil {
		utterance.sourceOnly = false
	}
	c.mu.Unlock()
}

func (c *PhraseTranslationCoordinator) ObservePhraseSubtitle(ctx context.Context, event realtimev1.PhraseSubtitleEvent) {
	if c == nil || event.Status != realtimev1.PhraseSubtitleSourceStable {
		return
	}
	c.mu.Lock()
	utterance := c.utterances[event.UtteranceID]
	if utterance == nil || utterance.phrases[event.PhraseSequence] != nil {
		c.mu.Unlock()
		return
	}
	phrase := &translatedPhrase{event: event, doneCh: make(chan struct{}), sourceDelivered: make(chan struct{})}
	previousSource := utterance.sourceTail
	utterance.sourceTail = phrase.sourceDelivered
	utterance.phrases[event.PhraseSequence] = phrase
	sourceOnly := utterance.sourceOnly
	c.mu.Unlock()
	go c.publishSourcePhrase(utterance, phrase, ctx, previousSource)
	if !sourceOnly {
		go c.translate(utterance, phrase)
	}
}

func (c *PhraseTranslationCoordinator) publishSourcePhrase(utterance *phraseTranslationUtterance, phrase *translatedPhrase, ctx context.Context, previous <-chan struct{}) {
	<-previous
	defer close(phrase.sourceDelivered)
	if !c.activePhraseSubtitleTurn(utterance) {
		return
	}
	utterance.observerMu.Lock()
	c.observer.ObservePhraseSubtitle(ctx, phrase.event)
	utterance.observerMu.Unlock()
}

func (c *PhraseTranslationCoordinator) translate(utterance *phraseTranslationUtterance, phrase *translatedPhrase) {
	result, err := c.translator.Translate(utterance.ctx, translate.Request{SessionID: utterance.turn.SessionID, TurnID: utterance.turn.ID, Text: phrase.event.SourceText, SourceLanguage: utterance.source, TargetLanguage: utterance.target})
	c.mu.Lock()
	phrase.result, phrase.err, phrase.done = result, err, true
	close(phrase.doneCh)
	lateUsage, usageErr := c.latePhraseUsageLocked(utterance, phrase)
	c.mu.Unlock()
	if usageErr == nil && lateUsage.ID != "" {
		c.reportLatePhraseUsage(lateUsage)
	}
	if !c.activePhraseSubtitleTurn(utterance) {
		return
	}
	<-phrase.sourceDelivered
	c.mu.Lock()
	if c.utterances[utterance.turn.ID] != utterance {
		c.mu.Unlock()
		return
	}
	events := c.publishReadyLocked(utterance)
	c.mu.Unlock()
	c.publishPhraseEvents(utterance, events)
}

func (c *PhraseTranslationCoordinator) publishReadyLocked(utterance *phraseTranslationUtterance) []realtimev1.PhraseSubtitleEvent {
	var events []realtimev1.PhraseSubtitleEvent
	for {
		phrase := utterance.phrases[utterance.next]
		if phrase == nil || !phrase.done || !sourcePhraseDelivered(phrase) {
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

func (c *PhraseTranslationCoordinator) publishPhraseEvents(utterance *phraseTranslationUtterance, events []realtimev1.PhraseSubtitleEvent) {
	if len(events) == 0 {
		return
	}
	// Keep translated notifications in sequence without holding state ownership
	// while a transport observer waits for a client channel.
	utterance.observerMu.Lock()
	defer utterance.observerMu.Unlock()
	if !c.activePhraseSubtitleTurn(utterance) {
		return
	}
	for _, event := range events {
		c.observer.ObservePhraseSubtitle(utterance.ctx, event)
	}
}

func (c *PhraseTranslationCoordinator) activePhraseSubtitleTurn(utterance *phraseTranslationUtterance) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.utterances[utterance.turn.ID] == utterance
}

func sourcePhraseDelivered(phrase *translatedPhrase) bool {
	select {
	case <-phrase.sourceDelivered:
		return true
	default:
		return false
	}
}

func (c *PhraseTranslationCoordinator) FinalizePhraseSubtitleTurn(ctx context.Context, turn TurnContext, finalText string) (PhraseTranslationSummary, string, []UsageFact, bool, error) {
	if c == nil {
		return PhraseTranslationSummary{}, finalText, nil, false, nil
	}
	if ctx.Err() != nil {
		c.discardPhraseSubtitleTurn(turn.ID, true)
		return PhraseTranslationSummary{}, finalText, nil, false, nil
	}
	c.mu.Lock()
	utterance := c.utterances[turn.ID]
	c.mu.Unlock()
	if utterance == nil {
		return PhraseTranslationSummary{}, finalText, nil, false, nil
	}
	c.mu.Lock()
	summary, consumed, fullyReused := phraseSummary(finalText, utterance)
	if fullyReused {
		if consumed == len(finalText) {
			// A marker means residual settlement owns the phrase. Keep that
			// ownership on the detached state so a canceled provider result cannot
			// be reported as an additional usage fact.
			c.detachPhraseSubtitleTurnLocked(turn.ID, false, len(summary.ResidualSegments) > 0)
			c.mu.Unlock()
			return summary, "", nil, true, nil
		}
	}
	// Residual settlement will issue the single replacement request for any
	// unresolved phrase. Suppress usage from the canceled request if it returns
	// after this ownership handoff.
	usage, err := c.detachPhraseSubtitleTurnLocked(turn.ID, false, true)
	c.mu.Unlock()
	return summary, finalText[consumed:], usage, false, err
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
	usage, _ := c.detachPhraseSubtitleTurnLocked(turnID, true, false)
	c.mu.Unlock()
	c.reportLatePhraseUsageList(usage)
}

func (c *PhraseTranslationCoordinator) discardPhraseSubtitleTurn(turnID string, collectUsage bool) {
	c.mu.Lock()
	usage, _ := c.detachPhraseSubtitleTurnLocked(turnID, collectUsage, false)
	c.mu.Unlock()
	c.reportLatePhraseUsageList(usage)
}

func (c *PhraseTranslationCoordinator) detachPhraseSubtitleTurnLocked(turnID string, collectUsage, suppressLateUsage bool) ([]UsageFact, error) {
	utterance := c.utterances[turnID]
	if utterance == nil {
		return nil, nil
	}
	var usage []UsageFact
	var usageErr error
	if collectUsage {
		for _, phrase := range utterance.phrases {
			if phrase.done && hasPhraseUsage(phrase.result) {
				fact, err := c.phraseUsageFact(utterance.turn, phrase)
				if err != nil && usageErr == nil {
					usageErr = err
				} else if err == nil {
					usage = append(usage, fact)
					phrase.usageHanded = true
				}
			}
		}
	}
	utterance.suppressLateUsage = suppressLateUsage
	utterance.cancel()
	delete(c.utterances, turnID)
	return usage, usageErr
}

func (c *PhraseTranslationCoordinator) latePhraseUsageLocked(utterance *phraseTranslationUtterance, phrase *translatedPhrase) (UsageFact, error) {
	if c.utterances[utterance.turn.ID] == utterance || utterance.suppressLateUsage || phrase.usageHanded || !hasPhraseUsage(phrase.result) {
		return UsageFact{}, nil
	}
	fact, err := c.phraseUsageFact(utterance.turn, phrase)
	if err != nil {
		return UsageFact{}, err
	}
	phrase.usageHanded = true
	return fact, nil
}

func (c *PhraseTranslationCoordinator) reportLatePhraseUsageList(facts []UsageFact) {
	for _, fact := range facts {
		c.reportLatePhraseUsage(fact)
	}
}

func (c *PhraseTranslationCoordinator) reportLatePhraseUsage(fact UsageFact) {
	c.mu.Lock()
	reporter := c.lateUsage
	c.mu.Unlock()
	if reporter != nil {
		go reporter(fact)
	}
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

func (c *PhraseTranslationCoordinator) phraseUsageFact(turn TurnContext, phrase *translatedPhrase) (UsageFact, error) {
	return buildUsageFactWithIdentity(
		turn, "translation", phrase.result.Provider, phrase.result.Model, 0,
		phrase.result.InputTokens, phrase.result.OutputTokens, phrase.result.CostAmount, phrase.result.Currency,
		fmt.Sprintf("usage_%s_phrase_%d", turn.ID, phrase.event.PhraseSequence),
		fmt.Sprintf("usage:%s:phrase:%d", turn.ID, phrase.event.PhraseSequence), c.now(),
	)
}

const phraseResidualMarker = "\x00"

// phraseSummary builds a final translation template without waiting for phrase
// workers. Successful phrases are reused; unresolved source segments are replaced
// by markers and translated once by the final settlement path.
func phraseSummary(finalText string, utterance *phraseTranslationUtterance) (PhraseTranslationSummary, int, bool) {
	var summary PhraseTranslationSummary
	cursor := 0
	covered := false
	for sequence := int64(1); ; sequence++ {
		phrase := utterance.phrases[sequence]
		if phrase == nil {
			break
		}
		index := strings.Index(finalText[cursor:], phrase.event.SourceText)
		if index < 0 || strings.TrimSpace(finalText[cursor:cursor+index]) != "" {
			return PhraseTranslationSummary{}, 0, false
		}
		summary.Text += finalText[cursor : cursor+index]
		cursor += index + len(phrase.event.SourceText)
		if phrase.done && phrase.err == nil && strings.TrimSpace(phrase.result.Text) != "" {
			summary.Text += phrase.result.Text
			if !mergePhraseUsage(&summary, phrase.result) {
				return PhraseTranslationSummary{}, 0, false
			}
		} else {
			summary.Text += phraseResidualMarker
			summary.ResidualSegments = append(summary.ResidualSegments, phrase.event.SourceText)
			if phrase.done && hasPhraseUsage(phrase.result) {
				if !mergePhraseUsage(&summary, phrase.result) {
					return PhraseTranslationSummary{}, 0, false
				}
				phrase.usageHanded = true
			}
		}
		covered = true
	}
	if !covered {
		return summary, cursor, false
	}
	if strings.TrimSpace(finalText[cursor:]) != "" {
		summary.Text += phraseResidualMarker
		summary.ResidualSegments = append(summary.ResidualSegments, finalText[cursor:])
	}
	return summary, cursor, true
}

func mergePhraseUsage(summary *PhraseTranslationSummary, result translate.Result) bool {
	if !hasPhraseUsage(result) {
		return true
	}
	hadUsage := summary.Provider != ""
	if !hadUsage {
		summary.Provider, summary.Model, summary.CostAmount, summary.Currency = result.Provider, result.Model, result.CostAmount, result.Currency
	} else if summary.Provider != result.Provider || summary.Model != result.Model || summary.Currency != result.Currency {
		return false
	}
	if hadUsage && result.CostAmount != "" {
		var ok bool
		summary.CostAmount, ok = addPhraseCost(summary.CostAmount, result.CostAmount)
		if !ok {
			return false
		}
	}
	summary.InputTokens += result.InputTokens
	summary.OutputTokens += result.OutputTokens
	return true
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
