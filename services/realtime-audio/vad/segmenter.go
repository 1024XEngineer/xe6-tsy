package vad

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/audio"
)

var (
	ErrClassifierRequired    = errors.New("speech classifier is required")
	ErrInvalidOptions        = errors.New("invalid VAD options")
	ErrNonMonotonicTimestamp = errors.New("audio timestamps must be strictly increasing")
)

type Classifier interface {
	Speech(audio.Frame) bool
}

type Options struct {
	SilenceAfter time.Duration
	MaxDuration  time.Duration
}

type EventType string

const (
	EventOpened EventType = "opened"
	EventAudio  EventType = "audio"
	EventFinal  EventType = "final"
)

type Event struct {
	Type      EventType
	Frame     *audio.Frame
	Frames    []audio.Frame
	StartedAt time.Time
	EndedAt   time.Time
}

type Segmenter struct {
	classifier   Classifier
	silenceAfter time.Duration
	maxDuration  time.Duration
	active       bool
	startedAt    time.Time
	lastSpeech   time.Time
	lastSeen     time.Time
	frames       []audio.Frame
}

func NewSegmenter(classifier Classifier, options Options) (*Segmenter, error) {
	if classifier == nil {
		return nil, ErrClassifierRequired
	}
	if options.SilenceAfter <= 0 || options.MaxDuration <= 0 || options.SilenceAfter >= options.MaxDuration {
		return nil, ErrInvalidOptions
	}
	return &Segmenter{classifier: classifier, silenceAfter: options.SilenceAfter, maxDuration: options.MaxDuration}, nil
}

func (s *Segmenter) Push(ctx context.Context, frame audio.Frame) ([]Event, error) {
	if err := ctx.Err(); err != nil {
		if s != nil {
			s.reset()
		}
		return nil, err
	}
	if s == nil || s.classifier == nil {
		return nil, ErrClassifierRequired
	}
	if err := validateFrame(frame); err != nil {
		return nil, err
	}
	if !s.lastSeen.IsZero() && !frame.CapturedAt.After(s.lastSeen) {
		return nil, fmt.Errorf("%w: %s is not after %s", ErrNonMonotonicTimestamp, frame.CapturedAt, s.lastSeen)
	}
	s.lastSeen = frame.CapturedAt

	if s.active && frame.CapturedAt.Sub(s.startedAt) >= s.maxDuration {
		events := s.finalize(s.startedAt.Add(s.maxDuration))
		if s.classifier.Speech(frame) {
			s.start(frame)
			events = append(events, Event{Type: EventOpened, StartedAt: s.startedAt}, s.audioEvent(frame))
		}
		return events, nil
	}

	if s.classifier.Speech(frame) {
		if !s.active {
			s.start(frame)
			return []Event{{Type: EventOpened, StartedAt: s.startedAt}, s.audioEvent(frame)}, nil
		}
		s.lastSpeech = frame.CapturedAt
		s.frames = append(s.frames, frame.Clone())
		return []Event{s.audioEvent(frame)}, nil
	}
	if s.active && frame.CapturedAt.Sub(s.lastSpeech) >= s.silenceAfter {
		return s.finalize(frame.CapturedAt), nil
	}
	return nil, nil
}

func (s *Segmenter) Flush(ctx context.Context, endedAt time.Time) ([]Event, error) {
	if err := ctx.Err(); err != nil {
		if s != nil {
			s.reset()
		}
		return nil, err
	}
	if s == nil || s.classifier == nil {
		return nil, ErrClassifierRequired
	}
	if endedAt.IsZero() {
		return nil, audio.ErrCaptureTimeRequired
	}
	if !s.active {
		return nil, nil
	}
	if !endedAt.After(s.lastSeen) {
		return nil, fmt.Errorf("%w: flush time %s is not after %s", ErrNonMonotonicTimestamp, endedAt, s.lastSeen)
	}
	s.lastSeen = endedAt
	return s.finalize(endedAt), nil
}

func (s *Segmenter) start(frame audio.Frame) {
	s.active = true
	s.startedAt = frame.CapturedAt
	s.lastSpeech = frame.CapturedAt
	s.frames = []audio.Frame{frame.Clone()}
}

func (s *Segmenter) audioEvent(frame audio.Frame) Event {
	copy := frame.Clone()
	return Event{Type: EventAudio, Frame: &copy, StartedAt: s.startedAt}
}

func (s *Segmenter) finalize(endedAt time.Time) []Event {
	frames := make([]audio.Frame, len(s.frames))
	for index, frame := range s.frames {
		frames[index] = frame.Clone()
	}
	event := Event{Type: EventFinal, Frames: frames, StartedAt: s.startedAt, EndedAt: endedAt}
	s.resetActive()
	return []Event{event}
}

func (s *Segmenter) reset() {
	s.resetActive()
	s.lastSeen = time.Time{}
}

func (s *Segmenter) resetActive() {
	s.active = false
	s.startedAt = time.Time{}
	s.lastSpeech = time.Time{}
	s.frames = nil
}

func validateFrame(frame audio.Frame) error {
	if len(frame.PCM) == 0 {
		return audio.ErrPCMRequired
	}
	if len(frame.PCM)%2 != 0 {
		return audio.ErrPCMAlignment
	}
	if frame.SampleRate != audio.SupportedSampleRate {
		return audio.ErrUnsupportedSampleRate
	}
	if frame.CapturedAt.IsZero() {
		return audio.ErrCaptureTimeRequired
	}
	return nil
}
