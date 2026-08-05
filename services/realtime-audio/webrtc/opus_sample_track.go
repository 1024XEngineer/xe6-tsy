package webrtc

import (
	"context"
	"encoding/binary"
	"fmt"
	"sync"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/pipeline"
	"github.com/pion/webrtc/v4/pkg/media"
	opus "github.com/tphakala/go-opus/opus"
)

const (
	opusFrameDuration   = 20 * time.Millisecond
	opusPrebufferFrames = 6
	opusMaxPacketSize   = 1275
)

type opusSampleWriter interface {
	WriteSample(media.Sample) error
}

type opusPCMEncoder interface {
	Encode(pcm []int16, packet []byte) (int, error)
}

type goOpusEncoder struct {
	encoder *opus.Encoder
}

func (e *goOpusEncoder) Encode(pcm []int16, packet []byte) (int, error) {
	if e == nil || e.encoder == nil {
		return 0, fmt.Errorf("Opus encoder is unavailable")
	}
	return e.encoder.Encode(pcm, packet)
}

// OpusSampleTrack encodes signed 16-bit PCM into browser-compatible Opus samples.
type OpusSampleTrack struct {
	track           opusSampleWriter
	sampleRate      int
	channels        int
	samplesPerFrame int
	encoder         opusPCMEncoder

	mu           sync.Mutex
	writeMu      sync.Mutex
	stopped      map[string]bool
	pending      map[string][]byte
	lastSequence map[string]int64
	started      map[string]bool
	nextWrite    map[string]time.Time
	now          func() time.Time
	waitUntil    func(context.Context, time.Time) error
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
	encoder, err := opus.NewEncoder(opus.EncoderConfig{
		SampleRate: normalized.SampleRate,
		Channels:   normalized.Channels,
		Bitrate:    32_000,
		Complexity: 10,
	})
	if err != nil {
		return nil, fmt.Errorf("create Opus encoder: %w", err)
	}
	return &OpusSampleTrack{
		track:           track,
		sampleRate:      normalized.SampleRate,
		channels:        normalized.Channels,
		samplesPerFrame: samplesPerFrame,
		encoder:         &goOpusEncoder{encoder: encoder},
		stopped:         make(map[string]bool),
		pending:         make(map[string][]byte),
		lastSequence:    make(map[string]int64),
		started:         make(map[string]bool),
		nextWrite:       make(map[string]time.Time),
		now:             time.Now,
		waitUntil:       waitForOpusDeadline,
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
	t.mu.Lock()
	if chunk.SequenceNo != t.lastSequence[chunk.PlaybackID] {
		t.pending[chunk.PlaybackID] = append(t.pending[chunk.PlaybackID], chunk.Data...)
		t.lastSequence[chunk.PlaybackID] = chunk.SequenceNo
	}
	buffered := len(t.pending[chunk.PlaybackID])
	if !t.started[chunk.PlaybackID] && buffered >= bytesPerFrame*opusPrebufferFrames {
		t.started[chunk.PlaybackID] = true
	}
	started := t.started[chunk.PlaybackID]
	t.mu.Unlock()
	if !started {
		return nil
	}
	return t.drainFrames(ctx, chunk.PlaybackID, false)
}

// drainFrames preserves provider PCM across arbitrary chunk boundaries. The
// startup buffer absorbs provider delivery jitter; once started, deadlines
// keep RTP arrival aligned with the 20 ms timestamps written by Pion.
func (t *OpusSampleTrack) drainFrames(ctx context.Context, playbackID string, flushTail bool) error {
	bytesPerFrame := t.samplesPerFrame * 2 * t.channels
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if t.isStopped(playbackID) {
			return ErrPlaybackStopped
		}
		t.mu.Lock()
		buffered := t.pending[playbackID]
		if len(buffered) == 0 || (!flushTail && len(buffered) < bytesPerFrame) {
			t.mu.Unlock()
			return nil
		}
		frameBytes := bytesPerFrame
		if len(buffered) < frameBytes {
			frameBytes = len(buffered)
		}
		frame := make([]byte, bytesPerFrame)
		copy(frame, buffered[:frameBytes])
		t.mu.Unlock()
		if err := t.writeFrame(ctx, playbackID, frame); err != nil {
			return err
		}
		t.mu.Lock()
		buffered = t.pending[playbackID]
		if len(buffered) >= frameBytes {
			remaining := append([]byte(nil), buffered[frameBytes:]...)
			if len(remaining) == 0 {
				delete(t.pending, playbackID)
			} else {
				t.pending[playbackID] = remaining
			}
		}
		t.mu.Unlock()
		if frameBytes < bytesPerFrame {
			return nil
		}
	}
}

func (t *OpusSampleTrack) writeFrame(ctx context.Context, playbackID string, frame []byte) error {
	if err := t.paceFrame(ctx, playbackID); err != nil {
		return err
	}
	if t.isStopped(playbackID) {
		return ErrPlaybackStopped
	}
	pcm := make([]int16, t.samplesPerFrame*t.channels)
	for index := 0; index < len(frame)/2; index++ {
		pcm[index] = int16(binary.LittleEndian.Uint16(frame[index*2:]))
	}
	packet := make([]byte, opusMaxPacketSize)
	size, err := t.encoder.Encode(pcm, packet)
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
	t.markFrameSent(playbackID)
	return nil
}

func (t *OpusSampleTrack) paceFrame(ctx context.Context, playbackID string) error {
	t.mu.Lock()
	deadline := t.nextWrite[playbackID]
	waitUntil := t.waitUntil
	t.mu.Unlock()
	if deadline.IsZero() {
		return nil
	}
	if waitUntil == nil {
		waitUntil = waitForOpusDeadline
	}
	return waitUntil(ctx, deadline)
}

func (t *OpusSampleTrack) markFrameSent(playbackID string) {
	now := time.Now
	if t.now != nil {
		now = t.now
	}
	sentAt := now()
	t.mu.Lock()
	base := t.nextWrite[playbackID]
	if base.IsZero() || sentAt.After(base.Add(opusFrameDuration)) {
		base = sentAt
	}
	t.nextWrite[playbackID] = base.Add(opusFrameDuration)
	t.mu.Unlock()
}

func waitForOpusDeadline(ctx context.Context, deadline time.Time) error {
	delay := time.Until(deadline)
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// Complete flushes the final partial PCM frame only after the TTS stream ends.
func (t *OpusSampleTrack) Complete(ctx context.Context, playbackID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if playbackID == "" {
		return ErrInvalidDependency
	}
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	if t.isStopped(playbackID) {
		return ErrPlaybackStopped
	}
	t.mu.Lock()
	t.started[playbackID] = true
	t.mu.Unlock()
	if err := t.drainFrames(ctx, playbackID, true); err != nil {
		return err
	}
	t.mu.Lock()
	delete(t.pending, playbackID)
	delete(t.lastSequence, playbackID)
	delete(t.started, playbackID)
	delete(t.nextWrite, playbackID)
	t.mu.Unlock()
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
	delete(t.pending, playbackID)
	delete(t.lastSequence, playbackID)
	delete(t.started, playbackID)
	delete(t.nextWrite, playbackID)
	t.mu.Unlock()
	return nil
}
