package segment

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/audio"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/command"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/pipeline"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/vad"
)

var (
	errProcessor = errors.New("processor failed")
	errClose     = errors.New("source close failed")
)

func TestServiceProcessesOnlyFinalizedUtterances(t *testing.T) {
	source := &fakeSource{frames: []audio.Frame{
		testFrame(t, 1, time.Unix(10, 0)),
		testFrame(t, 2, time.Unix(10, 100_000_000)),
		testFrame(t, 0, time.Unix(10, 400_000_000)),
	}}
	processor := &fakeProcessor{}
	service := newTestService(t, source, processor)

	err := service.Run(context.Background(), Request{
		SessionID: "session-1", AccountID: "account-1", TraceID: "trace-1", SourceLanguage: "zh-CN",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(processor.requests) != 1 {
		t.Fatalf("processor requests = %d, want 1", len(processor.requests))
	}
	request := processor.requests[0]
	if request.SessionID != "session-1" || request.AccountID != "account-1" || request.TraceID != "trace-1" || request.SourceLanguage != "zh-CN" {
		t.Fatalf("processor request metadata = %#v", request)
	}
	if len(request.AudioChunks) != 3 {
		t.Fatalf("audio chunks = %d, want speech and trailing quiet frames", len(request.AudioChunks))
	}
	if !request.StartedAt.Equal(time.Unix(10, 0)) {
		t.Fatalf("StartedAt = %s, want first speech timestamp", request.StartedAt)
	}
	if source.closeCalls != 1 {
		t.Fatalf("source close calls = %d, want 1", source.closeCalls)
	}
}

func TestServiceKeepsReadingWhileTurnProcessingIsBusy(t *testing.T) {
	base := time.Unix(100, 0)
	source := &trackingSource{exhausted: make(chan struct{}), frames: []audio.Frame{
		testFrame(t, 1, base),
		testFrame(t, 0, base.Add(300*time.Millisecond)),
		testFrame(t, 1, base.Add(400*time.Millisecond)),
		testFrame(t, 0, base.Add(700*time.Millisecond)),
	}}
	processor := &blockingProcessor{started: make(chan struct{}), release: make(chan struct{})}
	service := newTestService(t, source, processor)

	runDone := make(chan error, 1)
	go func() {
		runDone <- service.Run(context.Background(), Request{SessionID: "session-1"})
	}()

	select {
	case <-processor.started:
	case <-time.After(time.Second):
		t.Fatal("first Turn did not start")
	}
	select {
	case <-source.exhausted:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("audio source stopped being read while first Turn was processing")
	}
	close(processor.release)

	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run() did not finish after releasing the first Turn")
	}
	if got := processor.calls(); got != 2 {
		t.Fatalf("processed Turns = %d, want 2", got)
	}
}

func TestServiceFlushesActiveUtteranceAtEOF(t *testing.T) {
	source := &fakeSource{frames: []audio.Frame{testFrame(t, 1, time.Unix(20, 0))}}
	processor := &fakeProcessor{}
	service := newTestService(t, source, processor)

	if err := service.Run(context.Background(), Request{SessionID: "session-1"}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(processor.requests) != 1 || len(processor.requests[0].AudioChunks) != 1 {
		t.Fatalf("EOF flush requests = %#v, want one one-frame Turn", processor.requests)
	}
}

func TestServiceIgnoresSilenceOnlyInput(t *testing.T) {
	source := &fakeSource{frames: []audio.Frame{
		testFrame(t, 0, time.Unix(30, 0)),
		testFrame(t, 0, time.Unix(30, 100_000_000)),
	}}
	processor := &fakeProcessor{}
	service := newTestService(t, source, processor)

	if err := service.Run(context.Background(), Request{SessionID: "session-1"}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(processor.requests) != 0 {
		t.Fatalf("processor requests = %d, want 0", len(processor.requests))
	}
}

func TestServiceQuarantinesAudioAfterWakeWord(t *testing.T) {
	base := time.Unix(31, 0)
	wake := &fakeWakeWords{signal: realtimev1.WakeWordDetectedSignal{
		Type: realtimev1.WakeWordDetectedType, EventVersion: realtimev1.WakeWordDetectedEventVersion,
		SignalID: "wake-1", DetectedAt: base.Add(24 * time.Hour),
	}, ready: make(chan struct{})}
	source := &wakeAwareSource{
		ready: wake.ready,
		frames: []audio.Frame{
			testFrame(t, 9, base), // command audio: must never reach ordinary VAD
			testFrame(t, 1, base.Add(100*time.Millisecond)),
			testFrame(t, 0, base.Add(400*time.Millisecond)),
		},
	}
	gate := &recordingGate{}
	processor := &fakeProcessor{}
	service := newTestServiceWithDeps(t, source, processor, gate, wake, func() time.Time { return base })

	if err := service.Run(context.Background(), Request{SessionID: "session-1", SourceLanguage: "zh-CN"}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if gate.openRequest.CommandID != "wake-1" {
		t.Fatalf("gate open request = %#v, want signal ID", gate.openRequest)
	}
	if !gate.openRequest.OpenedAt.Equal(base) {
		t.Fatalf("gate opened at = %s, want server receive time %s", gate.openRequest.OpenedAt, base)
	}
	if gate.consumed != 1 {
		t.Fatalf("command frames consumed = %d, want current command frame", gate.consumed)
	}
	if len(processor.requests) != 1 || len(processor.requests[0].AudioChunks) != 2 {
		t.Fatalf("ordinary processor requests = %#v, want only post-command turn", processor.requests)
	}
}

func TestOpenCommandWindowReplaysCompleteActiveUtterance(t *testing.T) {
	base := time.Unix(32, 0).UTC()
	segmenter, err := vad.NewSegmenter(energyClassifier{}, vad.Options{
		SilenceAfter: 800 * time.Millisecond,
		MaxDuration:  5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewSegmenter() error = %v", err)
	}
	for _, offset := range []time.Duration{0, time.Second, 2500 * time.Millisecond} {
		if _, err := segmenter.Push(t.Context(), testFrame(t, 1, base.Add(offset))); err != nil {
			t.Fatalf("Push(%s) error = %v", offset, err)
		}
	}
	gate := &recordingGate{}
	service, err := NewService(Dependencies{
		Source: &fakeSource{}, Segmenter: segmenter, Processor: &fakeProcessor{}, Command: gate,
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	wake := receivedWakeWord{
		signal: realtimev1.WakeWordDetectedSignal{
			Type: realtimev1.WakeWordDetectedType, EventVersion: 1,
			SignalID: "wake-1", DetectedAt: base.Add(2500 * time.Millisecond),
		},
		receivedAt: base.Add(2600 * time.Millisecond),
	}

	if !service.openCommandWindow(t.Context(), Request{SessionID: "session-1"}, wake) {
		t.Fatal("openCommandWindow() = false, want active command window")
	}
	if len(gate.replayed) != 3 || !gate.replayed[0].CapturedAt.Equal(base) ||
		!gate.replayed[2].CapturedAt.Equal(base.Add(2500*time.Millisecond)) {
		t.Fatalf("replayed frames = %#v, want complete 2.5 second utterance", gate.replayed)
	}
	if events, err := segmenter.Flush(t.Context(), base.Add(3*time.Second)); err != nil || len(events) != 0 {
		t.Fatalf("Flush(after claim) = %#v, %v; want no ordinary Final", events, err)
	}
}

func TestOpenCommandWindowWithoutActiveUtteranceWaitsForLiveCommand(t *testing.T) {
	base := time.Unix(32, 0).UTC()
	gate := &recordingGate{}
	service := newTestServiceWithDeps(t, &fakeSource{}, &fakeProcessor{}, gate, nil, func() time.Time { return base })

	if !service.openCommandWindow(t.Context(), Request{SessionID: "session-1"}, receivedWakeWord{
		signal: realtimev1.WakeWordDetectedSignal{
			Type: realtimev1.WakeWordDetectedType, EventVersion: 1,
			SignalID: "wake-1", DetectedAt: base,
		},
		receivedAt: base,
	}) {
		t.Fatal("openCommandWindow() = false, want armed command window")
	}
	if len(gate.replayed) != 0 || !gate.active {
		t.Fatalf("gate = %#v, want armed gate without replay", gate)
	}
}

func TestOpenCommandWindowLeavesStateUntouchedForDuplicateSignal(t *testing.T) {
	base := time.Unix(33, 0).UTC()
	gate := &recordingGate{openErr: command.ErrDuplicateOpen}
	service := newTestServiceWithDeps(t, &fakeSource{}, &fakeProcessor{}, gate, nil, func() time.Time { return base })
	if service.openCommandWindow(t.Context(), Request{SessionID: "session-1"}, receivedWakeWord{
		signal: realtimev1.WakeWordDetectedSignal{
			Type: realtimev1.WakeWordDetectedType, EventVersion: 1,
			SignalID: "wake-1", DetectedAt: base,
		},
		receivedAt: base,
	}) {
		t.Fatal("duplicate signal reopened command window")
	}
	if gate.openCalls != 1 || gate.active {
		t.Fatalf("duplicate signal changed gate state: %#v", gate)
	}
	if events, err := service.segmenter.Push(t.Context(), testFrame(t, 1, base.Add(time.Second))); err != nil || len(events) != 2 {
		t.Fatalf("ordinary segmenter after duplicate = %#v, %v; want untouched usable state", events, err)
	}
}

func TestServiceStopsOnSourceErrorAndClosesSource(t *testing.T) {
	sourceErr := errors.New("track read failed")
	source := &fakeSource{readErr: sourceErr}
	service := newTestService(t, source, &fakeProcessor{})

	err := service.Run(context.Background(), Request{SessionID: "session-1"})
	if !errors.Is(err, sourceErr) {
		t.Fatalf("Run() error = %v, want source error", err)
	}
	if source.closeCalls != 1 {
		t.Fatalf("source close calls = %d, want 1", source.closeCalls)
	}
}

func TestServiceReturnsSourceCloseError(t *testing.T) {
	source := &fakeSource{closeErr: errClose}
	service := newTestService(t, source, &fakeProcessor{})

	err := service.Run(context.Background(), Request{SessionID: "session-1"})
	if !errors.Is(err, errClose) {
		t.Fatalf("Run() error = %v, want close error", err)
	}
}

func TestServicePropagatesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	source := &fakeSource{}
	service := newTestService(t, source, &fakeProcessor{})

	err := service.Run(ctx, Request{SessionID: "session-1"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context canceled", err)
	}
	if source.closeCalls != 1 {
		t.Fatalf("source close calls = %d, want 1", source.closeCalls)
	}
}

func TestServicePropagatesProcessorError(t *testing.T) {
	source := &fakeSource{frames: []audio.Frame{
		testFrame(t, 1, time.Unix(40, 0)),
		testFrame(t, 0, time.Unix(40, 300_000_000)),
	}}
	service := newTestService(t, source, &fakeProcessor{err: errProcessor})

	err := service.Run(context.Background(), Request{SessionID: "session-1"})
	if !errors.Is(err, errProcessor) {
		t.Fatalf("Run() error = %v, want processor error", err)
	}
	if source.closeCalls != 1 {
		t.Fatalf("source close calls = %d, want 1", source.closeCalls)
	}
}

func TestServiceContinuesAfterFinalTurnPostCommitFailure(t *testing.T) {
	base := time.Unix(41, 0)
	source := &fakeSource{frames: []audio.Frame{
		testFrame(t, 1, base),
		testFrame(t, 0, base.Add(300*time.Millisecond)),
		testFrame(t, 1, base.Add(400*time.Millisecond)),
		testFrame(t, 0, base.Add(700*time.Millisecond)),
	}}
	postCommitErr := errors.Join(pipeline.ErrFinalTurnAccepted, errors.New("play Japanese TTS"))
	processor := &sequenceProcessor{errors: []error{postCommitErr, nil}}
	service := newTestService(t, source, processor)
	var logs bytes.Buffer
	service.latency = slog.New(slog.NewJSONHandler(&logs, nil))

	err := service.Run(context.Background(), Request{SessionID: "session-1", TraceID: "trace-1"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if processor.calls != 2 {
		t.Fatalf("processor calls = %d, want second Turn after post-commit failure", processor.calls)
	}
	for _, want := range []string{
		"realtime turn post-commit processing failed", "session-1", "trace-1", "play Japanese TTS",
	} {
		if !strings.Contains(logs.String(), want) {
			t.Fatalf("post-commit log = %s, missing %q", logs.String(), want)
		}
	}
}

func TestNewServiceRejectsMissingDependency(t *testing.T) {
	if _, err := NewService(Dependencies{}); !errors.Is(err, ErrDependencyRequired) {
		t.Fatalf("NewService() error = %v, want %v", err, ErrDependencyRequired)
	}
}

func TestServiceRejectsMissingSessionID(t *testing.T) {
	service := newTestService(t, &fakeSource{}, &fakeProcessor{})
	if err := service.Run(context.Background(), Request{}); !errors.Is(err, ErrSessionIDRequired) {
		t.Fatalf("Run() error = %v, want %v", err, ErrSessionIDRequired)
	}
}

func newTestService(t *testing.T, source FrameSource, processor TurnProcessor) *Service {
	t.Helper()
	segmenter, err := vad.NewSegmenter(energyClassifier{}, vad.Options{
		SilenceAfter: 200 * time.Millisecond,
		MaxDuration:  time.Second,
	})
	if err != nil {
		t.Fatalf("NewSegmenter() error = %v", err)
	}
	service, err := NewService(Dependencies{Source: source, Segmenter: segmenter, Processor: processor})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return service
}

func newTestServiceWithDeps(
	t *testing.T,
	source FrameSource,
	processor TurnProcessor,
	gate CommandGate,
	wake WakeWordSource,
	now func() time.Time,
) *Service {
	t.Helper()
	segmenter, err := vad.NewSegmenter(energyClassifier{}, vad.Options{
		SilenceAfter: 200 * time.Millisecond,
		MaxDuration:  time.Second,
	})
	if err != nil {
		t.Fatalf("NewSegmenter() error = %v", err)
	}
	service, err := NewService(Dependencies{
		Source: source, Segmenter: segmenter, Processor: processor,
		Command: gate, WakeWords: wake, Now: now,
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return service
}

type energyClassifier struct{}

func (energyClassifier) Speech(frame audio.Frame) bool {
	return len(frame.PCM) > 0 && frame.PCM[0] != 0
}

type fakeWakeWords struct {
	signal realtimev1.WakeWordDetectedSignal
	ready  chan struct{}
}

func (s *fakeWakeWords) Receive(ctx context.Context) (realtimev1.WakeWordDetectedSignal, error) {
	if s.ready == nil {
		s.ready = make(chan struct{})
	}
	select {
	case <-s.ready:
		return s.signal, io.EOF
	default:
		close(s.ready)
		return s.signal, nil
	case <-ctx.Done():
		return realtimev1.WakeWordDetectedSignal{}, ctx.Err()
	}
}

type wakeAwareSource struct {
	ready  <-chan struct{}
	frames []audio.Frame
}

func (s *wakeAwareSource) ReadFrame(ctx context.Context) (audio.Frame, error) {
	select {
	case <-s.ready:
	case <-ctx.Done():
		return audio.Frame{}, ctx.Err()
	}
	if len(s.frames) == 0 {
		return audio.Frame{}, io.EOF
	}
	frame := s.frames[0].Clone()
	s.frames = s.frames[1:]
	return frame, nil
}

func (s *wakeAwareSource) Close() error { return nil }

type recordingGate struct {
	openRequest command.OpenRequest
	openErr     error
	openCalls   int
	active      bool
	consumed    int
	replayed    []audio.Frame
}

func (g *recordingGate) Replay(_ context.Context, frames []audio.Frame) command.Result {
	if !g.active {
		return command.Result{State: command.StateDormant}
	}
	g.replayed = make([]audio.Frame, len(frames))
	for index, frame := range frames {
		g.replayed[index] = frame.Clone()
	}
	if len(frames) == 0 {
		return command.Result{Consumed: true, State: command.StateArmed}
	}
	g.consumed += len(frames)
	return command.Result{Consumed: true, State: command.StateCapturing}
}

func (g *recordingGate) Open(request command.OpenRequest) error {
	g.openCalls++
	if g.openErr != nil {
		return g.openErr
	}
	g.openRequest = request
	g.active = true
	return nil
}

func (g *recordingGate) Consume(context.Context, audio.Frame) command.Result {
	if !g.active {
		return command.Result{State: command.StateDormant}
	}
	g.active = false
	g.consumed++
	return command.Result{Consumed: true, State: command.StateDormant}
}

func (g *recordingGate) Cancel() { g.active = false }

type fakeSource struct {
	frames     []audio.Frame
	readErr    error
	closeErr   error
	closeCalls int
}

func (s *fakeSource) ReadFrame(ctx context.Context) (audio.Frame, error) {
	if err := ctx.Err(); err != nil {
		return audio.Frame{}, err
	}
	if s.readErr != nil {
		err := s.readErr
		s.readErr = nil
		return audio.Frame{}, err
	}
	if len(s.frames) == 0 {
		return audio.Frame{}, io.EOF
	}
	frame := s.frames[0].Clone()
	s.frames = s.frames[1:]
	return frame, nil
}

func (s *fakeSource) Close() error {
	s.closeCalls++
	return s.closeErr
}

type fakeProcessor struct {
	requests []pipeline.TurnProcessRequest
	err      error
}

type blockingProcessor struct {
	mu      sync.Mutex
	count   int
	started chan struct{}
	release chan struct{}
}

func (p *blockingProcessor) ProcessAudio(_ context.Context, _ pipeline.TurnProcessRequest) (pipeline.TurnContext, error) {
	p.mu.Lock()
	p.count++
	count := p.count
	p.mu.Unlock()
	if count == 1 {
		close(p.started)
		<-p.release
	}
	return pipeline.TurnContext{ID: "turn", SessionID: "session-1"}, nil
}

func (p *blockingProcessor) calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.count
}

type trackingSource struct {
	mu        sync.Mutex
	frames    []audio.Frame
	exhausted chan struct{}
	closed    bool
}

func (s *trackingSource) ReadFrame(ctx context.Context) (audio.Frame, error) {
	if err := ctx.Err(); err != nil {
		return audio.Frame{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.frames) == 0 {
		select {
		case <-s.exhausted:
		default:
			close(s.exhausted)
		}
		return audio.Frame{}, io.EOF
	}
	frame := s.frames[0].Clone()
	s.frames = s.frames[1:]
	return frame, nil
}

func (s *trackingSource) Close() error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	return nil
}

func (p *fakeProcessor) ProcessAudio(_ context.Context, request pipeline.TurnProcessRequest) (pipeline.TurnContext, error) {
	p.requests = append(p.requests, request)
	if p.err != nil {
		return pipeline.TurnContext{}, p.err
	}
	return pipeline.TurnContext{ID: "turn-1", SessionID: request.SessionID}, nil
}

type sequenceProcessor struct {
	calls  int
	errors []error
}

func (p *sequenceProcessor) ProcessAudio(_ context.Context, request pipeline.TurnProcessRequest) (pipeline.TurnContext, error) {
	p.calls++
	var err error
	if len(p.errors) > 0 {
		err = p.errors[0]
		p.errors = p.errors[1:]
	}
	return pipeline.TurnContext{ID: "turn", SessionID: request.SessionID}, err
}

func testFrame(t *testing.T, value byte, capturedAt time.Time) audio.Frame {
	t.Helper()
	frame, err := audio.NewFrame([]byte{value, 0}, audio.SupportedSampleRate, capturedAt)
	if err != nil {
		t.Fatalf("audio.NewFrame() error = %v", err)
	}
	return frame
}
