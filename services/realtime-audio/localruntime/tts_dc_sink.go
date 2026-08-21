package localruntime

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"sync"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/pipeline"
)

// Keep each DataChannel JSON well under typical SCTP/message caps.
const (
	maxTTSPCMChunkBytes          = 8 * 1024
	maxSettledPlaybackTombstones = 128
	ttsDataChannelPublishTimeout = 5 * time.Second
)

// DataChannelTTSAudioSink buffers one playback's audio, then ships DC-safe
// chunks. The browser reassembles by playback_id before decoding/playing so
// WAV/MP3 containers are never split mid-frame.
//
// PipelineService calls Complete via AudioPlaybackLifecycle after TTS finishes.
type DataChannelTTSAudioSink struct {
	Media      MediaLookup
	SampleRate int
	Failures   DataChannelFailureObserver

	mu           sync.Mutex
	buffers      map[ttsPlaybackKey]*ttsBuffer
	publishing   map[ttsPlaybackKey]bool
	settled      map[ttsPlaybackKey]struct{}
	settledOrder []ttsPlaybackKey
	publishAudio ttsAudioPublisher
}

var _ pipeline.AudioPlaybackLifecycle = (*DataChannelTTSAudioSink)(nil)
var _ pipeline.AudioChunkSink = (*DataChannelTTSAudioSink)(nil)
var _ interface {
	InterruptCurrent(context.Context, string, string) error
} = (*DataChannelTTSAudioSink)(nil)

type ttsBuffer struct {
	sessionID string
	turnID    string
	encoding  string
	pcm       []byte
}

type ttsPlaybackKey struct {
	sessionID  string
	playbackID string
}

type ttsAudioPublisher func(
	context.Context,
	string,
	string,
	string,
	int64,
	bool,
	string,
	[]byte,
) error

// FrontendTTSAudio is consumed by lingow-voice-demo Web Audio playback.
type FrontendTTSAudio struct {
	Type       string `json:"type"`
	Event      string `json:"event"`
	PlaybackID string `json:"playback_id"`
	SessionID  string `json:"session_id"`
	TurnID     string `json:"turn_id"`
	SampleRate int    `json:"sample_rate_hz"`
	Channels   int    `json:"channels"`
	Encoding   string `json:"encoding"`
	PCMBase64  string `json:"pcm_base64"`
	SequenceNo int64  `json:"sequence"`
	Final      bool   `json:"final"`
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
	key := ttsPlaybackKey{sessionID: chunk.SessionID, playbackID: chunk.PlaybackID}
	if _, ok := s.settled[key]; ok {
		return nil
	}
	if s.buffers == nil {
		s.buffers = make(map[ttsPlaybackKey]*ttsBuffer)
	}
	buf := s.buffers[key]
	if buf == nil {
		buf = &ttsBuffer{sessionID: chunk.SessionID, turnID: chunk.TurnID}
		s.buffers[key] = buf
	}
	if chunk.TurnID != "" {
		buf.turnID = chunk.TurnID
	}
	if chunk.SessionID != "" {
		buf.sessionID = chunk.SessionID
	}
	if chunk.Encoding != "" {
		buf.encoding = chunk.Encoding
	}
	buf.pcm = append(buf.pcm, chunk.Data...)
	return nil
}

