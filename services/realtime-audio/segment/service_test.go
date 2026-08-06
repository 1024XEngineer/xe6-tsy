package segment

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/audio"
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

type energyClassifier struct{}

func (energyClassifier) Speech(frame audio.Frame) bool {
	return len(frame.PCM) > 0 && frame.PCM[0] != 0
}

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

func testFrame(t *testing.T, value byte, capturedAt time.Time) audio.Frame {
	t.Helper()
	frame, err := audio.NewFrame([]byte{value, 0}, audio.SupportedSampleRate, capturedAt)
	if err != nil {
		t.Fatalf("audio.NewFrame() error = %v", err)
	}
	return frame
}
