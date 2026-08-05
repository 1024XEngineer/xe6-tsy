package webrtc

import (
	"context"
	"encoding/binary"
	"math"
	"math/rand"
	"reflect"
	"testing"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/pipeline"
	"github.com/pion/webrtc/v4/pkg/media"
	opus "github.com/tphakala/go-opus/opus"
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
	for index := 0; index < 480; index++ {
		sample := int16(math.Sin(2*math.Pi*440*float64(index)/24_000) * 12_000)
		binary.LittleEndian.PutUint16(pcm[index*2:], uint16(sample))
	}
	if err := track.Write(context.Background(), pipeline.AudioChunk{
		SessionID:  "session-1",
		PlaybackID: "playback-1",
		SequenceNo: 1,
		Data:       pcm,
	}); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	configureImmediateOpusPacing(track)
	if err := track.Complete(context.Background(), "playback-1"); err != nil {
		t.Fatalf("Complete() error = %v", err)
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
	packetDuration, err := opus.PacketDuration(writer.samples[0].Data)
	if err != nil {
		t.Fatalf("PacketDuration(encoded sample) error = %v", err)
	}
	if packetDuration != 960 {
		t.Fatalf("packet duration = %d samples at 48kHz, want 960", packetDuration)
	}
	decoder, err := NewOpusDecoder()
	if err != nil {
		t.Fatalf("NewOpusDecoder() error = %v", err)
	}
	decoded, err := decoder.Decode(writer.samples[0].Data)
	if err != nil || len(decoded) == 0 {
		t.Fatalf("Decode(encoded sample) = %d bytes, error = %v", len(decoded), err)
	}
	var peak int16
	for index := 0; index < len(decoded); index += 2 {
		sample := int16(binary.LittleEndian.Uint16(decoded[index:]))
		if sample < 0 {
			sample = -sample
		}
		if sample > peak {
			peak = sample
		}
	}
	if peak < 100 {
		t.Fatalf("decoded Opus signal peak = %d, want audible PCM", peak)
	}
}

func TestOpusSampleTrackFlushesShortPCMOnlyOnComplete(t *testing.T) {
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
	if len(writer.samples) != 0 {
		t.Fatalf("sample count before Complete = %d, want 0", len(writer.samples))
	}
	if err := track.Complete(context.Background(), "playback-1"); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if len(writer.samples) != 1 {
		t.Fatalf("sample count after Complete = %d, want 1", len(writer.samples))
	}
}

func TestOpusSampleTrackCarriesPartialPCMAcrossProviderChunks(t *testing.T) {
	writer := &sampleRecorder{}
	track, err := newOpusSampleTrack(writer, MediaConfig{SampleRate: 24_000, Channels: 1})
	if err != nil {
		t.Fatalf("newOpusSampleTrack() error = %v", err)
	}
	track.encoder = &pcmRecordingEncoder{}
	configureImmediateOpusPacing(track)
	first := make([]byte, 200*2)
	second := make([]byte, 280*2)
	if err := track.Write(context.Background(), pipeline.AudioChunk{
		SessionID: "session-1", PlaybackID: "playback-1", SequenceNo: 1, Data: first,
	}); err != nil {
		t.Fatalf("Write(first partial chunk) error = %v", err)
	}
	if len(writer.samples) != 0 {
		t.Fatalf("sample count after first partial chunk = %d, want 0", len(writer.samples))
	}
	if err := track.Write(context.Background(), pipeline.AudioChunk{
		SessionID: "session-1", PlaybackID: "playback-1", SequenceNo: 2, Data: second,
	}); err != nil {
		t.Fatalf("Write(second partial chunk) error = %v", err)
	}
	if len(writer.samples) != 0 {
		t.Fatalf("sample count before startup buffer fills = %d, want 0", len(writer.samples))
	}
	if err := track.Complete(context.Background(), "playback-1"); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if len(writer.samples) != 1 {
		t.Fatalf("sample count after Complete = %d, want 1", len(writer.samples))
	}
}

func TestOpusSampleTrackPrebuffersAndPacesFrames(t *testing.T) {
	writer := &sampleRecorder{}
	track, err := newOpusSampleTrack(writer, MediaConfig{SampleRate: 24_000, Channels: 1})
	if err != nil {
		t.Fatalf("newOpusSampleTrack() error = %v", err)
	}
	track.encoder = &pcmRecordingEncoder{}
	base := time.Unix(1_700_000_000, 0)
	track.now = func() time.Time { return base }
	var deadlines []time.Time
	track.waitUntil = func(_ context.Context, deadline time.Time) error {
		deadlines = append(deadlines, deadline)
		return nil
	}

	frame := make([]byte, 480*2)
	if err := track.Write(context.Background(), pipeline.AudioChunk{
		SessionID: "session-1", PlaybackID: "playback-1", SequenceNo: 1, Data: append([]byte(nil), frame...),
	}); err != nil {
		t.Fatalf("Write(first frame) error = %v", err)
	}
	if len(writer.samples) != 0 {
		t.Fatalf("sample count below startup buffer = %d, want 0", len(writer.samples))
	}
	if err := track.Write(context.Background(), pipeline.AudioChunk{
		SessionID: "session-1", PlaybackID: "playback-1", SequenceNo: 2, Data: make([]byte, (opusPrebufferFrames-1)*len(frame)),
	}); err != nil {
		t.Fatalf("Write(startup buffer) error = %v", err)
	}
	if len(writer.samples) != opusPrebufferFrames {
		t.Fatalf("sample count after startup buffer = %d, want %d", len(writer.samples), opusPrebufferFrames)
	}
	wantDeadlines := make([]time.Time, opusPrebufferFrames-1)
	for index := range wantDeadlines {
		wantDeadlines[index] = base.Add(time.Duration(index+1) * opusFrameDuration)
	}
	if !reflect.DeepEqual(deadlines, wantDeadlines) {
		t.Fatalf("pacing deadlines = %v, want %v", deadlines, wantDeadlines)
	}
}

func TestOpusSampleTrackPreservesPCMWithRandomProviderBoundaries(t *testing.T) {
	writer := &sampleRecorder{}
	track, err := newOpusSampleTrack(writer, MediaConfig{SampleRate: 24_000, Channels: 1})
	if err != nil {
		t.Fatalf("newOpusSampleTrack() error = %v", err)
	}
	encoder := &pcmRecordingEncoder{}
	track.encoder = encoder
	configureImmediateOpusPacing(track)

	const sampleCount = 37*480 + 123
	pcm := make([]byte, sampleCount*2)
	for index := 0; index < sampleCount; index++ {
		binary.LittleEndian.PutUint16(pcm[index*2:], uint16(int16(index*29)))
	}
	random := rand.New(rand.NewSource(42))
	sequence := int64(1)
	for offset := 0; offset < len(pcm); sequence++ {
		samples := random.Intn(700) + 1
		end := offset + samples*2
		if end > len(pcm) {
			end = len(pcm)
		}
		if err := track.Write(context.Background(), pipeline.AudioChunk{
			SessionID: "session-1", PlaybackID: "playback-1", SequenceNo: sequence, Data: pcm[offset:end],
		}); err != nil {
			t.Fatalf("Write(random chunk %d) error = %v", sequence, err)
		}
		offset = end
	}
	if err := track.Complete(context.Background(), "playback-1"); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	want := make([]int16, ((sampleCount+479)/480)*480)
	for index := 0; index < sampleCount; index++ {
		want[index] = int16(binary.LittleEndian.Uint16(pcm[index*2:]))
	}
	if !reflect.DeepEqual(encoder.pcm, want) {
		t.Fatal("encoded PCM differs after random provider chunking")
	}
}

func TestOpusSampleTrackPreservesToneInsteadOfProducingNoise(t *testing.T) {
	writer := &sampleRecorder{}
	track, err := newOpusSampleTrack(writer, MediaConfig{SampleRate: 24_000, Channels: 1})
	if err != nil {
		t.Fatalf("newOpusSampleTrack() error = %v", err)
	}
	configureImmediateOpusPacing(track)

	const frameCount = 12
	pcm := make([]byte, frameCount*480*2)
	for index := 0; index < frameCount*480; index++ {
		sample := int16(math.Sin(2*math.Pi*440*float64(index)/24_000) * 12_000)
		binary.LittleEndian.PutUint16(pcm[index*2:], uint16(sample))
	}
	if err := track.Write(context.Background(), pipeline.AudioChunk{
		SessionID:  "session-1",
		PlaybackID: "playback-1",
		SequenceNo: 1,
		Data:       pcm,
	}); err != nil {
		t.Fatalf("Write(tone PCM) error = %v", err)
	}
	if len(writer.samples) != frameCount {
		t.Fatalf("sample count = %d, want %d", len(writer.samples), frameCount)
	}

	decoder, err := NewOpusDecoder()
	if err != nil {
		t.Fatalf("NewOpusDecoder() error = %v", err)
	}
	decoded := make([]byte, 0, frameCount*320*2)
	for _, sample := range writer.samples {
		frame, decodeErr := decoder.Decode(sample.Data)
		if decodeErr != nil {
			t.Fatalf("Decode(encoded tone) error = %v", decodeErr)
		}
		decoded = append(decoded, frame...)
	}

	// Skip encoder lookahead, then ensure a 440 Hz tone has a bounded number
	// of sign changes. Broadband corruption crosses zero far more often.
	decoded = decoded[3*320*2:]
	zeroCrossings := 0
	previousSign := 0
	for index := 0; index < len(decoded); index += 2 {
		sample := int16(binary.LittleEndian.Uint16(decoded[index:]))
		if sample > -100 && sample < 100 {
			continue
		}
		sign := 1
		if sample < 0 {
			sign = -1
		}
		if previousSign != 0 && sign != previousSign {
			zeroCrossings++
		}
		previousSign = sign
	}
	if zeroCrossings < 120 || zeroCrossings > 190 {
		t.Fatalf("decoded tone zero crossings = %d, want 120..190", zeroCrossings)
	}
}

type sampleRecorder struct {
	samples []media.Sample
}

type pcmRecordingEncoder struct {
	pcm []int16
}

func (e *pcmRecordingEncoder) Encode(pcm []int16, packet []byte) (int, error) {
	e.pcm = append(e.pcm, pcm...)
	packet[0] = 1
	return 1, nil
}

func configureImmediateOpusPacing(track *OpusSampleTrack) {
	track.now = func() time.Time { return time.Unix(1_700_000_000, 0) }
	track.waitUntil = func(context.Context, time.Time) error { return nil }
}

func (r *sampleRecorder) WriteSample(sample media.Sample) error {
	r.samples = append(r.samples, sample)
	return nil
}
