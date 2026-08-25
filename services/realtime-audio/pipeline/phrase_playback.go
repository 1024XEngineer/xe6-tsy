package pipeline

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode"
)

// A session can contain more than one live utterance while a final settlement
// is being handed off. Keep a finite global queue as a memory guard, but apply
// back-pressure instead of discarding accepted translation text.
const maxPhrasePlaybackQueue = 40

const maxPhrasePlaybackSegmentRunes = 40

const maxRetiredPhraseUtterances = 256

const maxPhrasePlaybackSessionBarriers = 1024

const maxPhrasePlaybackAttempts = 2

var ErrPhrasePlaybackClosed = errors.New("phrase playback scheduler is closed")

const (
	PhrasePlaybackRejectBacklogLimit         = "backlog_limit"
	PhrasePlaybackRejectPlaybackClosed       = "playback_closed"
	PhrasePlaybackRejectGenerationSuperseded = "generation_superseded"
	PhrasePlaybackRejectInvalid              = "invalid_request"
)

// PhrasePlaybackEnqueueResult makes rejected playback observable without
// forcing existing callers to migrate from the bool Enqueue method.
type PhrasePlaybackEnqueueResult struct {
	Accepted bool
	Reason   string
}

// PhrasePlaybackRequest is the immutable translation accepted by the playback
// scheduler. Playback IDs are scoped to a session and phrase sequence.
type PhrasePlaybackRequest struct {
	Turn           TurnContext
	UtteranceID    string
	PhraseSequence int64
	Language       string
	Text           string
	PlaybackID     string
	Final          bool
}

// PhrasePlaybackScheduler accepts translated phrases independently from audio
// playback. Implementations preserve enqueue order per session and may apply
// back-pressure only at their hard memory boundary.
type PhrasePlaybackScheduler interface {
	ResetUtterance(sessionID, utteranceID string)
	Enqueue(PhrasePlaybackRequest) bool
	InterruptCurrent(context.Context, string, string) error
	Stop(context.Context, string) error
}

// PhrasePlaybackSchedulerDependencies wires the scheduler to the existing
// speech output and media lifecycle. No alternate media track is created.
type PhrasePlaybackSchedulerDependencies struct {
	Speech     *SpeechOutput
	Audio      AudioChunkSink
	Usage      UsageFactSink
	Now        func() time.Time
	UsageError func(PhrasePlaybackRequest, UsageFact, error)
}

type PhrasePlaybackSchedulerService struct {
	mu             sync.Mutex
	speech         *SpeechOutput
	audio          AudioChunkSink
	usage          UsageFactSink
	now            func() time.Time
	usageError     func(PhrasePlaybackRequest, UsageFact, error)
	sessions       map[string]*phrasePlaybackSession
	barriers       map[string]phrasePlaybackSessionBarrier
	barrierOrder   []phrasePlaybackSessionBarrierEntry
	nextGeneration uint64
}

type phrasePlaybackSession struct {
	sessionID string
	ctx       context.Context
	cancel    context.CancelFunc
	done      chan struct{}
	wake      chan struct{}
	// capacityChanged broadcasts to producers waiting for global backlog
	// capacity. The channel is replaced after every broadcast so producers
	// always re-check the session state under mu.
	capacityChanged chan struct{}

	queue      []*phrasePlaybackTask
	active     *phrasePlaybackTask
	closed     bool
	generation uint64
	utterances map[string]*phrasePlaybackUtterance
	// Once all tasks for a non-final phrase have drained, retain its generation
	// metadata separately. Streaming ASR may deliver the next stable phrase
	// after that drain; treating it as a superseded generation loses text.
	retired         map[string]*phrasePlaybackUtterance
	retiredOrder    []retiredPhrasePlaybackUtterance
	acceptedIDs     map[string]map[string]struct{}
	superseded      map[string]uint64
	supersededOrder []supersededPhrasePlaybackUtterance
}

type phrasePlaybackSessionBarrier struct {
	generation uint64
	reason     string
}

type phrasePlaybackSessionBarrierEntry struct {
	sessionID  string
	generation uint64
}

type supersededPhrasePlaybackUtterance struct {
	id         string
	generation uint64
}

type retiredPhrasePlaybackUtterance struct {
	id        string
	utterance *phrasePlaybackUtterance
}

