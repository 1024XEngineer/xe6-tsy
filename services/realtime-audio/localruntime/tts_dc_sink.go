package localruntime

import (
	"context"
	"encoding/base64"
	"sync"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/pipeline"
)

// DataChannelTTSAudioSink buffers TTS PCM and publishes one playable event
// over the translation-events DataChannel when playback completes.
// Used when Chrome cannot receive L16 and a real Opus encoder is not wired yet.
type DataChannelTTSAudioSink struct {
	Media      MediaLookup
	SampleRate int

	mu      sync.Mutex
	buffers map[string]*ttsBuffer
}

type ttsBuffer struct {
	sessionID string
	pcm       []byte
}

// FrontendTTSAudio is consumed by lingow-voice-demo Web Audio playback.
type FrontendTTSAudio struct {
	Type        string `json:"type"`
	Event       string `json:"event"`
	PlaybackID  string `json:"playback_id"`
	SessionID   string `json:"session_id"`
	TurnID      string `json:"turn_id"`
	SampleRate  int    `json:"sample_rate_hz"`
	Channels    int    `json:"channels"`
	Encoding    string `json:"encoding"`
	PCMBase64   string `json:"pcm_base64"`
	SequenceNo  int64  `json:"sequence"`
}

func (s *DataChannelTTSAudioSink) Publish(ctx context.Context, chunk pipeline.AudioChunk) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if chunk.PlaybackID == "" || len(chunk.Data) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.buffers == nil {
		s.buffers = make(map[string]*ttsBuffer)
	}
	buf := s.buffers[chunk.PlaybackID]
	if buf == nil {
		buf = &ttsBuffer{sessionID: chunk.SessionID}
		s.buffers[chunk.PlaybackID] = buf
	}
	buf.pcm = append(buf.pcm, chunk.Data...)
	return nil
}

func (s *DataChannelTTSAudioSink) Complete(ctx context.Context, sessionID, playbackID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	buf := s.buffers[playbackID]
	delete(s.buffers, playbackID)
	s.mu.Unlock()
	if buf == nil || len(buf.pcm) == 0 {
		return nil
	}
	if sessionID == "" {
		sessionID = buf.sessionID
	}
	return s.publish(ctx, sessionID, playbackID, "", buf.pcm)
}

func (s *DataChannelTTSAudioSink) Cancel(ctx context.Context, sessionID, playbackID, _ string) error {
	s.mu.Lock()
	delete(s.buffers, playbackID)
	s.mu.Unlock()
	return ctx.Err()
}

func (s *DataChannelTTSAudioSink) publish(ctx context.Context, sessionID, playbackID, turnID string, pcm []byte) error {
	if s.Media == nil {
		return nil
	}
	media, err := s.Media.CurrentMedia(ctx, sessionID)
	if err != nil || media == nil {
		return nil
	}
	sink := media.TranslationEvents()
	if sink == nil {
		return nil
	}
	rate := s.SampleRate
	if rate <= 0 {
		rate = 24000
	}
	payload := FrontendTTSAudio{
		Type:       "tts.audio",
		Event:      "tts.audio",
		PlaybackID: playbackID,
		SessionID:  sessionID,
		TurnID:     turnID,
		SampleRate: rate,
		Channels:   1,
		Encoding:   "pcm_s16le",
		PCMBase64:  base64.StdEncoding.EncodeToString(pcm),
	}
	publishCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	_ = sink.PublishJSON(publishCtx, payload)
	return nil
}
