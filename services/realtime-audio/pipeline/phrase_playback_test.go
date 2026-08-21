package pipeline

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/session"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/tts"
)

func TestPhrasePlaybackSchedulerPreservesOrderAndUsesIndependentIDs(t *testing.T) {
	provider := &recordingTTSProvider{}
	audio := &recordingPhraseAudio{}
	usage := &phraseUsageSink{}
	scheduler := NewPhrasePlaybackScheduler(PhrasePlaybackSchedulerDependencies{
		Speech: NewSpeechOutput(SpeechOutputDependencies{
			TTS: provider, Audio: audio, Runtime: phraseRuntimeReporter{}, Provider: "fake",
		}),
		Audio: audio, Usage: usage,
	})
	turn := TurnContext{ID: "turn-1", SessionID: "session-1", AccountID: "account-1", TraceID: "trace-1"}
	scheduler.ResetUtterance(turn.SessionID, turn.ID)
	for sequence := int64(1); sequence <= 3; sequence++ {
		if !scheduler.Enqueue(PhrasePlaybackRequest{
			Turn: turn, UtteranceID: turn.ID, PhraseSequence: sequence,
			Language: "en-US", Text: "phrase", PlaybackID: "phrase_turn-1_" + string(rune('0'+sequence)),
		}) {
			t.Fatalf("Enqueue(%d) rejected", sequence)
		}
	}
	if !audio.waitFor(3, time.Second) {
		t.Fatalf("timed out waiting for audio chunks: %#v", audio.ids())
	}
	got := provider.requests()
	if len(got) != 3 {
		t.Fatalf("TTS requests = %d, want 3", len(got))
	}
	for index, request := range got {
		want := "phrase_turn-1_" + string(rune('1'+index))
		if request.PlaybackID != want {
			t.Fatalf("request %d playback ID = %q, want %q", index, request.PlaybackID, want)
		}
	}
	deadline := time.Now().Add(time.Second)
	for len(usage.facts()) < 3 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	facts := usage.facts()
	if len(facts) != 3 {
		t.Fatalf("usage facts = %d, want 3", len(facts))
	}
	if facts[0].ID == facts[1].ID || facts[1].ID == facts[2].ID || facts[0].IdempotencyKey == facts[1].IdempotencyKey {
		t.Fatalf("phrase usage identities are not unique: %#v", facts)
	}
}

