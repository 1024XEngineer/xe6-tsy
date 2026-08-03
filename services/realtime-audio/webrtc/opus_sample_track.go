package webrtc

import (
	"context"
	"encoding/binary"
	"fmt"
	"sync"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/pipeline"
	opus "github.com/kazzmir/opus-go/opus"
	"github.com/pion/webrtc/v4/pkg/media"
)

const (
	opusFrameDuration = 20 * time.Millisecond
	opusMaxPacketSize = 1275
)

type opusSampleWriter interface {
	WriteSample(media.Sample) error
}

type opusPCMEncoder interface {
	Encode(pcm []int16, frameSize int, packet []byte) (int, error)
}

// OpusSampleTrack encodes signed 16-bit PCM into browser-compatible Opus samples.
type OpusSampleTrack struct {
	track           opusSampleWriter
	sampleRate      int
	channels        int
	samplesPerFrame int
	encoder         opusPCMEncoder

	mu      sync.Mutex
	writeMu sync.Mutex
	stopped map[string]bool
}

func newOpusSampleTrack(track opusSampleWriter, config MediaConfig) (*OpusSampleTrack, error) {
	if track == nil {
		return nil, ErrMediaUnavailable
	}
	normalized, err := config.normalized()
	if err != nil {
		return nil, err
	}
	samplesPerFrame := normalized.SampleRate / 50
	if samplesPerFrame <= 0 {
		return nil, ErrMediaConfigInvalid
	}
	encoder, err := opus.NewEncoder(normalized.SampleRate, normalized.Channels, opus.ApplicationAudio)
	if err != nil {
		return nil, fmt.Errorf("create Opus encoder: %w", err)
	}
	if err := encoder.SetBitrate(32_000); err != nil {
		return nil, fmt.Errorf("configure Opus bitrate: %w", err)
	}
	if err := encoder.SetVBR(true); err != nil {
		return nil, fmt.Errorf("configure Opus VBR: %w", err)
	}
	return &OpusSampleTrack{
		track:           track,
		sampleRate:      normalized.SampleRate,
		channels:        normalized.Channels,
		samplesPerFrame: samplesPerFrame,
		encoder:         encoder,
		stopped:         make(map[string]bool),
	}, nil
}

func (t *OpusSampleTrack) Write(ctx context.Context, chunk pipeline.AudioChunk) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if t == nil || t.track == nil || t.encoder == nil {
		return ErrMediaUnavailable
	}
	if chunk.PlaybackID == "" || len(chunk.Data) == 0 {
		return ErrInvalidDependency
	}
	if len(chunk.Data)%(2*t.channels) != 0 {
		return ErrInvalidDependency
	}

	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	bytesPerFrame := t.samplesPerFrame * 2 * t.channels
	for offset := 0; offset < len(chunk.Data); {
		if err := ctx.Err(); err != nil {
			return err
		}
		if t.isStopped(chunk.PlaybackID) {
			return ErrPlaybackStopped
		}
		end := offset + bytesPerFrame
		if end > len(chunk.Data) {
			end = len(chunk.Data)
		}
		pcm := make([]int16, t.samplesPerFrame*t.channels)
		for index := 0; index < (end-offset)/2; index++ {
			pcm[index] = int16(binary.LittleEndian.Uint16(chunk.Data[offset+index*2:]))
		}
		packet := make([]byte, opusMaxPacketSize)
		size, err := t.encoder.Encode(pcm, t.samplesPerFrame, packet)
		if err != nil {
			return fmt.Errorf("encode TTS PCM as Opus: %w", err)
		}
		if size <= 0 {
			return fmt.Errorf("encode TTS PCM as Opus: empty packet")
		}
		if err := t.track.WriteSample(media.Sample{
			Data:     append([]byte(nil), packet[:size]...),
			Duration: opusFrameDuration,
		}); err != nil {
			return fmt.Errorf("write Opus TTS sample: %w", err)
		}
		offset = end
	}
	return nil
}

func (t *OpusSampleTrack) isStopped(playbackID string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.stopped[playbackID]
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
