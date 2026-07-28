package segment

import (
	"context"
	"errors"
	"fmt"
	"io"
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
type FrameSource interface {
	ReadFrame(ctx context.Context) (audio.Frame, error)
	Close() error
}

// TurnProcessor accepts exactly one finalized utterance and opens the member-3 Turn boundary.
type TurnProcessor interface {
	ProcessAudio(ctx context.Context, request pipeline.TurnProcessRequest) (pipeline.TurnContext, error)
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
}

// Service reads normalized frames, applies VAD, and sends only finalized utterances downstream.
type Service struct {
	source    FrameSource
	segmenter *vad.Segmenter
	processor TurnProcessor
}

func NewService(deps Dependencies) (*Service, error) {
	if deps.Source == nil || deps.Segmenter == nil || deps.Processor == nil {
		return nil, ErrDependencyRequired
	}
	return &Service{source: deps.Source, segmenter: deps.Segmenter, processor: deps.Processor}, nil
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

	var lastSeen time.Time
	for {
		frame, err := s.source.ReadFrame(ctx)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return s.flush(ctx, request, lastSeen)
			}
			return fmt.Errorf("read audio frame: %w", err)
		}
		lastSeen = frame.CapturedAt
		events, err := s.segmenter.Push(ctx, frame)
		if err != nil {
			return fmt.Errorf("segment audio frame: %w", err)
		}
		if err := s.handleEvents(ctx, request, events); err != nil {
			return err
		}
	}
}

func (s *Service) flush(ctx context.Context, request Request, lastSeen time.Time) error {
	if lastSeen.IsZero() {
		return nil
	}
	events, err := s.segmenter.Flush(ctx, lastSeen.Add(time.Nanosecond))
	if err != nil {
		return fmt.Errorf("flush audio segment: %w", err)
	}
	return s.handleEvents(ctx, request, events)
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

func audioChunks(frames []audio.Frame) [][]byte {
	chunks := make([][]byte, 0, len(frames))
	for _, frame := range frames {
		chunks = append(chunks, append([]byte(nil), frame.PCM...))
	}
	return chunks
}