func TestPhrasePlaybackSchedulerRetriesUsageAndReportsPermanentFailure(t *testing.T) {
	provider := &recordingTTSProvider{}
	audio := &recordingPhraseAudio{}
	usage := &phraseUsageSink{failures: 1}
	scheduler := NewPhrasePlaybackScheduler(PhrasePlaybackSchedulerDependencies{
		Speech: NewSpeechOutput(SpeechOutputDependencies{
			TTS: provider, Audio: audio, Runtime: phraseRuntimeReporter{}, Provider: "fake",
		}),
		Audio: audio, Usage: usage,
	})
	turn := TurnContext{ID: "turn-retry", SessionID: "session-retry", AccountID: "account-1", TraceID: "trace-retry"}
	scheduler.ResetUtterance(turn.SessionID, turn.ID)
	if !scheduler.Enqueue(phraseRequest(turn, 1)) {
		t.Fatal("phrase rejected")
	}
	deadline := time.Now().Add(time.Second)
	for usage.attemptsCount() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := usage.attemptsCount(); got != 2 {
		t.Fatalf("usage attempts = %d, want 2", got)
	}
	if len(usage.facts()) != 1 {
		t.Fatalf("published usage facts = %d, want 1", len(usage.facts()))
	}

	permanent := errors.New("outbox unavailable")
	reported := make(chan error, 1)
	usage = &phraseUsageSink{err: permanent}
	scheduler = NewPhrasePlaybackScheduler(PhrasePlaybackSchedulerDependencies{
		Speech: NewSpeechOutput(SpeechOutputDependencies{
			TTS: provider, Audio: audio, Runtime: phraseRuntimeReporter{}, Provider: "fake",
		}),
		Audio: audio, Usage: usage,
		UsageError: func(_ PhrasePlaybackRequest, _ UsageFact, err error) { reported <- err },
	})
	turn = TurnContext{ID: "turn-error", SessionID: "session-error", AccountID: "account-1", TraceID: "trace-error"}
	scheduler.ResetUtterance(turn.SessionID, turn.ID)
	if !scheduler.Enqueue(phraseRequest(turn, 1)) {
		t.Fatal("phrase with failing usage sink rejected")
	}
	select {
	case err := <-reported:
		if !errors.Is(err, permanent) {
			t.Fatalf("reported usage error = %v, want %v", err, permanent)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for usage error callback")
	}
}

func TestPhrasePlaybackSchedulerCleansCompletedUtteranceAndAcceptsFinalResidual(t *testing.T) {
	provider := &recordingTTSProvider{}
	audio := &recordingPhraseAudio{}
	scheduler := NewPhrasePlaybackScheduler(PhrasePlaybackSchedulerDependencies{
		Speech: NewSpeechOutput(SpeechOutputDependencies{
			TTS: provider, Audio: audio, Runtime: phraseRuntimeReporter{}, Provider: "fake",
		}),
		Audio: audio,
	})
	turn := TurnContext{ID: "turn-cleanup", SessionID: "session-cleanup", AccountID: "account-1", TraceID: "trace-cleanup"}
	scheduler.ResetUtterance(turn.SessionID, turn.ID)
	if !scheduler.Enqueue(phraseRequest(turn, 1)) {
		t.Fatal("phrase rejected")
	}
	if !audio.waitFor(1, time.Second) {
		t.Fatal("phrase did not play")
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		scheduler.mu.Lock()
		_, exists := scheduler.sessions[turn.SessionID].utterances[turn.ID]
		scheduler.mu.Unlock()
		if !exists {
			break
		}
		time.Sleep(time.Millisecond)
	}
	scheduler.mu.Lock()
	_, exists := scheduler.sessions[turn.SessionID].utterances[turn.ID]
	scheduler.mu.Unlock()
	if exists {
		t.Fatal("completed utterance remains in session state")
	}
	final := phraseRequest(turn, 2)
	final.Final = true
	final.PlaybackID = "phrase_" + turn.ID + "_final"
	if !scheduler.Enqueue(final) {
		t.Fatal("final residual rejected after phrase state cleanup")
	}
	if !audio.waitFor(2, time.Second) {
		t.Fatal("final residual did not play")
	}
}

func TestPhrasePlaybackSchedulerDegradesAtFiveUnfinishedSegmentsAndResets(t *testing.T) {
	provider := newPhraseBlockingTTSProvider()
	audio := &recordingPhraseAudio{}
	scheduler := NewPhrasePlaybackScheduler(PhrasePlaybackSchedulerDependencies{
		Speech: NewSpeechOutput(SpeechOutputDependencies{
			TTS: provider, Audio: audio, Runtime: phraseRuntimeReporter{}, Provider: "fake",
		}),
		Audio: audio,
	})
	turn := TurnContext{ID: "turn-1", SessionID: "session-1"}
	scheduler.ResetUtterance(turn.SessionID, turn.ID)
	if !scheduler.Enqueue(phraseRequest(turn, 1)) {
		t.Fatal("first phrase rejected")
	}
	<-provider.started
	for sequence := int64(2); sequence <= 5; sequence++ {
		accepted := scheduler.Enqueue(phraseRequest(turn, sequence))
		if sequence == 5 && accepted {
			t.Fatal("fifth phrase was accepted after backlog limit")
		}
	}
	provider.release()
	if !audio.waitFor(1, time.Second) {
		t.Fatal("active phrase did not finish")
	}
	if scheduler.Enqueue(phraseRequest(turn, 6)) {
		t.Fatal("degraded utterance accepted a later phrase")
	}

	newTurn := TurnContext{ID: "turn-2", SessionID: turn.SessionID}
	scheduler.ResetUtterance(newTurn.SessionID, newTurn.ID)
	provider.allowImmediate = true
	if !scheduler.Enqueue(phraseRequest(newTurn, 1)) {
		t.Fatal("next utterance did not recover playback eligibility")
	}
	if !audio.waitFor(2, time.Second) {
		t.Fatal("next utterance did not play")
	}
}

func TestPhrasePlaybackSchedulerInterruptDropsLateQueue(t *testing.T) {
	provider := newPhraseBlockingTTSProvider()
	audio := &recordingPhraseAudio{}
	scheduler := NewPhrasePlaybackScheduler(PhrasePlaybackSchedulerDependencies{
		Speech: NewSpeechOutput(SpeechOutputDependencies{
			TTS: provider, Audio: audio, Runtime: phraseRuntimeReporter{}, Provider: "fake",
		}),
		Audio: audio,
	})
	turn := TurnContext{ID: "turn-1", SessionID: "session-1"}
	scheduler.ResetUtterance(turn.SessionID, turn.ID)
	scheduler.Enqueue(phraseRequest(turn, 1))
	<-provider.started
	scheduler.Enqueue(phraseRequest(turn, 2))
	if err := scheduler.InterruptCurrent(context.Background(), turn.SessionID, "wake_word_detected"); err != nil {
		t.Fatalf("InterruptCurrent() error = %v", err)
	}
	provider.release()
	time.Sleep(20 * time.Millisecond)
	if got := len(provider.requests()); got != 1 {
		t.Fatalf("late queued TTS requests = %d, want 1", got)
	}
}

func TestPhrasePlaybackSchedulerReopensSessionAfterStop(t *testing.T) {
	provider := &recordingTTSProvider{}
	audio := &recordingPhraseAudio{}
	scheduler := NewPhrasePlaybackScheduler(PhrasePlaybackSchedulerDependencies{
		Speech: NewSpeechOutput(SpeechOutputDependencies{
			TTS: provider, Audio: audio, Runtime: phraseRuntimeReporter{}, Provider: "fake",
		}),
		Audio: audio,
	})
	turn := TurnContext{ID: "turn-reconnect", SessionID: "session-reconnect"}
	scheduler.ResetUtterance(turn.SessionID, turn.ID)
	if !scheduler.Enqueue(phraseRequest(turn, 1)) {
		t.Fatal("first phrase rejected")
	}
	if !audio.waitFor(1, time.Second) {
		t.Fatal("first phrase did not play")
	}
	if err := scheduler.Stop(context.Background(), turn.SessionID); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	scheduler.ResetUtterance(turn.SessionID, turn.ID)
	if !scheduler.Enqueue(phraseRequest(turn, 2)) {
		t.Fatal("phrase after session restart rejected")
	}
	if !audio.waitFor(2, time.Second) {
		t.Fatal("phrase after session restart did not play")
	}
}

func TestPhrasePlaybackSchedulerPublishesFirstChunkBeforeTTSFinish(t *testing.T) {
	provider := &firstChunkTTSProvider{releaseFinish: make(chan struct{})}
	audio := &recordingPhraseAudio{}
	scheduler := NewPhrasePlaybackScheduler(PhrasePlaybackSchedulerDependencies{
		Speech: NewSpeechOutput(SpeechOutputDependencies{
			TTS: provider, Audio: audio, Runtime: phraseRuntimeReporter{}, Provider: "fake",
		}),
		Audio: audio,
	})
	turn := TurnContext{ID: "turn-first", SessionID: "session-first"}
	scheduler.ResetUtterance(turn.SessionID, turn.ID)
	if !scheduler.Enqueue(phraseRequest(turn, 1)) {
		t.Fatal("first phrase rejected")
	}
	if !audio.waitFor(1, time.Second) {
		t.Fatal("first audio chunk was not published before TTS finish")
	}
	close(provider.releaseFinish)
}

func phraseRequest(turn TurnContext, sequence int64) PhrasePlaybackRequest {
	return PhrasePlaybackRequest{
		Turn: turn, UtteranceID: turn.ID, PhraseSequence: sequence,
		Language: "en-US", Text: "translated", PlaybackID: "phrase_" + turn.ID + "_" + string(rune('0'+sequence)),
	}
}

type phraseRuntimeReporter struct{}

func (phraseRuntimeReporter) SetProcessingState(context.Context, session.ProcessingStateUpdate) error {
	return nil
}

type recordingPhraseAudio struct {
	mu   sync.Mutex
	idsV []string
}

func (a *recordingPhraseAudio) Publish(_ context.Context, chunk AudioChunk) error {
	a.mu.Lock()
	a.idsV = append(a.idsV, chunk.PlaybackID)
	a.mu.Unlock()
	return nil
}
func (*recordingPhraseAudio) Complete(context.Context, string, string) error       { return nil }
func (*recordingPhraseAudio) Cancel(context.Context, string, string, string) error { return nil }
func (a *recordingPhraseAudio) ids() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.idsV...)
}
func (a *recordingPhraseAudio) waitFor(count int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if len(a.ids()) >= count {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return false
}

type phraseUsageSink struct {
	mu       sync.Mutex
	factsV   []UsageFact
	failures int
	err      error
	attempts int
}

func (s *phraseUsageSink) Publish(_ context.Context, fact UsageFact) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attempts++
	if s.failures > 0 {
		s.failures--
		return errors.New("temporary usage failure")
	}
	if s.err != nil {
		return s.err
	}
	s.factsV = append(s.factsV, fact)
	return nil
}

