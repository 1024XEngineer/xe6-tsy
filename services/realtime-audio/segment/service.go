package segment

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/audio"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/command"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/pipeline"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/vad"
)

var (
	ErrDependencyRequired = errors.New("audio segment dependency is required")
	ErrSessionIDRequired  = pipeline.ErrSessionIDRequired
)

// FrameSource emits normalized mono PCM frames for one session-bound audio track.
// Close implementations must be safe to retry after returning an error.
type FrameSource interface {
	ReadFrame(ctx context.Context) (audio.Frame, error)
	Close() error
}

// TurnProcessor accepts exactly one finalized utterance and opens the member-3 Turn boundary.
type TurnProcessor interface {
	ProcessAudio(ctx context.Context, request pipeline.TurnProcessRequest) (pipeline.TurnContext, error)
}

// WakeWordSource exposes validated local wake-word detections. The source is
// optional: transports without a local detector continue through the ordinary
// VAD path unchanged.
type WakeWordSource interface {
	Receive(context.Context) (realtimev1.WakeWordDetectedSignal, error)
}

// CommandGate is the narrow command-channel contract consumed before ordinary
// VAD. Implementations quarantine every frame while armed and return to a
// dormant state after success or any bounded failure.
type CommandGate interface {
	Open(command.OpenRequest) error
	Replay(context.Context, []audio.Frame) command.Result
	Consume(context.Context, audio.Frame) command.Result
	Cancel()
}

// Request carries immutable session metadata used for every utterance read from a source.
type Request struct {
	SessionID      string
	AccountID      string
	TraceID        string
	SourceLanguage string
}

// Dependencies wires the audio ingress loop without binding it to a concrete WebRTC adapter.
type Dependencies struct {
	Source    FrameSource
	Segmenter *vad.Segmenter
	Processor TurnProcessor
	Command   CommandGate
	WakeWords WakeWordSource
	Latency   *slog.Logger
	Now       func() time.Time
}

// Service reads normalized frames, applies VAD, and sends only finalized utterances downstream.
type Service struct {
	source    FrameSource
	segmenter *vad.Segmenter
	processor TurnProcessor
	command   CommandGate
	wakeWords WakeWordSource
	latency   *slog.Logger
	now       func() time.Time
}

// finalizedEventQueueCapacity bounds audio-turn backlog while provider calls run.
const finalizedEventQueueCapacity = 8

func NewService(deps Dependencies) (*Service, error) {
	if deps.Source == nil || deps.Segmenter == nil || deps.Processor == nil {
		return nil, ErrDependencyRequired
	}
	now := deps.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Service{
		source: deps.Source, segmenter: deps.Segmenter, processor: deps.Processor,
		command: deps.Command, wakeWords: deps.WakeWords, latency: deps.Latency, now: now,
	}, nil
}

