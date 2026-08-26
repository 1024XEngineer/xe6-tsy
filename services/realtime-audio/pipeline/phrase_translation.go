package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"sync"
	"time"
	"unicode"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/asr"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/translate"
)

// PhraseTranslationSummary is the aggregate provider result reused by the final Turn.
type PhraseTranslationSummary struct {
	Text, Provider, Model, CostAmount, Currency string
	InputTokens, OutputTokens                   int64
	ResidualSegments                            []string
	PlaybackResidualText                        string
	// PlaybackResidualSegments preserves phrase order when a failed or
	// unaccepted phrase leaves later translated text waiting behind it.
	// PlaybackResidualText remains the compact representation for callers that
	// only need a single fallback string.
	PlaybackResidualSegments []string
}

// PhraseTranslationCoordinator translates stable source phrases without blocking ASR reads.
// It owns only ephemeral per-utterance state; FinalTurn persistence remains in PipelineService.
type PhraseTranslationCoordinator struct {
	translator translate.Provider
	provider   string
	observer   PhraseSubtitleObserver
	playback   PhrasePlaybackScheduler
	now        func() time.Time
	lateUsage  func(UsageFact)

	mu         sync.Mutex
	utterances map[string]*phraseTranslationUtterance
}

// SetPhrasePlaybackScheduler attaches optional audio output to the existing
// ordered phrase translation stream. Subtitle delivery remains independent.
func (c *PhraseTranslationCoordinator) SetPhrasePlaybackScheduler(scheduler PhrasePlaybackScheduler) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.playback = scheduler
	c.mu.Unlock()
}

type phraseTranslationUtterance struct {
	turn           TurnContext
	source, target string
	ctx            context.Context
	cancel         context.CancelFunc
	phrases        map[int64]*translatedPhrase
	next           int64
	observerMu     sync.Mutex
	playbackMu     sync.Mutex
	sourceTail     chan struct{}
	sourceOnly     bool
	playbackNext   int64
	playbackReady  map[int64]*translatedPhrase
	playbackFailed bool
}

