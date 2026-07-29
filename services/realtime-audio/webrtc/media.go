package webrtc

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/audio"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/pipeline"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/playback"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/segment"
	"github.com/pion/opus"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

const (
	defaultMediaSampleRate = audio.SupportedSampleRate
	defaultMediaChannels   = 1
	defaultDataChannel     = "translation-events"
)

var (
	ErrMediaUnavailable    = errors.New("WebRTC media is unavailable")
	ErrMediaConfigInvalid  = errors.New("WebRTC media configuration is invalid")
	ErrRemoteTrackRequired = errors.New("remote audio track is required")
	ErrRemoteTrackAttached = errors.New("remote audio track is already attached")
	ErrDecoderRequired     = errors.New("RTP audio decoder is required")
	ErrPlaybackStopped     = errors.New("playback track is stopped")
)

// MediaConfig defines the local media format and advertised identifiers.
// TTS chunks are signed 16-bit little-endian PCM at SampleRate and Channels.
type MediaConfig struct {
	TTSTrackID       string
	DataChannelLabel string
	SampleRate       int
	Channels         int
}

func (c MediaConfig) normalized() (MediaConfig, error) {
	if c.TTSTrackID == "" {
		c.TTSTrackID = defaultTTSTrackID
	}
	if c.DataChannelLabel == "" {
		c.DataChannelLabel = defaultDataChannel
	}
	if c.SampleRate == 0 {
		c.SampleRate = defaultMediaSampleRate
	}
	if c.Channels == 0 {
		c.Channels = defaultMediaChannels
	}
	if c.SampleRate <= 0 || c.Channels != 1 {
		return MediaConfig{}, ErrMediaConfigInvalid
	}
	return c, nil
}

// MediaTransport exposes the optional media capabilities of a signaling transport.
// ConnectionManager continues to depend only on ConnectionTransport.
type MediaTransport interface {
	ConnectionTransport
	AudioSource() segment.FrameSource
	TTSAudioTrack() *PionAudioTrack
	TranslationEvents() *PionEventSink
	Playback() *playback.Service
}

// PionAudioTrack writes normalized PCM into a Pion sample track.
type PionAudioTrack struct {
	track      pionRTPTrack
	sampleRate int
	channels   int

	mu        sync.Mutex
	stopped   map[string]bool
	sequence  uint16
	timestamp uint32
}

type pionRTPTrack interface {
	WriteRTP(*rtp.Packet) error
}

func newPionAudioTrack(track pionRTPTrack, config MediaConfig) (*PionAudioTrack, error) {
	if track == nil {
		return nil, ErrMediaUnavailable
	}
	config, err := config.normalized()
	if err != nil {
		return nil, err
	}
	return &PionAudioTrack{track: track, sampleRate: config.SampleRate, channels: config.Channels, stopped: make(map[string]bool)}, nil
}

// Write publishes one PCM chunk as an RTP L16 packet.
func (t *PionAudioTrack) Write(ctx context.Context, chunk pipeline.AudioChunk) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if t == nil || t.track == nil {
		return ErrMediaUnavailable
	}
	if chunk.PlaybackID == "" || len(chunk.Data) == 0 {
		return ErrInvalidDependency
	}
	if len(chunk.Data)%(2*t.channels) != 0 {
		return ErrInvalidDependency
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	stopped := t.stopped[chunk.PlaybackID]
	if stopped {
		return ErrPlaybackStopped
	}
	payload := make([]byte, len(chunk.Data))
	for index := 0; index < len(chunk.Data); index += 2 {
		payload[index] = chunk.Data[index+1]
		payload[index+1] = chunk.Data[index]
	}
	t.sequence++
	packet := &rtp.Packet{Header: rtp.Header{Version: 2, SequenceNumber: t.sequence, Timestamp: t.timestamp}, Payload: payload}
	t.timestamp += uint32(len(chunk.Data) / (2 * t.channels))
	if err := t.track.WriteRTP(packet); err != nil {
		return fmt.Errorf("write TTS sample: %w", err)
	}
	return nil
}

// Publish adapts the track to pipeline.AudioChunkSink.
func (t *PionAudioTrack) Publish(ctx context.Context, chunk pipeline.AudioChunk) error {
	return t.Write(ctx, chunk)
}

// Stop prevents future chunks for a playback while leaving the PeerConnection and track open.
func (t *PionAudioTrack) Stop(ctx context.Context, playbackID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if t == nil || t.track == nil {
		return ErrMediaUnavailable
	}
	if playbackID == "" {
		return playback.ErrPlaybackRequired
	}
	t.mu.Lock()
	t.stopped[playbackID] = true
	t.mu.Unlock()
	return nil
}

// PionEventSink serializes playback events to one translation-events DataChannel.
type PionEventSink struct {
	channel pionDataChannel
	open    chan struct{}
	openOne sync.Once
}

type pionDataChannel interface {
	OnOpen(func())
	ReadyState() webrtc.DataChannelState
	SendText(string) error
}

func newPionEventSink(channel pionDataChannel) *PionEventSink {
	sink := &PionEventSink{channel: channel, open: make(chan struct{})}
	if channel != nil {
		channel.OnOpen(func() { sink.openOne.Do(func() { close(sink.open) }) })
		if channel.ReadyState() == webrtc.DataChannelStateOpen {
			sink.openOne.Do(func() { close(sink.open) })
		}
	}
	return sink
}