func (s *phraseUsageSink) facts() []UsageFact {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]UsageFact(nil), s.factsV...)
}

func (s *phraseUsageSink) attemptsCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.attempts
}

type recordingTTSProvider struct {
	mu   sync.Mutex
	seen []tts.Request
}

func (p *recordingTTSProvider) StartStream(ctx context.Context, request tts.Request) (tts.Stream, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.mu.Lock()
	p.seen = append(p.seen, request)
	p.mu.Unlock()
	return &immediateTTSStream{}, nil
}
func (p *recordingTTSProvider) requests() []tts.Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]tts.Request(nil), p.seen...)
}

type immediateTTSStream struct{}

func (*immediateTTSStream) Chunks() <-chan tts.AudioChunk {
	ch := make(chan tts.AudioChunk, 1)
	ch <- tts.AudioChunk{SequenceNo: 1, Data: []byte{1}}
	close(ch)
	return ch
}
func (*immediateTTSStream) Finish(context.Context) (tts.Result, error) {
	return tts.Result{Provider: "fake-provider", Model: "fake-model"}, nil
}
func (*immediateTTSStream) Close() error { return nil }

type phraseBlockingTTSProvider struct {
	mu             sync.Mutex
	seen           []tts.Request
	started        chan struct{}
	releaseCh      chan struct{}
	allowImmediate bool
}

