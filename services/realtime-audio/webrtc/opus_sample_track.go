package webrtc

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/pipeline"
	"github.com/pion/webrtc/v4/pkg/media"
)

// Compact Opus comfort-noise / silence frame (20ms). Used until a real PCM→Opus
// encoder is wired; proves the Chrome downlink path for mock TTS.
var opusSilenceFrame = []byte{0xf8, 0xff, 0xfe}

type opusSampleWriter interface {
	WriteSample(media.Sample) error
}

// OpusSampleTrack adapts mock/real PCM chunks into Opus samples for browsers.
type OpusSampleTrack struct {
	track   opusSampleWriter
	mu      sync.Mutex
	stopped map[string]bool
}

func newOpusSampleTrack(track opusSampleWriter) (*OpusSampleTrack, error) {
	if track == nil {
		return nil, ErrMediaUnavailable
	}
	return &OpusSampleTrack{track: track, stopped: make(map[string]bool)}, nil
}

func (t *OpusSampleTrack) Write(ctx context.Context, chunk pipeline.AudioChunk) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if t == nil || t.track == nil {
		return ErrMediaUnavailable
	}
	if chunk.PlaybackID == "" || len(chunk.Data) == 0 {
		return ErrInvalidDependency
	}
	t.mu.Lock()
	if t.stopped[chunk.PlaybackID] {
		t.mu.Unlock()
		return ErrPlaybackStopped
	}
	t.mu.Unlock()

	// Rough 20ms frame count from PCM byte length (16-bit mono @ configured rate).
	frames := len(chunk.Data) / (2 * 24000 / 50)
	if frames < 1 {
		frames = 1
	}
	for i := 0; i < frames; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		t.mu.Lock()
		stopped := t.stopped[chunk.PlaybackID]
		t.mu.Unlock()
		if stopped {
			return ErrPlaybackStopped
		}
		if err := t.track.WriteSample(media.Sample{
			Data:     append([]byte(nil), opusSilenceFrame...),
			Duration: 20 * time.Millisecond,
		}); err != nil {
			return fmt.Errorf("write Opus TTS sample: %w", err)
		}
	}
	return nil
}

func (t *OpusSampleTrack) Stop(ctx context.Context, playbackID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if playbackID == "" {
		return ErrInvalidDependency
	}
	t.mu.Lock()
	t.stopped[playbackID] = true
	t.mu.Unlock()
	return nil
}
