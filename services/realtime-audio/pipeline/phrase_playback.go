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

const maxPhrasePlaybackBacklog = 5

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

// PhrasePlaybackScheduler accepts translated phrases without blocking the
// ASR/translation workers. Implementations must preserve enqueue order per
// session and may coalesce queued text when an utterance reaches its backlog
// budget.
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
	mu         sync.Mutex
	speech     *SpeechOutput
	audio      AudioChunkSink
	usage      UsageFactSink
	now        func() time.Time
	usageError func(PhrasePlaybackRequest, UsageFact, error)
	sessions   map[string]*phrasePlaybackSession
	closed     map[string]bool
}

type phrasePlaybackSession struct {
	ctx    context.Context
	cancel context.CancelFunc
	wake   chan struct{}

	queue      []*phrasePlaybackTask
	active     *phrasePlaybackTask
	closed     bool
	generation uint64
	utterances map[string]*phrasePlaybackUtterance
}

type phrasePlaybackUtterance struct {
	unfinished int
}

type phrasePlaybackTask struct {
	request    PhrasePlaybackRequest
	generation uint64
	cancel     context.CancelFunc
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
		usageError: deps.UsageError,
		sessions:   make(map[string]*phrasePlaybackSession), closed: make(map[string]bool),
	}
}

func (s *PhrasePlaybackSchedulerService) ResetUtterance(sessionID, utteranceID string) {
	if s == nil || sessionID == "" || utteranceID == "" {
		return
	}
	s.mu.Lock()
	delete(s.closed, sessionID)
	defer s.mu.Unlock()
	state := s.sessionLocked(sessionID)
	state.utterances[utteranceID] = &phrasePlaybackUtterance{}
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
	s.mu.Lock()
	if s.closed[request.Turn.SessionID] {
		s.mu.Unlock()
		return PhrasePlaybackEnqueueResult{Reason: PhrasePlaybackRejectPlaybackClosed}
	}
	state := s.sessionLocked(request.Turn.SessionID)
	if state.closed {
		s.mu.Unlock()
		return PhrasePlaybackEnqueueResult{Reason: PhrasePlaybackRejectPlaybackClosed}
	}
	utterance := state.utterances[request.UtteranceID]
	if utterance == nil {
		if !request.Final {
			s.mu.Unlock()
			return PhrasePlaybackEnqueueResult{Reason: PhrasePlaybackRejectGenerationSuperseded}
		}
		utterance = &phrasePlaybackUtterance{}
		state.utterances[request.UtteranceID] = utterance
	}
	task := &phrasePlaybackTask{request: request, generation: state.generation}
	// Streaming translation frequently yields adjacent micro-chunks. Fold them
	// into the last queued task for this utterance before considering the cap.
	if len(state.queue) > 0 {
		last := state.queue[len(state.queue)-1]
		if last.request.UtteranceID == request.UtteranceID && last.generation == state.generation &&
			(last.request.PhraseSequence >= 1000 && request.PhraseSequence >= 1000 ||
				utterance.unfinished >= maxPhrasePlaybackBacklog) {
			last.request.Text = joinPhrasePlaybackText(last.request.Text, request.Text)
			last.request.Final = last.request.Final || request.Final
			wake := state.wake
			s.mu.Unlock()
			select {
			case wake <- struct{}{}:
			default:
			}
			return PhrasePlaybackEnqueueResult{Accepted: true}
		}
	}
	if utterance.unfinished >= maxPhrasePlaybackBacklog {
		s.mu.Unlock()
		return PhrasePlaybackEnqueueResult{Reason: PhrasePlaybackRejectBacklogLimit}
	}
	state.queue = append(state.queue, task)
	utterance.unfinished++
	wake := state.wake
	s.mu.Unlock()
	select {
	case wake <- struct{}{}:
	default:
	}
	return PhrasePlaybackEnqueueResult{Accepted: true}
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
	// CJK text without inventing a word boundary; Latin chunks need a separator
	// because splitStreamTTS trims the boundary whitespace.
	if unicode.Is(unicode.Han, leftRunes[len(leftRunes)-1]) || unicode.Is(unicode.Han, rightRunes[0]) {
		return left + right
	}
	return left + " " + right
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
	if state == nil {
		s.mu.Unlock()
		return nil
	}
	state.generation++
	active := state.active
	state.queue = nil
	state.utterances = make(map[string]*phrasePlaybackUtterance)
	if active != nil && active.cancel != nil {
		active.cancel()
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
	if state == nil {
		s.mu.Unlock()
		return nil
	}
	state.closed = true
	s.closed[sessionID] = true
	state.generation++
	state.queue = nil
	if state.active != nil && state.active.cancel != nil {
		state.active.cancel()
	}
	state.cancel()
	// A session ID may be reused after a reconnect. Drop the closed worker so
	// the next utterance gets a fresh generation and session lifecycle.
	delete(s.sessions, sessionID)
	s.mu.Unlock()
	return nil
}

func (s *PhrasePlaybackSchedulerService) sessionLocked(sessionID string) *phrasePlaybackSession {
	if state := s.sessions[sessionID]; state != nil {
		return state
	}
	ctx, cancel := context.WithCancel(context.Background())
	state := &phrasePlaybackSession{
		ctx: ctx, cancel: cancel, wake: make(chan struct{}, 1),
		utterances: make(map[string]*phrasePlaybackUtterance),
	}
	s.sessions[sessionID] = state
	go s.runSession(state)
	return state
}

func (s *PhrasePlaybackSchedulerService) runSession(state *phrasePlaybackSession) {
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
				s.decrementLocked(state, task.request.UtteranceID)
				s.mu.Unlock()
				continue
			}
			playCtx, cancel := context.WithCancel(state.ctx)
			task.cancel, state.active = cancel, task
			s.mu.Unlock()
			s.play(task, playCtx)
			cancel()
			s.mu.Lock()
			if state.active == task {
				state.active = nil
			}
			s.decrementLocked(state, task.request.UtteranceID)
			s.mu.Unlock()
		}
	}
}

func (s *PhrasePlaybackSchedulerService) play(task *phrasePlaybackTask, ctx context.Context) {
	result, err := s.speech.Play(ctx, SpeechOutputRequest{
		Turn: task.request.Turn, Language: task.request.Language, Text: task.request.Text,
		PlaybackID: task.request.PlaybackID, SkipRuntime: true,
	})
	if err != nil || s.usage == nil {
		return
	}
	fact, err := buildUsageFactWithIdentity(task.request.Turn, "tts", result.Provider, result.Model,
		result.AudioDuration.Milliseconds(), 0, 0, result.CostAmount, result.Currency,
		fmt.Sprintf("usage_%s_tts", task.request.PlaybackID),
		fmt.Sprintf("usage:%s:tts", task.request.PlaybackID), s.now())
	if err != nil {
		s.reportUsageError(task.request, fact, err)
		return
	}
	for attempt := 0; attempt < 3; attempt++ {
		publishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		err = s.usage.Publish(publishCtx, fact)
		cancel()
		if err == nil {
			return
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
}

func (s *PhrasePlaybackSchedulerService) decrementLocked(state *phrasePlaybackSession, utteranceID string) {
	if utterance := state.utterances[utteranceID]; utterance != nil && utterance.unfinished > 0 {
		utterance.unfinished--
		if utterance.unfinished == 0 {
			delete(state.utterances, utteranceID)
		}
	}
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
