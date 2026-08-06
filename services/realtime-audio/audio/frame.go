package audio

import (
	"errors"
	"fmt"
	"time"
)

const SupportedSampleRate = 16_000

var (
	ErrPCMRequired           = errors.New("PCM audio is required")
	ErrPCMAlignment          = errors.New("PCM audio must contain complete 16-bit samples")
	ErrUnsupportedSampleRate = errors.New("unsupported audio sample rate")
	ErrCaptureTimeRequired   = errors.New("audio capture time is required")
)

// Frame is the normalized audio unit consumed by the local VAD core.
// PCM is mono signed 16-bit little-endian audio at SupportedSampleRate.
type Frame struct {
	PCM        []byte
	SampleRate int
	CapturedAt time.Time
}

// NewFrame validates and owns a copy of one normalized audio frame.
func NewFrame(pcm []byte, sampleRate int, capturedAt time.Time) (Frame, error) {
	if len(pcm) == 0 {
		return Frame{}, ErrPCMRequired
	}
	if len(pcm)%2 != 0 {
		return Frame{}, ErrPCMAlignment
	}
	if sampleRate != SupportedSampleRate {
		return Frame{}, fmt.Errorf("%w: %d", ErrUnsupportedSampleRate, sampleRate)
	}
	if capturedAt.IsZero() {
		return Frame{}, ErrCaptureTimeRequired
	}
	return Frame{PCM: append([]byte(nil), pcm...), SampleRate: sampleRate, CapturedAt: capturedAt}, nil
}

// Clone returns an independently owned copy of the frame.
func (f Frame) Clone() Frame {
	f.PCM = append([]byte(nil), f.PCM...)
	return f
}
