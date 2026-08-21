package pipeline

import (
	"context"
	"errors"
	"sync"
	"time"
)

const maxPhrasePlaybackBacklog = 5

var ErrPhrasePlaybackClosed = errors.New("phrase playback scheduler is closed")

// PhrasePlaybackRequest is the immutable translation accepted by the playback
// scheduler. Playback IDs are scoped to a session and phrase sequence.
type PhrasePlaybackRequest struct {
	Turn           TurnContext
	UtteranceID    string
	PhraseSequence int64
	Language       string
	Text           string
	PlaybackID     string
}

// PhrasePlaybackScheduler accepts translated phrases without blocking the
// ASR/translation workers. Implementations must preserve enqueue order per
// session and may reject a phrase when the current utterance is degraded.
type PhrasePlaybackScheduler interface {
	ResetUtterance(sessionID, utteranceID string)
	Enqueue(PhrasePlaybackRequest) bool
	InterruptCurrent(context.Context, string, string) error
	Stop(context.Context, string) error
}

// PhrasePlaybackSchedulerDependencies wires the scheduler to the existing
// speech output and media lifecycle. No alternate media track is created.
type PhrasePlaybackSchedulerDependencies struct {
	Speech *SpeechOutput
	Audio  AudioChunkSink
	Usage  UsageFactSink
	Now    func() time.Time
}

type PhrasePlaybackSchedulerService struct {
	mu       sync.Mutex
	speech   *SpeechOutput
	audio    AudioChunkSink
	usage    UsageFactSink
	now      func() time.Time
	sessions map[string]*phrasePlaybackSession
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
	degraded   bool
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
		sessions: make(map[string]*phrasePlaybackSession),
	}
}

func (s *PhrasePlaybackSchedulerService) ResetUtterance(sessionID, utteranceID string) {
	if s == nil || sessionID == "" || utteranceID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.sessionLocked(sessionID)
	state.utterances[utteranceID] = &phrasePlaybackUtterance{}
}

// Enqueue returns false when the phrase is invalid, the scheduler is not
// configured, or the utterance crossed the five unfinished-segment limit.
func (s *PhrasePlaybackSchedulerService) Enqueue(request PhrasePlaybackRequest) bool {
	if s == nil || s.speech == nil || s.audio == nil || request.Turn.SessionID == "" ||
		request.Turn.ID == "" || request.UtteranceID == "" || request.PhraseSequence < 1 ||
		request.PlaybackID == "" || request.Language == "" || request.Text == "" {
		return false
	}
	s.mu.Lock()
	state := s.sessionLocked(request.Turn.SessionID)
	if state.closed {
		s.mu.Unlock()
		return false
	}
	utterance := state.utterances[request.UtteranceID]
	if utterance == nil {
		utterance = &phrasePlaybackUtterance{}
		state.utterances[request.UtteranceID] = utterance
	}
	if utterance.degraded {
		s.mu.Unlock()
		return false
	}
	task := &phrasePlaybackTask{request: request, generation: state.generation}
	state.queue = append(state.queue, task)
	utterance.unfinished++
	if utterance.unfinished >= maxPhrasePlaybackBacklog {
		// Preserve the active TTS, but discard queued items for this utterance.
		// Subtitle delivery continues and this qualification lasts until the next
		// VAD open calls ResetUtterance.
		utterance.degraded = true
		kept := state.queue[:0]
		for _, queued := range state.queue {
			if queued.request.UtteranceID == request.UtteranceID {
				utterance.unfinished--
				continue
			}
			kept = append(kept, queued)
		}
		state.queue = kept
	}
	accepted := !utterance.degraded
	wake := state.wake
	s.mu.Unlock()
	select {
	case wake <- struct{}{}:
	default:
	}
	return accepted
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
	state.generation++
	state.queue = nil
	if state.active != nil && state.active.cancel != nil {
		state.active.cancel()
	}
	state.cancel()
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
	fact, err := BuildUsageFact(task.request.Turn, "tts", result.Provider, result.Model,
		result.AudioDuration.Milliseconds(), 0, 0, result.CostAmount, result.Currency, s.now())
	if err == nil {
		_ = s.usage.Publish(ctx, fact)
	}
}

func (s *PhrasePlaybackSchedulerService) decrementLocked(state *phrasePlaybackSession, utteranceID string) {
	if utterance := state.utterances[utteranceID]; utterance != nil && utterance.unfinished > 0 {
		utterance.unfinished--
	}
}

var _ PhrasePlaybackScheduler = (*PhrasePlaybackSchedulerService)(nil)