// Run owns the source until EOF, context cancellation, or a processing error closes the loop.
func (s *Service) Run(ctx context.Context, request Request) (returnErr error) {
	if s == nil || s.source == nil || s.segmenter == nil || s.processor == nil {
		return ErrDependencyRequired
	}
	defer func() {
		if err := s.source.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close audio frame source: %w", err))
		}
	}()
	if err := ctx.Err(); err != nil {
		return err
	}
	if request.SessionID == "" {
		return ErrSessionIDRequired
	}

	runCtx, cancel := context.WithCancel(ctx)
	finalizedEvents := make(chan vad.Event, finalizedEventQueueCapacity)
	processingErrors := make(chan error, 1)
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		for event := range finalizedEvents {
			if err := s.handleEvents(runCtx, request, []vad.Event{event}); err != nil {
				if errors.Is(err, pipeline.ErrUnsupportedSourceLanguage) {
					// ASR can misclassify short speech, playback echo, or background noise as
					// a third language. The pipeline rejects it before translation and durable
					// effects, so only this Turn is dropped; the shared media runtime stays live.
					s.logUnsupportedSourceLanguage(request, err)
					continue
				}
				if errors.Is(err, pipeline.ErrFinalTurnAccepted) {
					// FinalTurn is already durable at this point. TTS, playback, usage, or
					// runtime-reporting failures belong to this Turn and must not tear down
					// the shared WebRTC session or cause the Turn to be translated again.
					s.logPostCommitFailure(request, err)
					continue
				}
				processingErrors <- err
				cancel()
				return
			}
		}
	}()
	enqueueFinalized := func(event vad.Event) error {
		select {
		case finalizedEvents <- event:
			return nil
		case err := <-processingErrors:
			return err
		case <-runCtx.Done():
			select {
			case err := <-processingErrors:
				return err
			default:
				return runCtx.Err()
			}
		}
	}
	wakeSignals := make(chan receivedWakeWord, 1)
	wakeDone := make(chan struct{})
	if s.command != nil && s.wakeWords != nil {
		go s.receiveWakeWords(runCtx, wakeSignals, wakeDone)
	} else {
		close(wakeDone)
	}
	defer func() {
		cancel()
		if s.command != nil {
			s.command.Cancel()
		}
		<-wakeDone
	}()

	openPendingWake := func() {
		for {
			select {
			case wake := <-wakeSignals:
				s.openCommandWindow(runCtx, request, wake)
			default:
				return
			}
		}
	}
	var lastSeen time.Time
	var loopErr error
	for {
		openPendingWake()
		frame, err := s.source.ReadFrame(runCtx)
		if err != nil {
			if errors.Is(err, io.EOF) {
				events, flushErr := s.flush(runCtx, lastSeen)
				if flushErr != nil {
					loopErr = flushErr
				} else {
					for _, event := range events {
						s.logVADCheckpoint(request, event)
						if event.Type == vad.EventFinal {
							if err := enqueueFinalized(event); err != nil {
								loopErr = err
								break
							}
						}
					}
				}
				break
			}
			loopErr = fmt.Errorf("read audio frame: %w", err)
			break
		}
		// A wake signal may arrive while ReadFrame is blocked. Drain it before
		// routing this frame so command audio cannot reach ordinary VAD.
		openPendingWake()
		if s.command != nil {
			result := s.command.Consume(runCtx, frame)
			if result.Consumed {
				continue
			}
		}
		lastSeen = frame.CapturedAt
		events, err := s.segmenter.Push(runCtx, frame)
		if err != nil {
			loopErr = fmt.Errorf("segment audio frame: %w", err)
			break
		}
		for _, event := range events {
			s.logVADCheckpoint(request, event)
			if event.Type != vad.EventFinal {
				continue
			}
			if err := enqueueFinalized(event); err != nil {
				loopErr = err
				break
			}
		}
		if loopErr != nil {
			break
		}
	}
	if loopErr != nil {
		cancel()
	}
	close(finalizedEvents)
	<-workerDone
	select {
	case err := <-processingErrors:
		if loopErr != nil && !errors.Is(loopErr, context.Canceled) {
			return errors.Join(loopErr, err)
		}
		return err
	default:
	}
	return loopErr
}

func (s *Service) receiveWakeWords(
	ctx context.Context,
	signals chan<- receivedWakeWord,
	done chan<- struct{},
) {
	defer close(done)
	for {
		signal, err := s.wakeWords.Receive(ctx)
		if err != nil {
			// A detector ending is an optional capability failure. The media
			// loop remains healthy and continues ordinary input processing.
			return
		}
		select {
		case signals <- receivedWakeWord{signal: signal, receivedAt: s.now()}:
		case <-ctx.Done():
			return
		}
	}
}

type receivedWakeWord struct {
	signal     realtimev1.WakeWordDetectedSignal
	receivedAt time.Time
}