type phrasePlaybackUtterance struct {
	unfinished    int
	finalAccepted bool
}

type phrasePlaybackTaskStatus string

const (
	phrasePlaybackAccepted phrasePlaybackTaskStatus = "accepted"
	phrasePlaybackStarted  phrasePlaybackTaskStatus = "started"
	phrasePlaybackPlayed   phrasePlaybackTaskStatus = "played"
	phrasePlaybackFailed   phrasePlaybackTaskStatus = "failed"
	phrasePlaybackCanceled phrasePlaybackTaskStatus = "canceled"
)

type phrasePlaybackTask struct {
	request    PhrasePlaybackRequest
	generation uint64
	cancel     context.CancelFunc
	status     phrasePlaybackTaskStatus
	utterance  *phrasePlaybackUtterance
	attempts   int
}

// NewPhrasePlaybackScheduler creates one session worker lazily per active
// session. A missing speech boundary disables phrase audio while preserving
// phrase subtitles and final settlement.
func NewPhrasePlaybackScheduler(deps PhrasePlaybackSchedulerDependencies) *PhrasePlaybackSchedulerService {
	if deps.Now == nil {
		deps.Now = func() time.Time { return time.Now().UTC() }
	}
	return &PhrasePlaybackSchedulerService{
		speech: deps.Speech, audio: deps.Audio, usage: deps.Usage, now: deps.Now,
		usageError:     deps.UsageError,
		sessions:       make(map[string]*phrasePlaybackSession),
		barriers:       make(map[string]phrasePlaybackSessionBarrier),
		nextGeneration: 1,
	}
}

func (s *PhrasePlaybackSchedulerService) ResetUtterance(sessionID, utteranceID string) {
	if s == nil || sessionID == "" || utteranceID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.barriers, sessionID)
	if state := s.sessions[sessionID]; state != nil && state.closed {
		// Stop may still be waiting for a provider that is slow to observe
		// cancellation. Detach that canceled worker before opening the new
		// lifecycle; its deferred cleanup checks identity before deleting.
		delete(s.sessions, sessionID)
	}
	state := s.sessionLocked(sessionID)
	s.removeSupersededUtteranceLocked(state, utteranceID)
	// Reset is the explicit lifecycle boundary for an utterance. Any retired
	// metadata and idempotency keys from a previous incarnation must not make a
	// new request look already accepted.
	s.removeRetiredUtteranceLocked(state, utteranceID)
	delete(state.acceptedIDs, utteranceID)
	state.utterances[utteranceID] = &phrasePlaybackUtterance{}
	s.signalCapacityLocked(state)
}

// Enqueue preserves the original bool API for callers that only need to know
// whether audio was accepted. Use EnqueueWithReason for diagnostics.
func (s *PhrasePlaybackSchedulerService) Enqueue(request PhrasePlaybackRequest) bool {
	return s.EnqueueWithReason(request).Accepted
}