func newPhraseBlockingTTSProvider() *phraseBlockingTTSProvider {
	return &phraseBlockingTTSProvider{started: make(chan struct{}, 1), releaseCh: make(chan struct{})}
}
func (p *phraseBlockingTTSProvider) StartStream(ctx context.Context, request tts.Request) (tts.Stream, error) {
	p.mu.Lock()
	p.seen = append(p.seen, request)
	immediate := p.allowImmediate
	p.mu.Unlock()
	if !immediate {
		select {
		case p.started <- struct{}{}:
		default:
		}
	}
	return &phraseBlockingTTSStream{ctx: ctx, release: p.releaseCh, immediate: immediate}, nil
}
func (p *phraseBlockingTTSProvider) requests() []tts.Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]tts.Request(nil), p.seen...)
}
func (p *phraseBlockingTTSProvider) release() {
	select {
	case <-p.releaseCh:
	default:
		close(p.releaseCh)
	}
}

type phraseBlockingTTSStream struct {
	ctx       context.Context
	release   <-chan struct{}
	immediate bool
}

func (s *phraseBlockingTTSStream) Chunks() <-chan tts.AudioChunk {
	ch := make(chan tts.AudioChunk, 1)
	go func() {
		defer close(ch)
		if !s.immediate {
			select {
			case <-s.release:
			case <-s.ctx.Done():
				return
			}
		}
		ch <- tts.AudioChunk{SequenceNo: 1, Data: []byte{1}}
	}()
	return ch
}
func (s *phraseBlockingTTSStream) Finish(ctx context.Context) (tts.Result, error) {
	return tts.Result{}, ctx.Err()
}
func (*phraseBlockingTTSStream) Close() error { return nil }

type firstChunkTTSProvider struct {
	releaseFinish chan struct{}
}

func (p *firstChunkTTSProvider) StartStream(context.Context, tts.Request) (tts.Stream, error) {
	return &firstChunkTTSStream{releaseFinish: p.releaseFinish}, nil
}

type firstChunkTTSStream struct {
	releaseFinish <-chan struct{}
}

func (*firstChunkTTSStream) Chunks() <-chan tts.AudioChunk {
	ch := make(chan tts.AudioChunk, 1)
	ch <- tts.AudioChunk{SequenceNo: 1, Data: []byte{1}}
	close(ch)
	return ch
}

func (s *firstChunkTTSStream) Finish(ctx context.Context) (tts.Result, error) {
	select {
	case <-s.releaseFinish:
		return tts.Result{}, nil
	case <-ctx.Done():
		return tts.Result{}, ctx.Err()
	}
}

func (*firstChunkTTSStream) Close() error { return nil }
