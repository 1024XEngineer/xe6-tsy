package audio

import (
	"bytes"
	"errors"
	"testing"
	"time"
)

func TestNewFrameCopiesPCMAndAcceptsTheMVPFormat(t *testing.T) {
	pcm := []byte{1, 2, 3, 4}
	frame, err := NewFrame(pcm, SupportedSampleRate, time.Unix(10, 0))
	if err != nil {
		t.Fatalf("NewFrame() error = %v", err)
	}
	pcm[0] = 99
	if !bytes.Equal(frame.PCM, []byte{1, 2, 3, 4}) {
		t.Fatalf("Frame.PCM = %v, want copied input", frame.PCM)
	}
}

func TestNewFrameRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name       string
		pcm        []byte
		sampleRate int
		capturedAt time.Time
		want       error
	}{
		{name: "empty pcm", sampleRate: SupportedSampleRate, capturedAt: time.Unix(10, 0), want: ErrPCMRequired},
		{name: "partial sample", pcm: []byte{1}, sampleRate: SupportedSampleRate, capturedAt: time.Unix(10, 0), want: ErrPCMAlignment},
		{name: "unsupported sample rate", pcm: []byte{1, 0}, sampleRate: 8000, capturedAt: time.Unix(10, 0), want: ErrUnsupportedSampleRate},
		{name: "missing capture time", pcm: []byte{1, 0}, sampleRate: SupportedSampleRate, want: ErrCaptureTimeRequired},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewFrame(test.pcm, test.sampleRate, test.capturedAt)
			if !errors.Is(err, test.want) {
				t.Fatalf("NewFrame() error = %v, want %v", err, test.want)
			}
		})
	}
}