// EnqueueWithReason accepts a phrase, coalescing adjacent queued text for the
// same utterance when the scheduler is under pressure. Already active audio
// is never removed by this operation.
func (s *PhrasePlaybackSchedulerService) EnqueueWithReason(request PhrasePlaybackRequest) PhrasePlaybackEnqueueResult {
	if s == nil || s.speech == nil || s.audio == nil || request.Turn.SessionID == "" ||
		request.Turn.ID == "" || request.UtteranceID == "" || request.PhraseSequence < 1 ||
		request.PlaybackID == "" || request.Language == "" || strings.TrimSpace(request.Text) == "" {
		return PhrasePlaybackEnqueueResult{Reason: PhrasePlaybackRejectInvalid}
	}
	var expectedGeneration uint64
	generationKnown := false
	for {
		s.mu.Lock()
		if barrier, exists := s.barriers[request.Turn.SessionID]; exists {
			s.mu.Unlock()
			return PhrasePlaybackEnqueueResult{Reason: barrier.reason}
		}
		state := s.sessionLocked(request.Turn.SessionID)
		if state.closed {
			s.mu.Unlock()
			return PhrasePlaybackEnqueueResult{Reason: PhrasePlaybackRejectPlaybackClosed}
		}
		if generationKnown && state.generation != expectedGeneration {
			s.mu.Unlock()
			return PhrasePlaybackEnqueueResult{Reason: PhrasePlaybackRejectGenerationSuperseded}
		}
		expectedGeneration, generationKnown = state.generation, true
		if _, superseded := state.superseded[request.UtteranceID]; superseded {
			s.mu.Unlock()
			return PhrasePlaybackEnqueueResult{Reason: PhrasePlaybackRejectGenerationSuperseded}
		}
		// Playback IDs are the durable idempotency boundary. A retry after a
		// transient enqueue wait must not synthesize the same text twice.
		ids := state.acceptedIDs[request.UtteranceID]
		if ids == nil {
			ids = make(map[string]struct{})
			state.acceptedIDs[request.UtteranceID] = ids
		}
		if _, exists := ids[request.PlaybackID]; exists {
			s.mu.Unlock()
			return PhrasePlaybackEnqueueResult{Accepted: true}
		}

		utterance := state.utterances[request.UtteranceID]
		if utterance == nil {
			// A drained non-final utterance is retired, not superseded. Restore
			// it so a late ASR stable phrase remains in the same generation.
			if retired := state.retired[request.UtteranceID]; retired != nil {
				if retired.finalAccepted {
					s.mu.Unlock()
					return PhrasePlaybackEnqueueResult{Reason: PhrasePlaybackRejectGenerationSuperseded}
				}
				utterance = retired
				s.removeRetiredUtteranceLocked(state, request.UtteranceID)
				state.utterances[request.UtteranceID] = utterance
			} else if !request.Final {
				s.mu.Unlock()
				return PhrasePlaybackEnqueueResult{Reason: PhrasePlaybackRejectGenerationSuperseded}
			} else {
				utterance = &phrasePlaybackUtterance{}
				state.utterances[request.UtteranceID] = utterance
			}
		}
		if utterance.finalAccepted {
			s.mu.Unlock()
			return PhrasePlaybackEnqueueResult{Reason: PhrasePlaybackRejectGenerationSuperseded}
		}

		// Streaming translation frequently yields adjacent micro-chunks. Fold
		// them into the queued tail for this utterance when the result remains a
		// natural TTS micro-segment. Only the queued tail is eligible: merging into an
		// active task would replay audio that may already be audible.
		if len(state.queue) > 0 {
			last := state.queue[len(state.queue)-1]
			if last.request.UtteranceID == request.UtteranceID && last.generation == state.generation &&
				shouldMergePhrasePlayback(last.request, request) {
				last.request.Text = joinPhrasePlaybackText(last.request.Text, request.Text)
				last.request.Final = last.request.Final || request.Final
				if request.Final {
					utterance.finalAccepted = true
				}
				ids[request.PlaybackID] = struct{}{}
				wake := state.wake
				s.mu.Unlock()
				signalChannel(wake)
				return PhrasePlaybackEnqueueResult{Accepted: true}
			}
		}

		// Once a task is accepted it is never discarded merely because the
		// queue is busy. Apply back-pressure until either a task completes or
		// the session is explicitly stopped/interrupted. This is the critical
		// difference from the old permanent-degraded path.
		if len(state.queue) >= maxPhrasePlaybackQueue {
			wait := state.capacityChanged
			s.mu.Unlock()
			<-wait
			continue
		}

		task := &phrasePlaybackTask{request: request, generation: state.generation, status: phrasePlaybackAccepted, utterance: utterance}
		state.queue = append(state.queue, task)
		utterance.unfinished++
		if request.Final {
			utterance.finalAccepted = true
		}
		ids[request.PlaybackID] = struct{}{}
		wake := state.wake
		s.mu.Unlock()
		signalChannel(wake)
		return PhrasePlaybackEnqueueResult{Accepted: true}
	}
}