func (s *DataChannelTTSAudioSink) Complete(ctx context.Context, sessionID, playbackID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	key, buf := s.bufferLocked(sessionID, playbackID)
	sessionID = key.sessionID
	if _, interrupted := s.settled[key]; interrupted {
		delete(s.buffers, key)
		s.releaseSettledLocked(key)
		s.mu.Unlock()
		return nil
	}
	delete(s.buffers, key)
	if buf == nil || len(buf.pcm) == 0 {
		s.mu.Unlock()
		return nil
	}
	if s.publishing == nil {
		s.publishing = make(map[ttsPlaybackKey]bool)
	}
	s.publishing[key] = false
	s.mu.Unlock()
	defer s.finishPublishing(key)

	// Prefer raw PCM when the provider returned a complete WAV; keeps browser
	// playback on the pcm_s16le path. Containers that are not WAV stay intact
	// and are reassembled client-side before decodeAudioData.
	audio := normalizeTTSAudio(buf.pcm, buf.encoding)
	pieces := splitBytes(audio.data, maxTTSPCMChunkBytes)
	for i, piece := range pieces {
		if s.playbackSettled(key) {
			return nil
		}
		publish := s.publishAudio
		if publish == nil {
			publish = s.publish
		}
		if err := publish(ctx, sessionID, playbackID, buf.turnID, int64(i+1), i == len(pieces)-1, audio.encoding, piece); err != nil {
			return err
		}
	}
	return nil
}

func (s *DataChannelTTSAudioSink) Cancel(ctx context.Context, sessionID, playbackID, _ string) error {
	s.mu.Lock()
	key, _ := s.bufferLocked(sessionID, playbackID)
	delete(s.buffers, key)
	s.markSettledLocked(key)
	if _, publishing := s.publishing[key]; publishing {
		s.publishing[key] = true
	}
	s.mu.Unlock()
	return ctx.Err()
}

// InterruptCurrent discards every not-yet-published PCM buffer for a session.
// Browser playback is stopped by the local wake-word path; clearing these
// buffers prevents a TTS completion racing with the wake signal from sending
// stale audio after the command window opens.
func (s *DataChannelTTSAudioSink) InterruptCurrent(ctx context.Context, sessionID, _ string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil || sessionID == "" {
		return nil
	}
	s.mu.Lock()
	for key := range s.buffers {
		if key.sessionID == sessionID {
			delete(s.buffers, key)
			s.markSettledLocked(key)
		}
	}
	for key := range s.publishing {
		if key.sessionID == sessionID {
			s.publishing[key] = true
			s.markSettledLocked(key)
		}
	}
	s.mu.Unlock()
	return nil
}

// bufferLocked resolves legacy callers that omit sessionID only when one
// buffer owns the playback ID. Ambiguous IDs are never guessed across sessions.
func (s *DataChannelTTSAudioSink) bufferLocked(sessionID, playbackID string) (ttsPlaybackKey, *ttsBuffer) {
	key := ttsPlaybackKey{sessionID: sessionID, playbackID: playbackID}
	if buffer := s.buffers[key]; buffer != nil || sessionID != "" {
		return key, buffer
	}
	var match ttsPlaybackKey
	var buffer *ttsBuffer
	for candidate, candidateBuffer := range s.buffers {
		if candidate.playbackID != playbackID {
			continue
		}
		if buffer != nil {
			return key, nil
		}
		match, buffer = candidate, candidateBuffer
	}
	if buffer != nil {
		return match, buffer
	}
	return key, nil
}

func (s *DataChannelTTSAudioSink) playbackSettled(key ttsPlaybackKey) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	interrupted, publishing := s.publishing[key]
	if publishing {
		return interrupted
	}
	_, settled := s.settled[key]
	return settled
}

func (s *DataChannelTTSAudioSink) finishPublishing(key ttsPlaybackKey) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.publishing, key)
	s.releaseSettledLocked(key)
}

func (s *DataChannelTTSAudioSink) markSettledLocked(key ttsPlaybackKey) {
	if key.playbackID == "" {
		return
	}
	if s.settled == nil {
		s.settled = make(map[ttsPlaybackKey]struct{})
	}
	if _, exists := s.settled[key]; exists {
		return
	}
	s.settled[key] = struct{}{}
	s.settledOrder = append(s.settledOrder, key)
	if len(s.settledOrder) <= maxSettledPlaybackTombstones {
		return
	}
	oldest := s.settledOrder[0]
	s.settledOrder = s.settledOrder[1:]
	delete(s.settled, oldest)
}

