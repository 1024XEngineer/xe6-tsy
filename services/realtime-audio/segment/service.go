package segment

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/audio"
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

// Boundary is an optional media boundary owned by the processor. The segment
// loop calls it before each frame and tags generated finals with its current
// generation so queued events cannot cross a mode switch.
type Boundary interface {
	BeforeFrame(context.Context, time.Time) error
	Generation() uint64
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
	Latency   *slog.Logger
}

// Service reads normalized frames, applies VAD, and sends only finalized utterances downstream.
type Service struct {
	source    FrameSource
	segmenter *vad.Segmenter
	processor TurnProcessor
	latency   *slog.Logger
}

// finalizedEventQueueCapacity bounds audio-turn backlog while provider calls run.
const finalizedEventQueueCapacity = 8

func NewService(deps Dependencies) (*Service, error) {
	if deps.Source == nil || deps.Segmenter == nil || deps.Processor == nil {
		return nil, ErrDependencyRequired
	}
	return &Service{source: deps.Source, segmenter: deps.Segmenter, processor: deps.Processor, latency: deps.Latency}, nil
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
	defer cancel()
	boundary, _ := s.processor.(Boundary)
	var generation uint64
	if boundary != nil {
		generation = boundary.Generation()
	}
	finalizedEvents := make(chan vad.Event, finalizedEventQueueCapacity)
	processingErrors := make(chan error, 1)
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		for event := range finalizedEvents {
			if err := s.handleEvents(runCtx, request, []vad.Event{event}); err != nil {
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
	var lastSeen time.Time
	var loopErr error
	for {
		frame, err := s.source.ReadFrame(runCtx)
		if err != nil {
			if errors.Is(err, io.EOF) {
				events, flushErr := s.flush(runCtx, lastSeen)
				if flushErr != nil {
					loopErr = flushErr
				} else {
					for _, event := range events {
						event.Generation = generation
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
		if boundary != nil {
			if err := boundary.BeforeFrame(runCtx, frame.CapturedAt); err != nil {
				loopErr = fmt.Errorf("prepare audio frame boundary: %w", err)
				break
			}
			currentGeneration := boundary.Generation()
			if currentGeneration != generation {
				s.segmenter.Reset()
				generation = currentGeneration
			}
		}
		lastSeen = frame.CapturedAt
		events, err := s.segmenter.Push(runCtx, frame)
		if err != nil {
			loopErr = fmt.Errorf("segment audio frame: %w", err)
			break
		}
		for _, event := range events {
			event.Generation = generation
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
			EndedAt:        event.EndedAt,
			AudioChunks:    audioChunks(event.Frames),
			Generation:     event.Generation,
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

func audioChunks(frames []audio.Frame) [][]byte {
	chunks := make([][]byte, 0, len(frames))
	for _, frame := range frames {
		chunks = append(chunks, append([]byte(nil), frame.PCM...))
	}
	return chunks
}