func signalChannel(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

func shouldMergePhrasePlayback(left, right PhrasePlaybackRequest) bool {
	// Only streamed sub-segments are coalesced during normal operation. Stable
	// phrase requests already represent a semantic boundary selected by ASR.
	if left.PhraseSequence < 1000 || right.PhraseSequence < 1000 {
		return false
	}
	leftText := strings.TrimSpace(left.Text)
	if leftText == "" || phrasePlaybackTextEndsBoundary(leftText) {
		return false
	}
	return len([]rune(joinPhrasePlaybackText(leftText, right.Text))) <= maxPhrasePlaybackSegmentRunes
}

func phrasePlaybackTextEndsBoundary(text string) bool {
	runes := []rune(strings.TrimSpace(text))
	if len(runes) == 0 {
		return false
	}
	return strings.ContainsRune(".!?,;:\u3002\uff01\uff1f\uff0c\uff1b\uff1a\u3001\n", runes[len(runes)-1])
}

func joinPhrasePlaybackText(left, right string) string {
	left, right = strings.TrimSpace(left), strings.TrimSpace(right)
	if left == "" {
		return right
	}
	if right == "" {
		return left
	}
	leftRunes, rightRunes := []rune(left), []rune(right)
	// The chunks are contiguous slices of one validated translation. Preserve
	// Chinese and Japanese text without inventing a word boundary; whitespace-
	// delimited scripts need a separator because splitStreamTTS trims it.
	if isUnspacedCJKRune(leftRunes[len(leftRunes)-1]) || isUnspacedCJKRune(rightRunes[0]) {
		return left + right
	}
	return left + " " + right
}

func isUnspacedCJKRune(r rune) bool {
	return unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r)
}