// Publish waits for DataChannel open and sends one JSON event in order.
func (s *PionEventSink) Publish(ctx context.Context, event playback.Event) error {
	if s == nil || s.channel == nil {
		return ErrMediaUnavailable
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.open:
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("encode translation event: %w", err)
	}
	if err := s.channel.SendText(string(payload)); err != nil {
		return fmt.Errorf("send translation event: %w", err)
	}
	return nil
}

// RTPDecoder converts one RTP payload to normalized PCM.
type RTPDecoder interface {
	Decode(payload []byte) ([]byte, error)
}

// OpusDecoder is the default pure-Go decoder for browser WebRTC audio.
type OpusDecoder struct {
	decoder opus.Decoder
	output  []int16
}

// NewOpusDecoder creates a mono decoder at the normalized pipeline sample rate.
func NewOpusDecoder() (*OpusDecoder, error) {
	decoder, err := opus.NewDecoderWithOutput(defaultMediaSampleRate, defaultMediaChannels)
	if err != nil {
		return nil, err
	}
	return &OpusDecoder{decoder: decoder, output: make([]int16, defaultMediaSampleRate*120/1000)}, nil
}

// Decode returns copied signed 16-bit little-endian PCM.
func (d *OpusDecoder) Decode(payload []byte) ([]byte, error) {
	if d == nil {
		return nil, ErrDecoderRequired
	}
	if len(payload) == 0 {
		return nil, ErrDecoderRequired
	}
	samples, err := d.decoder.DecodeToInt16(payload, d.output)
	if err != nil {
		return nil, fmt.Errorf("decode Opus RTP payload: %w", err)
	}
	pcm := make([]byte, samples*2)
	for index, sample := range d.output[:samples] {
		binary.LittleEndian.PutUint16(pcm[index*2:], uint16(sample))
	}
	return pcm, nil
}

// PionAudioSource turns one remote Opus track into normalized audio.Frame values.
type PionAudioSource struct {
	decoder RTPDecoder
	now     func() time.Time
	frames  chan audio.Frame
	done    chan struct{}
	close   sync.Once

	mu       sync.Mutex
	err      error
	attached bool
}

type pionRemoteTrack interface {
	ReadRTP() (*rtp.Packet, error)
}

func newPionAudioSource(decoder RTPDecoder, now func() time.Time) (*PionAudioSource, error) {
	if decoder == nil {
		return nil, ErrDecoderRequired
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &PionAudioSource{decoder: decoder, now: now, frames: make(chan audio.Frame, 16), done: make(chan struct{})}, nil
}

// Attach starts the reader for the remote track exactly once.
func (s *PionAudioSource) Attach(track pionRemoteTrack) error {
	if s == nil || track == nil {
		return ErrRemoteTrackRequired
	}
	s.mu.Lock()
	if s.attached {
		s.mu.Unlock()
		return ErrRemoteTrackAttached
	}
	s.attached = true
	s.mu.Unlock()
	go s.readLoop(track)
	return nil
}

func (s *PionAudioSource) readLoop(track pionRemoteTrack) {
	for {
		packet, err := track.ReadRTP()
		if err != nil {
			if !errors.Is(err, io.EOF) {
				s.setError(err)
			}
			s.closeDone()
			return
		}
		pcm, err := s.decoder.Decode(packet.Payload)
		if err != nil {
			s.setError(err)
			s.closeDone()
			return
		}
		capturedAt := s.now()
		frame, err := audio.NewFrame(pcm, defaultMediaSampleRate, capturedAt)
		if err != nil {
			s.setError(err)
			s.closeDone()
			return
		}
		select {
		case s.frames <- frame:
		case <-s.done:
			return
		}
	}
}

// ReadFrame returns decoded audio or the terminal track error.
func (s *PionAudioSource) ReadFrame(ctx context.Context) (audio.Frame, error) {
	if s == nil {
		return audio.Frame{}, ErrMediaUnavailable
	}
	select {
	case <-ctx.Done():
		return audio.Frame{}, ctx.Err()
	case frame := <-s.frames:
		return frame, nil
	case <-s.done:
		select {
		case frame := <-s.frames:
			return frame, nil
		default:
		}
		s.mu.Lock()
		err := s.err
		s.mu.Unlock()
		if err != nil {
			return audio.Frame{}, err
		}
		return audio.Frame{}, io.EOF
	}
}

// Close stops delivery and is idempotent; the owning PeerConnection closes the remote reader.
func (s *PionAudioSource) Close() error {
	if s == nil {
		return nil
	}
	s.closeDone()
	return nil
}

func (s *PionAudioSource) setError(err error) {
	s.mu.Lock()
	if s.err == nil {
		s.err = err
	}
	s.mu.Unlock()
}

func (s *PionAudioSource) closeDone() {
	s.close.Do(func() {
		close(s.done)
	})
}

var _ RTPDecoder = (*OpusDecoder)(nil)
var _ pipeline.AudioChunkSink = (*PionAudioTrack)(nil)
var _ playback.EventSink = (*PionEventSink)(nil)
var _ interface {
	ReadFrame(context.Context) (audio.Frame, error)
	Close() error
} = (*PionAudioSource)(nil)
