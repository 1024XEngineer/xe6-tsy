package localruntime

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"testing"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/pipeline"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/playback"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/segment"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/webrtc"
)

func TestSplitBytes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		dataLen   int
		max       int
		wantCount int
		wantLast  int
	}{
		{name: "empty", dataLen: 0, max: 8, wantCount: 0},
		{name: "exact chunks", dataLen: maxTTSPCMChunkBytes * 2, max: maxTTSPCMChunkBytes, wantCount: 2, wantLast: maxTTSPCMChunkBytes},
		{name: "remainder", dataLen: maxTTSPCMChunkBytes*2 + 3, max: maxTTSPCMChunkBytes, wantCount: 3, wantLast: 3},
		{name: "nonpositive max uses default", dataLen: 5, max: 0, wantCount: 1, wantLast: 5},
		{name: "negative max uses default", dataLen: 5, max: -1, wantCount: 1, wantLast: 5},
		{name: "smaller max", dataLen: 10, max: 4, wantCount: 3, wantLast: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			data := make([]byte, tt.dataLen)
			pieces := splitBytes(data, tt.max)
			if len(pieces) != tt.wantCount {
				t.Fatalf("len(pieces)=%d, want %d", len(pieces), tt.wantCount)
			}
			if tt.wantCount == 0 {
				return
			}
			if len(pieces[len(pieces)-1]) != tt.wantLast {
				t.Fatalf("last=%d, want %d", len(pieces[len(pieces)-1]), tt.wantLast)
			}
		})
	}
}