func (s *DataChannelTTSAudioSink) releaseSettledLocked(key ttsPlaybackKey) {
	if _, exists := s.settled[key]; !exists {
		return
	}
	delete(s.settled, key)
	for index, candidate := range s.settledOrder {
		if candidate == key {
			s.settledOrder = append(s.settledOrder[:index], s.settledOrder[index+1:]...)
			return
		}
	}
}

type normalizedTTSAudio struct {
	data     []byte
	encoding string
}

func normalizeTTSAudio(raw []byte, declaredEncoding string) normalizedTTSAudio {
	if declaredEncoding == "pcm_s16le" {
		return normalizedTTSAudio{data: raw, encoding: declaredEncoding}
	}
	if pcm, ok := wavPCMData(raw); ok {
		return normalizedTTSAudio{data: pcm, encoding: "pcm_s16le"}
	}
	// Unknown / MP3 / partial stream: ship bytes as-is; browser reassembles.
	return normalizedTTSAudio{data: raw, encoding: "audio_container"}
}

func wavPCMData(raw []byte) ([]byte, bool) {
	if len(raw) < 44 || string(raw[0:4]) != "RIFF" || string(raw[8:12]) != "WAVE" {
		return nil, false
	}
	offset := 12
	for offset+8 <= len(raw) {
		chunkID := string(raw[offset : offset+4])
		chunkSize := int(binary.LittleEndian.Uint32(raw[offset+4 : offset+8]))
		offset += 8
		if chunkSize < 0 || offset+chunkSize > len(raw) {
			return nil, false
		}
		if chunkID == "data" {
			return append([]byte(nil), raw[offset:offset+chunkSize]...), true
		}
		offset += chunkSize
		if chunkSize%2 == 1 {
			offset++
		}
	}
	return nil, false
}

func splitBytes(data []byte, max int) [][]byte {
	if len(data) == 0 {
		return nil
	}
	if max <= 0 {
		max = maxTTSPCMChunkBytes
	}
	pieces := make([][]byte, 0, (len(data)+max-1)/max)
	for len(data) > 0 {
		n := max
		if len(data) < n {
			n = len(data)
		}
		pieces = append(pieces, append([]byte(nil), data[:n]...))
		data = data[n:]
	}
	return pieces
}

func (s *DataChannelTTSAudioSink) publish(
	ctx context.Context,
	sessionID, playbackID, turnID string,
	sequence int64,
	final bool,
	encoding string,
	pcm []byte,
) error {
	if s.Media == nil || len(pcm) == 0 {
		if len(pcm) > 0 {
			s.recordFailure()
		}
		return nil
	}
	media, err := s.Media.CurrentMedia(ctx, sessionID)
	if err != nil || media == nil {
		s.recordFailure()
		return nil
	}
	sink := media.TranslationEvents()
	if sink == nil {
		s.recordFailure()
		return nil
	}
	rate := defaultTTSSampleRate(s.SampleRate)
	if encoding == "" {
		encoding = "pcm_s16le"
	}
	payload := FrontendTTSAudio{
		Type:       "tts.audio",
		Event:      "tts.audio",
		PlaybackID: playbackID,
		SessionID:  sessionID,
		TurnID:     turnID,
		SampleRate: rate,
		Channels:   1,
		Encoding:   encoding,
		PCMBase64:  base64.StdEncoding.EncodeToString(pcm),
		SequenceNo: sequence,
		Final:      final,
	}
	publishCtx, cancel := newTTSPublishContext(ctx)
	defer cancel()
	if err := sink.PublishJSON(publishCtx, payload); err != nil {
		s.recordFailure()
		return fmt.Errorf("publish TTS audio chunk seq=%d: %w", sequence, err)
	}
	return nil
}

func defaultTTSSampleRate(sampleRate int) int {
	if sampleRate <= 0 {
		return 24000
	}
	return sampleRate
}

func newTTSPublishContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, ttsDataChannelPublishTimeout)
}

func (s *DataChannelTTSAudioSink) recordFailure() {
	if s.Failures != nil {
		s.Failures.RecordDataChannelFailure()
	}
}