func (s *Service) openCommandWindow(ctx context.Context, request Request, wake receivedWakeWord) bool {
	if s == nil || s.command == nil || wake.signal.Validate() != nil || wake.receivedAt.IsZero() {
		return false
	}
	if err := s.command.Open(command.OpenRequest{
		SessionID: request.SessionID, CommandID: wake.signal.SignalID,
		SourceLanguage: request.SourceLanguage, OpenedAt: wake.receivedAt,
	}); err != nil {
		// A device may retry the same signal_id after uncertain delivery. The Gate recognizes that
		// retry and leaves both the current command attempt and ordinary VAD state untouched.
		return false
	}
	// The ordinary Segmenter already owns the complete in-flight utterance, including its VAD
	// prefix and internal pauses. Transfer that utterance only after Open succeeds, so a duplicate
	// wake cannot discard ordinary audio. Command Replay receives the same sentence boundary rather
	// than an arbitrary wall-clock slice; when no sentence is active, Reset still prevents stale
	// prefix/timestamp history from crossing into the post-command ordinary stream.
	frames := s.segmenter.ClaimActiveUtterance()
	if len(frames) == 0 {
		s.segmenter.Reset()
	}
	_ = s.command.Replay(ctx, frames)
	return true
}

func (s *Service) flush(ctx context.Context, lastSeen time.Time) ([]vad.Event, error) {
	if lastSeen.IsZero() {
		return nil, nil
	}
	events, err := s.segmenter.Flush(ctx, lastSeen.Add(time.Nanosecond))
	if err != nil {
		return nil, fmt.Errorf("flush audio segment: %w", err)
	}
	return events, nil
}

func (s *Service) handleEvents(ctx context.Context, request Request, events []vad.Event) error {
	for _, event := range events {
		if event.Type != vad.EventFinal {
			continue
		}
		_, err := s.processor.ProcessAudio(ctx, pipeline.TurnProcessRequest{
			SessionID:      request.SessionID,
			AccountID:      request.AccountID,
			TraceID:        request.TraceID,
			SourceLanguage: request.SourceLanguage,
			StartedAt:      event.StartedAt,
			AudioChunks:    audioChunks(event.Frames),
		})
		if err != nil {
			return fmt.Errorf("process audio Turn: %w", err)
		}
	}
	return nil
}

func (s *Service) logVADCheckpoint(request Request, event vad.Event) {
	if s == nil || s.latency == nil {
		return
	}
	switch event.Type {
	case vad.EventOpened:
		s.latency.Info("realtime latency checkpoint",
			"stage", "vad_open",
			"session_id", request.SessionID,
			"trace_id", request.TraceID,
			"started_at", event.StartedAt,
		)
	case vad.EventFinal:
		s.latency.Info("realtime latency checkpoint",
			"stage", "vad_final",
			"session_id", request.SessionID,
			"trace_id", request.TraceID,
			"started_at", event.StartedAt,
			"ended_at", event.EndedAt,
			"segment_audio_ms", event.EndedAt.Sub(event.StartedAt).Milliseconds(),
			"frame_count", len(event.Frames),
		)
	}
}

func (s *Service) logPostCommitFailure(request Request, err error) {
	if s == nil || s.latency == nil || err == nil {
		return
	}
	s.latency.Error("realtime turn post-commit processing failed",
		"session_id", request.SessionID,
		"trace_id", request.TraceID,
		"error", err,
	)
}

func (s *Service) logUnsupportedSourceLanguage(request Request, err error) {
	if s == nil || s.latency == nil || err == nil {
		return
	}
	s.latency.Warn("realtime turn ignored unsupported source language",
		"session_id", request.SessionID,
		"trace_id", request.TraceID,
		"error_class", "unsupported_source_language",
		"error", err,
	)
}

func audioChunks(frames []audio.Frame) [][]byte {
	chunks := make([][]byte, 0, len(frames))
	for _, frame := range frames {
		chunks = append(chunks, append([]byte(nil), frame.PCM...))
	}
	return chunks
}
