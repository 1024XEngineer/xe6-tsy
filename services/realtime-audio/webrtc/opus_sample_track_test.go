package webrtc

import (
	"context"
	"testing"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/pipeline"
	"github.com/pion/webrtc/v4/pkg/media"
)

func TestOpusSampleTrackEncodesPCMForWebRTC(t *testing.T) {
	writer := &sampleRecorder{}
	track, err := newOpusSampleTrack(writer, MediaConfig{
		SampleRate: 24_000,
		Channels:   1,
	})
	if err != nil {
		t.Fatalf("newOpusSampleTrack() error = %v", err)
	}
	pcm := make([]byte, 480*2)
	for index := 0; index < len(pcm); index += 2 {
		pcm[index] = 0x00
		pcm[index+1] = 0x20
	}
	if err := track.Write(context.Background(), pipeline.AudioChunk{
		SessionID:  "session-1",
		PlaybackID: "playback-1",
		SequenceNo: 1,
		Data:       pcm,
	}); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if len(writer.samples) != 1 {
		t.Fatalf("sample count = %d, want 1", len(writer.samples))
	}
	if len(writer.samples[0].Data) == 0 || string(writer.samples[0].Data) == string([]byte{0xf8, 0xff, 0xfe}) {
		t.Fatalf("sample is still a silence placeholder: %x", writer.samples[0].Data)
	}
	if writer.samples[0].Duration != 20*time.Millisecond {
		t.Fatalf("sample duration = %v, want 20ms", writer.samples[0].Duration)
	}
	decoder, err := NewOpusDecoder()
	if err != nil {
		t.Fatalf("NewOpusDecoder() error = %v", err)
	}
	decoded, err := decoder.Decode(writer.samples[0].Data)
	if err != nil || len(decoded) == 0 {
		t.Fatalf("Decode(encoded sample) = %d bytes, error = %v", len(decoded), err)
	}
}

func TestOpusSampleTrackPadsShortPCMToOneFrame(t *testing.T) {
	writer := &sampleRecorder{}
	track, err := newOpusSampleTrack(writer, MediaConfig{SampleRate: 24_000, Channels: 1})
	if err != nil {
		t.Fatalf("newOpusSampleTrack() error = %v", err)
	}
	if err := track.Write(context.Background(), pipeline.AudioChunk{
		SessionID:  "session-1",
		PlaybackID: "playback-1",
		SequenceNo: 1,
		Data:       []byte{0, 1},
	}); err != nil {
		t.Fatalf("Write(short PCM) error = %v", err)
	}
	if len(writer.samples) != 1 {
		t.Fatalf("sample count = %d, want 1", len(writer.samples))
	}
}

type sampleRecorder struct {
	samples []media.Sample
}

func (r *sampleRecorder) WriteSample(sample media.Sample) error {
	r.samples = append(r.samples, sample)
	return nil
}
