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
	SilenceAfter  time.Duration
	MaxDuration   time.Duration
	PrefixPadding time.Duration
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
	classifier    Classifier
	silenceAfter  time.Duration
	maxDuration   time.Duration
	prefixPadding time.Duration
	active        bool
	startedAt     time.Time
	lastSpeech    time.Time
	lastSeen      time.Time
	frames        []audio.Frame
	prefixFrames  []audio.Frame
}

// Reset abandons the current utterance and all timestamp/prefix history.
//
// A wake-word command is a separate input boundary from ordinary turns. The
// caller must reset the segmenter before feeding the first command frame so
// audio buffered before the wake signal cannot be joined to a later turn.
func (s *Segmenter) Reset() {
	if s == nil {
		return
	}
	s.reset()
}

// ClaimActiveUtterance transfers the complete in-flight utterance to another
// consumer and resets ordinary segmentation. The returned frames are owned by
// the caller; an inactive segmenter never exposes prefix-only silence.
func (s *Segmenter) ClaimActiveUtterance() []audio.Frame {
	if s == nil || !s.active {
		return nil
	}
	frames := make([]audio.Frame, len(s.frames))
	for index, frame := range s.frames {
		frames[index] = frame.Clone()
	}
	s.reset()
	return frames
}

func NewSegmenter(classifier Classifier, options Options) (*Segmenter, error) {
	if classifier == nil {
		return nil, ErrClassifierRequired
	}
	if options.SilenceAfter <= 0 || options.MaxDuration <= 0 || options.SilenceAfter >= options.MaxDuration ||
		options.PrefixPadding < 0 || options.PrefixPadding >= options.MaxDuration {
		return nil, ErrInvalidOptions
	}
	return &Segmenter{
		classifier: classifier, silenceAfter: options.SilenceAfter,
		maxDuration: options.MaxDuration, prefixPadding: options.PrefixPadding,
	}, nil
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

	isSpeech := s.classifier.Speech(frame)
	if s.active && frame.CapturedAt.Sub(s.startedAt) >= s.maxDuration {
		events := s.finalize(s.startedAt.Add(s.maxDuration))
		if isSpeech {
			s.start(frame)
			events = append(events, Event{Type: EventOpened, StartedAt: s.startedAt}, s.audioEvent(frame))
		} else {
			s.rememberPrefix(frame)
		}
		return events, nil
	}

	if isSpeech {
		if !s.active {
			s.start(frame)
			return []Event{{Type: EventOpened, StartedAt: s.startedAt}, s.audioEvent(frame)}, nil
		}
		s.lastSpeech = frame.CapturedAt
		s.frames = append(s.frames, frame.Clone())
		return []Event{s.audioEvent(frame)}, nil
	}
	if s.active {
		// Preserve quiet phonemes and pauses inside an utterance. The classifier
		// decides boundaries; it must not destructively filter the ASR waveform.
		s.frames = append(s.frames, frame.Clone())
		if frame.CapturedAt.Sub(s.lastSpeech) >= s.silenceAfter {
			return s.finalize(frame.CapturedAt), nil
		}
		return []Event{s.audioEvent(frame)}, nil
	}
	s.rememberPrefix(frame)
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
	s.frames = make([]audio.Frame, 0, len(s.prefixFrames)+1)
	for _, prefix := range s.prefixFrames {
		s.frames = append(s.frames, prefix.Clone())
	}
	if len(s.frames) > 0 {
		s.startedAt = s.frames[0].CapturedAt
	}
	s.frames = append(s.frames, frame.Clone())
	s.prefixFrames = nil
}

func (s *Segmenter) rememberPrefix(frame audio.Frame) {
	if s.prefixPadding <= 0 {
		return
	}
	s.prefixFrames = append(s.prefixFrames, frame.Clone())
	cutoff := frame.CapturedAt.Add(-s.prefixPadding)
	first := 0
	for first < len(s.prefixFrames) && s.prefixFrames[first].CapturedAt.Before(cutoff) {
		first++
	}
	if first > 0 {
		kept := make([]audio.Frame, len(s.prefixFrames)-first)
		copy(kept, s.prefixFrames[first:])
		s.prefixFrames = kept
	}
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
	s.prefixFrames = nil
	return []Event{event}
}

func (s *Segmenter) reset() {
	s.resetActive()
	s.lastSeen = time.Time{}
	s.prefixFrames = nil
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