type translatedPhrase struct {
	event                  realtimev1.PhraseSubtitleEvent
	result                 translate.Result
	err                    error
	done                   bool
	translationStarted     bool
	streamed               bool
	streamPlaybackSequence int64
	playbackResidualText   string
	doneCh                 chan struct{}
	playbackDoneCh         chan struct{}
	sourceDelivered        chan struct{}
	usageHanded            bool
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
		slog.Warn("phrase_turn_route_unavailable", "session_id", turn.SessionID, "turn_id", turn.ID,
			"source_language", sourceLanguage, "normalized_source", asr.NormalizeLanguage(sourceLanguage),
			"language_pairs", turn.LanguageConfig.LanguagePairs)
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	firstSource := make(chan struct{})
	close(firstSource)
	c.mu.Lock()
	c.utterances[turn.ID] = &phraseTranslationUtterance{
		turn: turn, source: asr.NormalizeLanguage(sourceLanguage), target: target,
		ctx: ctx, cancel: cancel, phrases: make(map[int64]*translatedPhrase), next: 1, sourceTail: firstSource,
		playbackNext: 1, playbackReady: make(map[int64]*translatedPhrase),
	}
	playback := c.playback
	if playback != nil {
		playback.ResetUtterance(turn.SessionID, turn.ID)
	}
	c.mu.Unlock()
	_, streamProvider := c.translator.(translate.StreamProvider)
	slog.Info("phrase_turn_ready", "session_id", turn.SessionID, "turn_id", turn.ID,
		"source_language", asr.NormalizeLanguage(sourceLanguage), "target_language", target,
		"stream_provider", streamProvider, "translator_type", fmt.Sprintf("%T", c.translator))
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
	phrase := &translatedPhrase{
		event: event, doneCh: make(chan struct{}), playbackDoneCh: make(chan struct{}),
		sourceDelivered: make(chan struct{}),
	}
	previousSource := utterance.sourceTail
	utterance.sourceTail = phrase.sourceDelivered
	utterance.phrases[event.PhraseSequence] = phrase
	sourceOnly := utterance.sourceOnly
	phrase.translationStarted = !sourceOnly
	c.mu.Unlock()
	slog.Info("phrase_translation_queued", "session_id", event.SessionID, "turn_id", event.UtteranceID,
		"phrase_sequence", event.PhraseSequence, "source_text", event.SourceText, "source_only", sourceOnly)
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
	slog.Info("phrase_translation_started", "session_id", utterance.turn.SessionID, "turn_id", utterance.turn.ID,
		"phrase_sequence", phrase.event.PhraseSequence, "source_text", phrase.event.SourceText)
	request := translate.Request{SessionID: utterance.turn.SessionID, TurnID: utterance.turn.ID, Text: phrase.event.SourceText, SourceLanguage: utterance.source, TargetLanguage: utterance.target}
	var result translate.Result
	var err error
	_, streamed := c.translator.(translate.StreamProvider)
	if streaming, ok := c.translator.(translate.StreamProvider); ok {
		// The phrase stabilizer starts this request while ASR is still active.
		// Keep streamed deltas provisional until the provider validates the
		// complete response. Qwen may fall back to a reinforced retry after
		// detecting a refusal or prompt-injection response; enqueueing deltas
		// before that decision would speak the rejected response first.
		result, err = streaming.TranslateStream(utterance.ctx, request, nil)
	} else {
		result, err = c.translator.Translate(utterance.ctx, request)
	}
	c.mu.Lock()
	phrase.result, phrase.err, phrase.streamed = result, err, streamed
	c.mu.Unlock()
	// Playback admission is part of phrase settlement. Signal provider
	// completion only after the ordered enqueue decision has finished, otherwise
	// VAD final can detach this utterance in the narrow window between provider
	// return and enqueue and silently lose its TTS.
	c.enqueueTranslatedPhrasePlayback(utterance, phrase)
	c.mu.Lock()
	phrase.done = true
	close(phrase.doneCh)
	lateUsage, usageErr := c.latePhraseUsageLocked(utterance, phrase)
	c.mu.Unlock()
	slog.Info("phrase_translation_done", "session_id", utterance.turn.SessionID, "turn_id", utterance.turn.ID,
		"phrase_sequence", phrase.event.PhraseSequence, "error", err, "translated_runes", len([]rune(result.Text)),
		"provider", result.Provider, "model", result.Model)
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

func shouldFlushStreamTTS(text string) bool {
	if strings.TrimSpace(text) == "" {
		return false
	}
	runes := []rune(text)
	if strings.ContainsRune(".!?,;:\u3002\uff01\uff1f\uff0c\uff1b\uff1a\u3001\n", runes[len(runes)-1]) {
		return true
	}
	// For languages with spaces, wait for a word boundary inside the 20-40
	// character window. CJK scripts do not require a synthetic separator, so
	// they can keep the lower-latency 32-character target.
	if len(runes) >= 20 && unicode.IsSpace(runes[len(runes)-1]) {
		return true
	}
	if len(runes) >= 32 && unicode.In(runes[len(runes)-1], unicode.Han, unicode.Hiragana, unicode.Katakana) {
		return true
	}
	return len(runes) >= 40
}

func splitStreamTTS(text string) []string {
	var chunks []string
	var buffer strings.Builder
	for _, r := range text {
		buffer.WriteRune(r)
		if shouldFlushStreamTTS(buffer.String()) {
			chunks = append(chunks, strings.TrimSpace(buffer.String()))
			buffer.Reset()
		}
	}
	if tail := strings.TrimSpace(buffer.String()); tail != "" {
		chunks = append(chunks, tail)
	}
	return chunks
}

func (c *PhraseTranslationCoordinator) enqueueStreamPhrasePlayback(utterance *phraseTranslationUtterance, phrase *translatedPhrase, playback PhrasePlaybackScheduler, text string) {
	text = strings.TrimSpace(text)
	if c == nil || utterance == nil || phrase == nil || text == "" {
		return
	}
	phrase.streamPlaybackSequence++
	sequence := phrase.streamPlaybackSequence
	if utterance.playbackFailed || playback == nil {
		utterance.playbackFailed = true
		phrase.playbackResidualText = joinPhrasePlaybackText(phrase.playbackResidualText, text)
		return
	}
	request := PhrasePlaybackRequest{
		Turn: utterance.turn, UtteranceID: phrase.event.UtteranceID,
		PhraseSequence: phrase.event.PhraseSequence*1000 + sequence,
		PhraseGroup:    phrase.event.PhraseSequence,
		Language:       utterance.target, Text: text,
		PlaybackID: fmt.Sprintf("phrase_%s_%d_%d", phrase.event.UtteranceID, phrase.event.PhraseSequence, sequence),
	}
	result := enqueuePhrasePlayback(playback, request)
	if !result.Accepted {
		utterance.playbackFailed = true
		phrase.playbackResidualText = joinPhrasePlaybackText(phrase.playbackResidualText, text)
		slog.Warn("phrase_tts_enqueue_failed", "session_id", utterance.turn.SessionID, "turn_id", utterance.turn.ID,
			"phrase_sequence", phrase.event.PhraseSequence, "stream_sequence", sequence, "reason", result.Reason)
		return
	}
	slog.Info("phrase_tts_enqueued", "session_id", utterance.turn.SessionID, "turn_id", utterance.turn.ID,
		"phrase_sequence", phrase.event.PhraseSequence, "stream_sequence", sequence, "text", text)
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

// enqueueTranslatedPhrasePlayback is independent of source subtitle delivery:
// a blocked client observer must not delay low-latency audio or final settlement.
// Translation completion may arrive out of order, so ready phrases are drained
// under the coordinator lock in sequence order before the final tail is added.
func (c *PhraseTranslationCoordinator) enqueueTranslatedPhrasePlayback(utterance *phraseTranslationUtterance, phrase *translatedPhrase) {
	// Admission may apply scheduler back-pressure. Serialize it per utterance so
	// phrase order is stable without holding the coordinator-wide mutex while a
	// different session is waiting for playback capacity.
	utterance.playbackMu.Lock()
	defer utterance.playbackMu.Unlock()

	c.mu.Lock()
	if c.utterances[utterance.turn.ID] != utterance {
		c.mu.Unlock()
		return
	}
	utterance.playbackReady[phrase.event.PhraseSequence] = phrase
	var readyPhrases []*translatedPhrase
	for {
		ready := utterance.playbackReady[utterance.playbackNext]
		if ready == nil {
			break
		}
		readyPhrases = append(readyPhrases, ready)
		delete(utterance.playbackReady, utterance.playbackNext)
		utterance.playbackNext++
	}
	playback := c.playback
	c.mu.Unlock()

	for _, ready := range readyPhrases {
		text := strings.TrimSpace(ready.result.Text)
		if ready.err != nil || text == "" {
			// A failed translation creates a gap in the spoken prefix. Keep all
			// later target text behind final residual settlement so it cannot play
			// out of order before the retry for this source segment.
			utterance.playbackFailed = true
		} else if ready.streamed {
			// Stream deltas remain provisional until provider validation. Split
			// the validated result here, after earlier phrase sequences have
			// settled, so concurrently completed phrases cannot reach TTS out of
			// source order.
			for _, chunk := range splitStreamTTS(text) {
				c.enqueueStreamPhrasePlayback(utterance, ready, playback, chunk)
			}
		} else if utterance.playbackFailed || playback == nil {
			utterance.playbackFailed = true
			ready.playbackResidualText = joinPhrasePlaybackText(ready.playbackResidualText, text)
		} else {
			result := enqueuePhrasePlayback(playback, PhrasePlaybackRequest{
				Turn: utterance.turn, UtteranceID: ready.event.UtteranceID,
				PhraseSequence: ready.event.PhraseSequence, Language: utterance.target,
				Text:       ready.result.Text,
				PlaybackID: fmt.Sprintf("phrase_%s_%d", ready.event.UtteranceID, ready.event.PhraseSequence),
			})
			if !result.Accepted {
				utterance.playbackFailed = true
				ready.playbackResidualText = joinPhrasePlaybackText(ready.playbackResidualText, text)
				slog.Warn("phrase_tts_enqueue_failed", "session_id", utterance.turn.SessionID,
					"turn_id", utterance.turn.ID, "phrase_sequence", ready.event.PhraseSequence,
					"reason", result.Reason)
			}
		}
		close(ready.playbackDoneCh)
	}
}

type phrasePlaybackReasonEnqueuer interface {
	EnqueueWithReason(PhrasePlaybackRequest) PhrasePlaybackEnqueueResult
}

func enqueuePhrasePlayback(playback PhrasePlaybackScheduler, request PhrasePlaybackRequest) PhrasePlaybackEnqueueResult {
	if detailed, ok := playback.(phrasePlaybackReasonEnqueuer); ok {
		return detailed.EnqueueWithReason(request)
	}
	if playback.Enqueue(request) {
		return PhrasePlaybackEnqueueResult{Accepted: true}
	}
	return PhrasePlaybackEnqueueResult{Reason: PhrasePlaybackRejectBacklogLimit}
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
	if !c.waitForPendingPhrases(ctx, utterance) {
		c.discardPhraseSubtitleTurn(turn.ID, true)
		return PhraseTranslationSummary{}, finalText, nil, false, nil
	}
	c.mu.Lock()
	if c.utterances[turn.ID] != utterance {
		c.mu.Unlock()
		return PhraseTranslationSummary{}, finalText, nil, false, nil
	}
	// A phrase summary remains useful even when the final ASR text has an
	// unconfirmed suffix.  The summary contains already translated phrases and
	// residual markers for failed/unconfirmed source segments; the caller must
	// translate only those residual segments and must not fall back to the whole
	// Turn.  The bool therefore means "phrase coverage was established", not
	// "the summary covers every byte of finalText".
	summary, consumed, phraseCovered := phraseSummary(finalText, utterance)
	usage, err := c.detachPhraseSubtitleTurnLocked(turn.ID, false)
	c.mu.Unlock()
	if !phraseCovered {
		// No stable phrase matched the final source. Return the complete source
		// to the ordinary final translator; consumed is zero in this case, but
		// using finalText explicitly keeps the contract clear if phraseSummary
		// gains another failure path later.
		return summary, finalText, usage, false, err
	}
	return summary, finalText[consumed:], usage, true, err
}

// HasPendingPhrase reports whether a provider request is already in flight for
// this utterance. Final streaming settlement uses this to hand the turn to a
// background worker instead of issuing a second request from the media path.
func (c *PhraseTranslationCoordinator) HasPendingPhrase(turnID string) bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	utterance := c.utterances[turnID]
	if utterance == nil {
		return false
	}
	for _, phrase := range utterance.phrases {
		if phrase.translationStarted && !phrase.done {
			return true
		}
	}
	return false
}

func (c *PhraseTranslationCoordinator) waitForPendingPhrases(ctx context.Context, utterance *phraseTranslationUtterance) bool {
	c.mu.Lock()
	translationDone := make([]<-chan struct{}, 0, len(utterance.phrases))
	playbackDone := make([]<-chan struct{}, 0, len(utterance.phrases))
	for _, phrase := range utterance.phrases {
		if phrase.translationStarted {
			if !phrase.done {
				translationDone = append(translationDone, phrase.doneCh)
			}
			playbackDone = append(playbackDone, phrase.playbackDoneCh)
		}
	}
	c.mu.Unlock()
	for _, done := range translationDone {
		select {
		case <-done:
		case <-ctx.Done():
			return false
		}
	}
	// Wait for admission, not audio completion. This closes the provider-return
	// race where final settlement could detach the utterance immediately before
	// its translated text entered the scheduler. The scheduler still owns actual
	// playback asynchronously, so VAD final never waits for clip duration.
	for _, done := range playbackDone {
		select {
		case <-done:
		case <-ctx.Done():
			return false
		}
	}
	return true
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
	usage, _ := c.detachPhraseSubtitleTurnLocked(turnID, true)
	c.mu.Unlock()
	c.reportLatePhraseUsageList(usage)
}

func (c *PhraseTranslationCoordinator) discardPhraseSubtitleTurn(turnID string, collectUsage bool) {
	c.mu.Lock()
	usage, _ := c.detachPhraseSubtitleTurnLocked(turnID, collectUsage)
	c.mu.Unlock()
	c.reportLatePhraseUsageList(usage)
}

func (c *PhraseTranslationCoordinator) detachPhraseSubtitleTurnLocked(turnID string, collectUsage bool) ([]UsageFact, error) {
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
	utterance.cancel()
	delete(c.utterances, turnID)
	return usage, usageErr
}

func (c *PhraseTranslationCoordinator) latePhraseUsageLocked(utterance *phraseTranslationUtterance, phrase *translatedPhrase) (UsageFact, error) {
	if c.utterances[utterance.turn.ID] == utterance || phrase.usageHanded || !hasPhraseUsage(phrase.result) {
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
		if phrase.translationStarted && !phrase.done {
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

// Internal separator used only while passing ordered residual playback from
// phrase settlement to the scheduler. Provider text cannot contain this
// control byte under the validated translation contract.
const phrasePlaybackResidualSeparator = "\x1e"

// phraseSummary builds a final translation template without waiting for phrase
// workers. Successful phrases are reused; unresolved source segments are replaced
// by markers and translated once by the final settlement path. The returned
// bool reports whether at least one stable phrase was structurally matched. It
// is intentionally true when the match has residual suffix text or failed
// phrases, because those residuals can still be settled without retranslating
// the already successful prefix.
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
		if index < 0 {
			return PhraseTranslationSummary{}, 0, false
		}
		gap := finalText[cursor : cursor+index]
		if strings.TrimSpace(gap) == "" {
			summary.Text += gap
		} else if !isIgnorableConfirmedGap(gap) {
			return PhraseTranslationSummary{}, 0, false
		}
		cursor += index + len(phrase.event.SourceText)
		if phrase.done && phrase.err == nil && strings.TrimSpace(phrase.result.Text) != "" {
			summary.Text += phrase.result.Text
			summary.PlaybackResidualText = joinPhrasePlaybackText(summary.PlaybackResidualText, phrase.playbackResidualText)
			if residual := strings.TrimSpace(phrase.playbackResidualText); residual != "" {
				summary.PlaybackResidualSegments = append(summary.PlaybackResidualSegments, residual)
			}
			if !mergePhraseUsage(&summary, phrase.result) {
				return PhraseTranslationSummary{}, 0, false
			}
		} else {
			summary.Text += phraseResidualMarker
			summary.PlaybackResidualText += phraseResidualMarker
			summary.PlaybackResidualSegments = append(summary.PlaybackResidualSegments, phraseResidualMarker)
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
	suffix := finalText[cursor:]
	if isIgnorableConfirmedGap(suffix) {
		cursor = len(finalText)
	} else if strings.TrimSpace(suffix) != "" {
		summary.Text += phraseResidualMarker
		summary.PlaybackResidualText += phraseResidualMarker
		summary.PlaybackResidualSegments = append(summary.PlaybackResidualSegments, phraseResidualMarker)
		summary.ResidualSegments = append(summary.ResidualSegments, suffix)
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