func TestWavPCMData(t *testing.T) {
	t.Parallel()

	pcm := []byte{1, 0, 2, 0, 3, 0, 4, 0}

	tests := []struct {
		name string
		raw  []byte
		ok   bool
		want []byte
	}{
		{name: "valid wav", raw: makeWAV(pcm), ok: true, want: pcm},
		{name: "too short", raw: []byte("RIFF"), ok: false},
		{name: "bad riff", raw: append([]byte("XIFF"), make([]byte, 40)...), ok: false},
		{name: "bad wave", raw: func() []byte {
			b := makeWAV(pcm)
			copy(b[8:], []byte("NOPE"))
			return b
		}(), ok: false},
		{name: "truncated data chunk", raw: func() []byte {
			b := makeWAV(pcm)
			return b[:40]
		}(), ok: false},
		{name: "oversize chunk", raw: func() []byte {
			b := makeWAV(pcm)
			binary.LittleEndian.PutUint32(b[40:], 1<<30)
			return b
		}(), ok: false},
		{name: "no data chunk", raw: func() []byte {
			b := make([]byte, 44)
			copy(b[0:], []byte("RIFF"))
			binary.LittleEndian.PutUint32(b[4:], 36)
			copy(b[8:], []byte("WAVEfmt "))
			binary.LittleEndian.PutUint32(b[16:], 16)
			copy(b[36:], []byte("LIST"))
			binary.LittleEndian.PutUint32(b[40:], 0)
			return b
		}(), ok: false},
		{name: "odd-sized chunk padding", raw: makeWAVWithOddListChunk(pcm), ok: true, want: pcm},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := wavPCMData(tt.raw)
			if ok != tt.ok {
				t.Fatalf("ok=%v, want %v", ok, tt.ok)
			}
			if !tt.ok {
				return
			}
			if string(got) != string(tt.want) {
				t.Fatalf("pcm = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNormalizeTTSAudio(t *testing.T) {
	t.Parallel()

	pcm := []byte{1, 0, 2, 0}
	wav := makeWAV(pcm)
	norm := normalizeTTSAudio(wav, "")
	if norm.encoding != "pcm_s16le" || string(norm.data) != string(pcm) {
		t.Fatalf("wav normalize = %#v", norm)
	}

	raw := []byte{0xff, 0xfb, 1, 2, 3, 4}
	norm = normalizeTTSAudio(raw, "")
	if norm.encoding != "audio_container" || string(norm.data) != string(raw) {
		t.Fatalf("container normalize = %#v", norm)
	}
}

func TestNormalizeTTSAudioHonorsDeclaredRawPCM(t *testing.T) {
	t.Parallel()

	rawPCM := []byte{0x52, 0x49, 0x46, 0x46, 0x01, 0x00}
	norm := normalizeTTSAudio(rawPCM, "pcm_s16le")
	if norm.encoding != "pcm_s16le" || string(norm.data) != string(rawPCM) {
		t.Fatalf("raw PCM normalize = %#v", norm)
	}
}

func TestDataChannelTTSAudioSinkPublishCompleteCancel(t *testing.T) {
	t.Parallel()

	t.Run("publish ignores empty and canceled", func(t *testing.T) {
		t.Parallel()
		sink := &DataChannelTTSAudioSink{}
		canceled, cancel := context.WithCancel(context.Background())
		cancel()
		if err := sink.Publish(canceled, pipeline.AudioChunk{PlaybackID: "p1", Data: []byte{1}}); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled Publish error = %v", err)
		}
		if err := sink.Publish(context.Background(), pipeline.AudioChunk{PlaybackID: "", Data: []byte{1}}); err != nil {
			t.Fatalf("empty playback Publish error = %v", err)
		}
		if err := sink.Publish(context.Background(), pipeline.AudioChunk{PlaybackID: "p1", Data: nil}); err != nil {
			t.Fatalf("empty data Publish error = %v", err)
		}
	})

	t.Run("complete with nil media ships normalized chunks without error", func(t *testing.T) {
		t.Parallel()
		sink := &DataChannelTTSAudioSink{SampleRate: 0}
		pcm := make([]byte, maxTTSPCMChunkBytes+4)
		for i := range pcm {
			pcm[i] = byte(i)
		}
		if err := sink.Publish(context.Background(), pipeline.AudioChunk{
			SessionID:  "session-1",
			TurnID:     "turn-1",
			PlaybackID: "playback-1",
			Data:       makeWAV(pcm),
		}); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		if err := sink.Publish(context.Background(), pipeline.AudioChunk{
			SessionID:  "session-2",
			TurnID:     "turn-2",
			PlaybackID: "playback-1",
			Data:       []byte{},
		}); err != nil {
			t.Fatalf("Publish empty append: %v", err)
		}
		// Empty Data is ignored; buffer still holds the WAV from the first publish.
		if err := sink.Complete(context.Background(), "", "playback-1"); err != nil {
			t.Fatalf("Complete: %v", err)
		}
		if err := sink.Complete(context.Background(), "session-2", "playback-1"); err != nil {
			t.Fatalf("Complete empty: %v", err)
		}
	})

	t.Run("complete unknown container with media miss", func(t *testing.T) {
		t.Parallel()
		sink := &DataChannelTTSAudioSink{
			Media:      stubMediaLookup{},
			SampleRate: 16000,
		}
		if err := sink.Publish(context.Background(), pipeline.AudioChunk{
			SessionID:  "session-1",
			PlaybackID: "playback-1",
			Data:       []byte{0xff, 0xfb, 1, 2, 3, 4},
		}); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		if err := sink.Complete(context.Background(), "session-1", "playback-1"); err != nil {
			t.Fatalf("Complete with unavailable media = %v", err)
		}
	})

	t.Run("complete canceled context", func(t *testing.T) {
		t.Parallel()
		sink := &DataChannelTTSAudioSink{}
		_ = sink.Publish(context.Background(), pipeline.AudioChunk{PlaybackID: "p1", Data: []byte{1}})
		canceled, cancel := context.WithCancel(context.Background())
		cancel()
		if err := sink.Complete(canceled, "s1", "p1"); !errors.Is(err, context.Canceled) {
			t.Fatalf("Complete canceled = %v", err)
		}
	})

	t.Run("cancel drops buffer and surfaces ctx error", func(t *testing.T) {
		t.Parallel()
		sink := &DataChannelTTSAudioSink{}
		_ = sink.Publish(context.Background(), pipeline.AudioChunk{PlaybackID: "p1", Data: []byte{1, 2, 3}})
		canceled, cancel := context.WithCancel(context.Background())
		cancel()
		if err := sink.Cancel(canceled, "s1", "p1", "interrupt"); !errors.Is(err, context.Canceled) {
			t.Fatalf("Cancel = %v", err)
		}
		if err := sink.Complete(context.Background(), "s1", "p1"); err != nil {
			t.Fatalf("Complete after cancel = %v", err)
		}
	})

	t.Run("publish with nil translation events is best-effort", func(t *testing.T) {
		t.Parallel()
		sink := &DataChannelTTSAudioSink{
			Media: mediaLookupFunc(func(context.Context, string) (webrtc.MediaTransport, error) {
				return &fakeMediaTransport{}, nil
			}),
		}
		_ = sink.Publish(context.Background(), pipeline.AudioChunk{
			SessionID:  "session-1",
			PlaybackID: "playback-1",
			Data:       []byte{1, 2, 3, 4},
		})
		if err := sink.Complete(context.Background(), "session-1", "playback-1"); err != nil {
			t.Fatalf("Complete with nil events = %v", err)
		}
	})
}

func TestDataChannelTTSAudioSinkInterruptCurrentDropsSessionBuffers(t *testing.T) {
	sink := &DataChannelTTSAudioSink{}
	if err := sink.Publish(context.Background(), pipeline.AudioChunk{
		SessionID: "session-1", PlaybackID: "playback-1", Data: []byte{1},
	}); err != nil {
		t.Fatalf("Publish(session-1) error = %v", err)
	}
	if err := sink.Publish(context.Background(), pipeline.AudioChunk{
		SessionID: "session-2", PlaybackID: "playback-2", Data: []byte{2},
	}); err != nil {
		t.Fatalf("Publish(session-2) error = %v", err)
	}
	if err := sink.InterruptCurrent(context.Background(), "session-1", "wake_word_detected"); err != nil {
		t.Fatalf("InterruptCurrent() error = %v", err)
	}
	if len(sink.buffers) != 1 {
		t.Fatalf("buffers after interrupt = %d, want only another session", len(sink.buffers))
	}
	if _, ok := sink.buffers["playback-1"]; ok {
		t.Fatal("session-1 playback buffer was not removed")
	}
	if err := sink.Publish(context.Background(), pipeline.AudioChunk{
		SessionID: "session-1", PlaybackID: "playback-1", Data: []byte{3},
	}); err != nil {
		t.Fatalf("Publish(late chunk) error = %v", err)
	}
	if _, ok := sink.buffers["playback-1"]; ok {
		t.Fatal("late chunk recreated an interrupted playback buffer")
	}
	if err := sink.Complete(context.Background(), "session-1", "playback-1"); err != nil {
		t.Fatalf("Complete(interrupted) error = %v", err)
	}
	if _, ok := sink.settled[ttsPlaybackKey{sessionID: "session-1", playbackID: "playback-1"}]; ok {
		t.Fatal("final completion did not release interrupted playback marker")
	}
}

func TestDataChannelTTSAudioSinkInterruptionIsScopedToSession(t *testing.T) {
	sink := &DataChannelTTSAudioSink{}
	if err := sink.Publish(context.Background(), pipeline.AudioChunk{
		SessionID: "session-1", PlaybackID: "shared-playback", Data: []byte{1},
	}); err != nil {
		t.Fatalf("Publish(session-1) error = %v", err)
	}
	if err := sink.InterruptCurrent(context.Background(), "session-1", "wake_word_detected"); err != nil {
		t.Fatalf("InterruptCurrent() error = %v", err)
	}
	if err := sink.Publish(context.Background(), pipeline.AudioChunk{
		SessionID: "session-2", PlaybackID: "shared-playback", Data: []byte{2},
	}); err != nil {
		t.Fatalf("Publish(session-2) error = %v", err)
	}
	buffer := sink.buffers["shared-playback"]
	if buffer == nil || buffer.sessionID != "session-2" {
		t.Fatalf("another session buffer = %#v, want retained session-2 audio", buffer)
	}
}

func TestDataChannelTTSAudioSinkInterruptCurrentStopsPublishingChunks(t *testing.T) {
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	published := make(chan int64, 2)
	sink := &DataChannelTTSAudioSink{
		publishAudio: func(
			_ context.Context,
			_, _, _ string,
			sequence int64,
			_ bool,
			_ string,
			_ []byte,
		) error {
			published <- sequence
			if sequence == 1 {
				close(firstStarted)
				<-releaseFirst
			}
			return nil
		},
	}
	audioData := make([]byte, maxTTSPCMChunkBytes+1)
	if err := sink.Publish(context.Background(), pipeline.AudioChunk{
		SessionID: "session-1", PlaybackID: "playback-1", Data: audioData, Encoding: "pcm_s16le",
	}); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	complete := make(chan error, 1)
	go func() {
		complete <- sink.Complete(context.Background(), "session-1", "playback-1")
	}()
	<-firstStarted
	if err := sink.InterruptCurrent(context.Background(), "session-1", "wake_word_detected"); err != nil {
		t.Fatalf("InterruptCurrent() error = %v", err)
	}
	close(releaseFirst)
	if err := <-complete; err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	close(published)
	var sequences []int64
	for sequence := range published {
		sequences = append(sequences, sequence)
	}
	if len(sequences) != 1 || sequences[0] != 1 {
		t.Fatalf("published sequences = %v, want only in-flight first chunk", sequences)
	}
	key := ttsPlaybackKey{sessionID: "session-1", playbackID: "playback-1"}
	if _, ok := sink.publishing[key]; ok {
		t.Fatal("completed playback remained in publishing index")
	}
	if _, ok := sink.settled[key]; ok {
		t.Fatal("completed playback retained its interruption tombstone")
	}
}

func TestDataChannelTTSAudioSinkPublishingInterruptSurvivesTombstoneEviction(t *testing.T) {
	key := ttsPlaybackKey{sessionID: "session-1", playbackID: "playback-1"}
	sink := &DataChannelTTSAudioSink{
		publishing: map[ttsPlaybackKey]bool{key: true},
	}
	for index := 0; index <= maxSettledPlaybackTombstones; index++ {
		sink.mu.Lock()
		sink.markSettledLocked(ttsPlaybackKey{
			sessionID:  "another-session",
			playbackID: fmt.Sprintf("playback-%d", index),
		})
		sink.mu.Unlock()
	}
	if !sink.playbackSettled(key) {
		t.Fatal("publishing interruption was lost after tombstone eviction")
	}
}

func TestDataChannelTTSAudioSinkCountsUnavailableChannel(t *testing.T) {
	failures := &recordingDataChannelFailures{}
	sink := &DataChannelTTSAudioSink{Failures: failures}
	if err := sink.publish(t.Context(), "session-1", "playback-1", "turn-1", 1, true, "pcm_s16le", []byte{1, 2}); err != nil {
		t.Fatalf("publish() error = %v", err)
	}
	if failures.calls != 1 {
		t.Fatalf("data channel failures = %d, want 1", failures.calls)
	}
}

func makeWAV(pcm []byte) []byte {
	buf := make([]byte, 44+len(pcm))
	copy(buf[0:], []byte("RIFF"))
	binary.LittleEndian.PutUint32(buf[4:], uint32(36+len(pcm)))
	copy(buf[8:], []byte("WAVEfmt "))
	binary.LittleEndian.PutUint32(buf[16:], 16)
	binary.LittleEndian.PutUint16(buf[20:], 1)
	binary.LittleEndian.PutUint16(buf[22:], 1)
	binary.LittleEndian.PutUint32(buf[24:], 24000)
	binary.LittleEndian.PutUint32(buf[28:], 48000)
	binary.LittleEndian.PutUint16(buf[32:], 2)
	binary.LittleEndian.PutUint16(buf[34:], 16)
	copy(buf[36:], []byte("data"))
	binary.LittleEndian.PutUint32(buf[40:], uint32(len(pcm)))
	copy(buf[44:], pcm)
	return buf
}

// makeWAVWithOddListChunk inserts an odd-sized LIST chunk before data so the
// pad-byte branch in wavPCMData is exercised.
func makeWAVWithOddListChunk(pcm []byte) []byte {
	listPayload := []byte{1} // odd size
	buf := make([]byte, 12+8+16+8+len(listPayload)+1+8+len(pcm))
	copy(buf[0:], []byte("RIFF"))
	binary.LittleEndian.PutUint32(buf[4:], uint32(len(buf)-8))
	copy(buf[8:], []byte("WAVE"))
	offset := 12
	copy(buf[offset:], []byte("fmt "))
	binary.LittleEndian.PutUint32(buf[offset+4:], 16)
	offset += 8 + 16
	copy(buf[offset:], []byte("LIST"))
	binary.LittleEndian.PutUint32(buf[offset+4:], uint32(len(listPayload)))
	offset += 8
	copy(buf[offset:], listPayload)
	offset += len(listPayload) + 1 // pad
	copy(buf[offset:], []byte("data"))
	binary.LittleEndian.PutUint32(buf[offset+4:], uint32(len(pcm)))
	copy(buf[offset+8:], pcm)
	return buf
}

type mediaLookupFunc func(ctx context.Context, sessionID string) (webrtc.MediaTransport, error)

func (f mediaLookupFunc) CurrentMedia(ctx context.Context, sessionID string) (webrtc.MediaTransport, error) {
	return f(ctx, sessionID)
}

type fakeMediaTransport struct {
	source segment.FrameSource
	events *webrtc.PionEventSink
}

func (f *fakeMediaTransport) Answer(context.Context, webrtc.SessionDescription) (webrtc.SessionDescription, error) {
	return webrtc.SessionDescription{}, nil
}
func (*fakeMediaTransport) AddCandidate(context.Context, webrtc.ICECandidate) error { return nil }
func (*fakeMediaTransport) EndCandidates(context.Context) error                     { return nil }
func (*fakeMediaTransport) Close(context.Context) error                             { return nil }
func (f *fakeMediaTransport) AudioSource() segment.FrameSource                      { return f.source }
func (*fakeMediaTransport) TTSAudioTrack() *webrtc.PionAudioTrack                   { return nil }
func (f *fakeMediaTransport) TranslationEvents() *webrtc.PionEventSink              { return f.events }
func (*fakeMediaTransport) Playback() *playback.Service                             { return nil }