func (s *PhrasePlaybackSchedulerService) InterruptCurrent(ctx context.Context, sessionID, reason string) error {
	if s == nil || sessionID == "" {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	state := s.sessions[sessionID]
	generation := s.allocateGenerationLocked()
	s.setBarrierLocked(sessionID, PhrasePlaybackRejectGenerationSuperseded, generation)
	if state != nil {
		state.generation = generation
		for utteranceID := range state.utterances {
			s.markSupersededUtteranceLocked(state, utteranceID, generation)
		}
		for utteranceID := range state.retired {
			s.markSupersededUtteranceLocked(state, utteranceID, generation)
		}
		for _, task := range state.queue {
			s.markSupersededUtteranceLocked(state, task.request.UtteranceID, generation)
		}
		if state.active != nil {
			s.markSupersededUtteranceLocked(state, state.active.request.UtteranceID, generation)
		}
		active := state.active
		state.queue = nil
		state.utterances = make(map[string]*phrasePlaybackUtterance)
		state.retired = make(map[string]*phrasePlaybackUtterance)
		state.retiredOrder = nil
		state.acceptedIDs = make(map[string]map[string]struct{})
		if active != nil && active.cancel != nil {
			active.cancel()
		}
		s.signalCapacityLocked(state)
	}
	s.mu.Unlock()
	if interrupter, ok := s.audio.(interface {
		InterruptCurrent(context.Context, string, string) error
	}); ok {
		return interrupter.InterruptCurrent(ctx, sessionID, reason)
	}
	return nil
}

func (s *PhrasePlaybackSchedulerService) Stop(ctx context.Context, sessionID string) error {
	if s == nil || sessionID == "" {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	state := s.sessions[sessionID]
	generation := s.allocateGenerationLocked()
	s.setBarrierLocked(sessionID, PhrasePlaybackRejectPlaybackClosed, generation)
	if state == nil {
		s.mu.Unlock()
		return nil
	}
	state.closed = true
	state.generation = generation
	state.queue = nil
	if state.active != nil && state.active.cancel != nil {
		state.active.cancel()
	}
	s.signalCapacityLocked(state)
	state.cancel()
	done := state.done
	s.mu.Unlock()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *PhrasePlaybackSchedulerService) sessionLocked(sessionID string) *phrasePlaybackSession {
	if state := s.sessions[sessionID]; state != nil {
		return state
	}
	ctx, cancel := context.WithCancel(context.Background())
	state := &phrasePlaybackSession{
		sessionID: sessionID, ctx: ctx, cancel: cancel,
		done: make(chan struct{}), wake: make(chan struct{}, 1),
		capacityChanged: make(chan struct{}),
		generation:      s.allocateGenerationLocked(),
		utterances:      make(map[string]*phrasePlaybackUtterance),
		retired:         make(map[string]*phrasePlaybackUtterance),
		acceptedIDs:     make(map[string]map[string]struct{}),
		superseded:      make(map[string]uint64),
	}
	s.sessions[sessionID] = state
	go s.runSession(state)
	return state
}

func (s *PhrasePlaybackSchedulerService) allocateGenerationLocked() uint64 {
	if s.nextGeneration == 0 {
		s.nextGeneration = 1
	}
	generation := s.nextGeneration
	s.nextGeneration++
	return generation
}

func (s *PhrasePlaybackSchedulerService) setBarrierLocked(sessionID, reason string, generation uint64) {
	if current, exists := s.barriers[sessionID]; exists &&
		current.reason == PhrasePlaybackRejectPlaybackClosed && reason != PhrasePlaybackRejectPlaybackClosed {
		// Stop is terminal until ResetUtterance explicitly opens a new
		// lifecycle. A racing interruption must not weaken that result.
		return
	}
	barrier := phrasePlaybackSessionBarrier{generation: generation, reason: reason}
	s.barriers[sessionID] = barrier
	s.barrierOrder = append(s.barrierOrder, phrasePlaybackSessionBarrierEntry{
		sessionID: sessionID, generation: generation,
	})
	for len(s.barrierOrder) > maxPhrasePlaybackSessionBarriers {
		oldest := s.barrierOrder[0]
		s.barrierOrder = s.barrierOrder[1:]
		if current, exists := s.barriers[oldest.sessionID]; exists && current.generation == oldest.generation {
			delete(s.barriers, oldest.sessionID)
		}
	}
}

func (s *PhrasePlaybackSchedulerService) markSupersededUtteranceLocked(state *phrasePlaybackSession, utteranceID string, generation uint64) {
	if state == nil || utteranceID == "" {
		return
	}
	if current, exists := state.superseded[utteranceID]; exists && current == generation {
		return
	}
	state.superseded[utteranceID] = generation
	state.supersededOrder = append(state.supersededOrder, supersededPhrasePlaybackUtterance{
		id: utteranceID, generation: generation,
	})
	for len(state.supersededOrder) > maxRetiredPhraseUtterances {
		oldest := state.supersededOrder[0]
		state.supersededOrder = state.supersededOrder[1:]
		if state.superseded[oldest.id] == oldest.generation {
			delete(state.superseded, oldest.id)
		}
	}
}

func (s *PhrasePlaybackSchedulerService) removeSupersededUtteranceLocked(state *phrasePlaybackSession, utteranceID string) {
	if state != nil {
		delete(state.superseded, utteranceID)
	}
}

func (s *PhrasePlaybackSchedulerService) removeRetiredUtteranceLocked(state *phrasePlaybackSession, utteranceID string) {
	delete(state.retired, utteranceID)
	if len(state.retiredOrder) == 0 {
		return
	}
	kept := state.retiredOrder[:0]
	for _, retired := range state.retiredOrder {
		if retired.id != utteranceID {
			kept = append(kept, retired)
		}
	}
	state.retiredOrder = kept
}

func (s *PhrasePlaybackSchedulerService) runSession(state *phrasePlaybackSession) {
	defer func() {
		s.mu.Lock()
		if s.sessions[state.sessionID] == state {
			delete(s.sessions, state.sessionID)
		}
		close(state.done)
		s.mu.Unlock()
	}()
	for {
		select {
		case <-state.ctx.Done():
			return
		case <-state.wake:
		}
		for {
			s.mu.Lock()
			if state.closed || len(state.queue) == 0 {
				s.mu.Unlock()
				break
			}
			task := state.queue[0]
			state.queue = state.queue[1:]
			if task.generation != state.generation {
				task.status = phrasePlaybackCanceled
				s.decrementLocked(state, task)
				s.signalCapacityLocked(state)
				s.mu.Unlock()
				continue
			}
			playCtx, cancel := context.WithCancel(state.ctx)
			task.cancel, state.active = cancel, task
			task.status = phrasePlaybackStarted
			s.mu.Unlock()
			playErr := s.play(task, playCtx)
			cancel()
			s.mu.Lock()
			if state.active == task {
				state.active = nil
			}
			canRetry := playErr != nil && task.attempts < maxPhrasePlaybackAttempts &&
				!state.closed && state.ctx.Err() == nil && task.generation == state.generation
			if canRetry {
				task.status = phrasePlaybackAccepted
				state.queue = append([]*phrasePlaybackTask{task}, state.queue...)
				s.signalCapacityLocked(state)
				wake := state.wake
				attempt := task.attempts
				s.mu.Unlock()
				slog.Warn("phrase_tts_playback_retry",
					"session_id", task.request.Turn.SessionID,
					"turn_id", task.request.Turn.ID,
					"utterance_id", task.request.UtteranceID,
					"phrase_sequence", task.request.PhraseSequence,
					"playback_id", task.request.PlaybackID,
					"attempt", attempt,
					"error", playErr)
				signalChannel(wake)
				continue
			}
			if playErr == nil {
				task.status = phrasePlaybackPlayed
			} else if errors.Is(playErr, context.Canceled) || errors.Is(playErr, context.DeadlineExceeded) {
				task.status = phrasePlaybackCanceled
			} else {
				task.status = phrasePlaybackFailed
			}
			s.decrementLocked(state, task)
			s.signalCapacityLocked(state)
			s.mu.Unlock()
			if playErr != nil {
				logFn := slog.Error
				if errors.Is(playErr, context.Canceled) || errors.Is(playErr, context.DeadlineExceeded) {
					logFn = slog.Warn
				}
				logFn("phrase_tts_playback_failed",
					"session_id", task.request.Turn.SessionID,
					"turn_id", task.request.Turn.ID,
					"utterance_id", task.request.UtteranceID,
					"phrase_sequence", task.request.PhraseSequence,
					"playback_id", task.request.PlaybackID,
					"attempts", task.attempts,
					"error", playErr)
			}
		}
	}
}

func (s *PhrasePlaybackSchedulerService) play(task *phrasePlaybackTask, ctx context.Context) error {
	task.attempts++
	result, err := s.speech.Play(ctx, SpeechOutputRequest{
		Turn: task.request.Turn, Language: task.request.Language, Text: task.request.Text,
		PlaybackID: task.request.PlaybackID, SkipRuntime: true,
	})
	if err != nil || s.usage == nil {
		return err
	}
	fact, err := buildUsageFactWithIdentity(task.request.Turn, "tts", result.Provider, result.Model,
		result.AudioDuration.Milliseconds(), 0, 0, result.CostAmount, result.Currency,
		fmt.Sprintf("usage_%s_tts", task.request.PlaybackID),
		fmt.Sprintf("usage:%s:tts", task.request.PlaybackID), s.now())
	if err != nil {
		s.reportUsageError(task.request, fact, err)
		return nil
	}
	for attempt := 0; attempt < 3; attempt++ {
		publishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		err = s.usage.Publish(publishCtx, fact)
		cancel()
		if err == nil {
			return nil
		}
		if attempt < 2 {
			timer := time.NewTimer(time.Duration(attempt+1) * 25 * time.Millisecond)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
			}
		}
	}
	s.reportUsageError(task.request, fact, err)
	return nil
}

func (s *PhrasePlaybackSchedulerService) decrementLocked(state *phrasePlaybackSession, task *phrasePlaybackTask) {
	if task == nil || task.utterance == nil {
		return
	}
	utterance := task.utterance
	if utterance.unfinished > 0 {
		utterance.unfinished--
		if utterance.unfinished == 0 {
			utteranceID := task.request.UtteranceID
			if state.utterances[utteranceID] == utterance {
				delete(state.utterances, utteranceID)
				state.retired[utteranceID] = utterance
				state.retiredOrder = append(state.retiredOrder, retiredPhrasePlaybackUtterance{
					id: utteranceID, utterance: utterance,
				})
				for len(state.retiredOrder) > maxRetiredPhraseUtterances {
					oldest := state.retiredOrder[0]
					state.retiredOrder = state.retiredOrder[1:]
					if state.retired[oldest.id] == oldest.utterance {
						delete(state.retired, oldest.id)
						delete(state.acceptedIDs, oldest.id)
					}
				}
			}
		}
	}
}

func (s *PhrasePlaybackSchedulerService) signalCapacityLocked(state *phrasePlaybackSession) {
	if state == nil || state.capacityChanged == nil {
		return
	}
	close(state.capacityChanged)
	state.capacityChanged = make(chan struct{})
}

func (s *PhrasePlaybackSchedulerService) reportUsageError(request PhrasePlaybackRequest, fact UsageFact, err error) {
	if s.usageError != nil {
		s.usageError(request, fact, err)
		return
	}
	slog.Default().Error("phrase playback usage publish failed",
		"session_id", request.Turn.SessionID, "turn_id", request.Turn.ID,
		"playback_id", request.PlaybackID, "usage_id", fact.ID, "error", err)
}

var _ PhrasePlaybackScheduler = (*PhrasePlaybackSchedulerService)(nil)
