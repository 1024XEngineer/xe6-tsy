package webrtc

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/audio"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/pipeline"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/playback"
	"github.com/pion/rtp"
	pion "github.com/pion/webrtc/v4"
)

func TestPionAudioTrackCopiesPCMAndStopsOnlyOnePlayback(t *testing.T) {
	fake := &rtpTrackRecorder{}
	track, err := newPionAudioTrack(fake, MediaConfig{SampleRate: 16_000, Channels: 1})
	if err != nil {
		t.Fatalf("newPionAudioTrack() error = %v", err)
	}
	data := []byte{1, 2, 3, 4}
	chunk := pipeline.AudioChunk{SessionID: "session-1", PlaybackID: "playback-1", SequenceNo: 1, Data: data}
	if err := track.Write(context.Background(), chunk); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	data[0] = 9
	if err := track.Stop(context.Background(), "playback-1"); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if err := track.Write(context.Background(), pipeline.AudioChunk{SessionID: "session-1", PlaybackID: "playback-1", SequenceNo: 2, Data: []byte{5, 6}}); !errors.Is(err, ErrPlaybackStopped) {
		t.Fatalf("Write(stopped) error = %v", err)
	}
	if err := track.Write(context.Background(), pipeline.AudioChunk{SessionID: "session-1", PlaybackID: "playback-2", SequenceNo: 1, Data: []byte{7, 8}}); err != nil {
		t.Fatalf("Write(next playback) error = %v", err)
	}
	got := fake.Packets()
	if len(got) != 2 || !reflect.DeepEqual(got[0].Payload, []byte{2, 1, 4, 3}) || got[0].SequenceNumber != 1 || got[1].Timestamp != 2 {
		t.Fatalf("packets = %#v", got)
	}
}

func TestPionAudioTrackPacketizesPCMIntoTwentyMillisecondRTP(t *testing.T) {
	fake := &rtpTrackRecorder{}
	track, err := newPionAudioTrack(fake, MediaConfig{SampleRate: 16_000, Channels: 1})
	if err != nil {
		t.Fatalf("newPionAudioTrack() error = %v", err)
	}
	pcm := make([]byte, (320+160)*2)
	for index := range pcm {
		pcm[index] = byte(index)
	}
	if err := track.Write(context.Background(), pipeline.AudioChunk{SessionID: "session-1", PlaybackID: "playback-1", SequenceNo: 1, Data: pcm}); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	packets := fake.Packets()
	if len(packets) != 2 {
		t.Fatalf("RTP packet count = %d, want 2", len(packets))
	}
	if len(packets[0].Payload) != 320*2 || len(packets[1].Payload) != 160*2 {
		t.Fatalf("RTP payload lengths = %d, %d; want 640, 320", len(packets[0].Payload), len(packets[1].Payload))
	}
	if packets[0].SequenceNumber != 1 || packets[1].SequenceNumber != 2 {
		t.Fatalf("RTP sequence numbers = %d, %d; want 1, 2", packets[0].SequenceNumber, packets[1].SequenceNumber)
	}
	if packets[0].Timestamp != 0 || packets[1].Timestamp != 320 {
		t.Fatalf("RTP timestamps = %d, %d; want 0, 320", packets[0].Timestamp, packets[1].Timestamp)
	}
}

func TestPionAudioTrackPacketizesTwentyMillisecondsAtTwentyFourKilohertz(t *testing.T) {
	fake := &rtpTrackRecorder{}
	track, err := newPionAudioTrack(fake, MediaConfig{SampleRate: 24_000, Channels: 1})
	if err != nil {
		t.Fatalf("newPionAudioTrack() error = %v", err)
	}
	pcm := make([]byte, (480+1)*2)
	if err := track.Write(context.Background(), pipeline.AudioChunk{SessionID: "session-1", PlaybackID: "playback-1", SequenceNo: 1, Data: pcm}); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	packets := fake.Packets()
	if len(packets) != 2 || len(packets[0].Payload) != 480*2 || len(packets[1].Payload) != 2 || packets[1].Timestamp != 480 {
		t.Fatalf("24kHz packets = %#v, want 960-byte full packet and 2-byte tail at timestamp 480", packets)
	}
}

func TestPionAudioTrackReturnsRTPWriteError(t *testing.T) {
	expected := errors.New("RTP write failed")
	track, err := newPionAudioTrack(&failingRTPTrackRecorder{err: expected}, MediaConfig{SampleRate: 16_000, Channels: 1})
	if err != nil {
		t.Fatalf("newPionAudioTrack() error = %v", err)
	}
	err = track.Write(context.Background(), pipeline.AudioChunk{SessionID: "session-1", PlaybackID: "playback-1", SequenceNo: 1, Data: make([]byte, 640)})
	if !errors.Is(err, expected) {
		t.Fatalf("Write() error = %v, want wrapped RTP error", err)
	}
}

func TestPionAudioTrackRetriesOnlyUnsentPacketsAfterPartialFailure(t *testing.T) {
	expected := errors.New("second RTP write failed")
	recorder := &partialFailureRTPTrackRecorder{failAt: 2, err: expected}
	track, err := newPionAudioTrack(recorder, MediaConfig{SampleRate: 16_000, Channels: 1})
	if err != nil {
		t.Fatalf("newPionAudioTrack() error = %v", err)
	}
	pcm := make([]byte, (320+160)*2)
	for index := range pcm {
		pcm[index] = byte(index)
	}
	chunk := pipeline.AudioChunk{SessionID: "session-1", PlaybackID: "playback-1", SequenceNo: 1, Data: pcm}
	if err := track.Write(context.Background(), chunk); !errors.Is(err, expected) {
		t.Fatalf("first Write() error = %v, want %v", err, expected)
	}
	if got := len(recorder.Packets()); got != 1 {
		t.Fatalf("successful packets after partial failure = %d, want 1", got)
	}
	if err := track.Write(context.Background(), chunk); err != nil {
		t.Fatalf("retry Write() error = %v", err)
	}
	packets := recorder.Packets()
	if len(packets) != 2 {
		t.Fatalf("successful packets after retry = %d, want 2", len(packets))
	}
	if packets[0].SequenceNumber != 1 || packets[1].SequenceNumber != 2 {
		t.Fatalf("packet sequence numbers = %d, %d; want 1, 2", packets[0].SequenceNumber, packets[1].SequenceNumber)
	}
	wantPayload := make([]byte, len(pcm[320*2:]))
	for index := 0; index < len(wantPayload); index += 2 {
		wantPayload[index] = pcm[320*2+index+1]
		wantPayload[index+1] = pcm[320*2+index]
	}
	if !reflect.DeepEqual(packets[1].Payload, wantPayload) {
		t.Fatalf("retry payload = %v, want unsent PCM tail", packets[1].Payload)
	}
}

func TestPionAudioTrackStopDoesNotWaitForRTPWrite(t *testing.T) {
	recorder := &blockingRTPTrackRecorder{started: make(chan struct{}), release: make(chan struct{})}
	track, err := newPionAudioTrack(recorder, MediaConfig{SampleRate: 16_000, Channels: 1})
	if err != nil {
		t.Fatalf("newPionAudioTrack() error = %v", err)
	}
	chunk := pipeline.AudioChunk{SessionID: "session-1", PlaybackID: "playback-1", SequenceNo: 1, Data: []byte{1, 2}}
	writeDone := make(chan error, 1)
	go func() { writeDone <- track.Write(context.Background(), chunk) }()
	select {
	case <-recorder.started:
	case <-time.After(time.Second):
		t.Fatal("Write() did not reach RTP track")
	}
	stopDone := make(chan error, 1)
	go func() { stopDone <- track.Stop(context.Background(), "playback-1") }()
	select {
	case err := <-stopDone:
		if err != nil {
			t.Fatalf("Stop() error = %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Stop() waited for blocked RTP write")
	}
	close(recorder.release)
	if err := <-writeDone; err != nil {
		t.Fatalf("Write() error = %v", err)
	}
}

func TestPionEventSinkWaitsForOpenAndSendsJSON(t *testing.T) {
	channel := &dataChannelRecorder{state: pion.DataChannelStateConnecting}
	sink := newPionEventSink(channel)
	event := playback.Event{EventID: "event-1", Type: playback.EventStarted, SessionID: "session-1", PlaybackID: "playback-1", SequenceNo: 1, OccurredAt: time.Unix(1700000000, 0).UTC()}
	sent := make(chan error, 1)
	go func() { sent <- sink.Publish(context.Background(), event) }()
	select {
	case err := <-sent:
		t.Fatalf("Publish() returned before open: %v", err)
	case <-time.After(10 * time.Millisecond):
	}
	channel.Open()
	if err := <-sent; err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if len(channel.Messages()) != 1 || !contains(channel.Messages()[0], `"event_id":"event-1"`) {
		t.Fatalf("messages = %#v", channel.Messages())
	}
}

func TestPionEventSinkSerializesDataChannelWrites(t *testing.T) {
	channel := &blockingDataChannelRecorder{started: make(chan struct{}, 2), release: make(chan struct{})}
	sink := newPionEventSink(channel)
	event := playback.Event{EventID: "event-1", Type: playback.EventStarted, SessionID: "session-1", PlaybackID: "playback-1", SequenceNo: 1}
	firstDone := make(chan error, 1)
	go func() { firstDone <- sink.Publish(context.Background(), event) }()
	select {
	case <-channel.started:
	case <-time.After(time.Second):
		t.Fatal("first event did not reach DataChannel")
	}
	secondDone := make(chan error, 1)
	go func() { secondDone <- sink.Publish(context.Background(), event) }()
	select {
	case <-channel.started:
		t.Fatal("second event wrote while first DataChannel write was blocked")
	case <-time.After(100 * time.Millisecond):
	}
	close(channel.release)
	if err := <-firstDone; err != nil {
		t.Fatalf("first Publish() error = %v", err)
	}
	if err := <-secondDone; err != nil {
		t.Fatalf("second Publish() error = %v", err)
	}
}

func TestPionEventSinkPublishUnblocksWhenTransportCloses(t *testing.T) {
	peer := &mediaPeerRecorder{
		fakePionPeerConnection: &fakePionPeerConnection{gatherComplete: closedChannel()},
		dataChannelState:       pion.DataChannelStateConnecting,
	}
	factory := newFakePionTransportFactory(peer.fakePionPeerConnection)
	factory.newPeerConnection = func(pion.Configuration) (pionPeerConnection, error) { return peer, nil }
	transport, err := factory.Create(context.Background(), "session-1", "rtc_1", nil)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	sink := transport.(*PionTransport).TranslationEvents()
	event := playback.Event{EventID: "event-1", Type: playback.EventStarted, SessionID: "session-1", PlaybackID: "playback-1", SequenceNo: 1}
	publishDone := make(chan error, 1)
	go func() { publishDone <- sink.Publish(context.Background(), event) }()
	select {
	case err := <-publishDone:
		t.Fatalf("Publish() returned before transport close: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	if err := transport.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	select {
	case err := <-publishDone:
		if !errors.Is(err, ErrTransportClosed) {
			t.Fatalf("Publish() after transport close error = %v, want ErrTransportClosed", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Publish() remained blocked after transport close")
	}
}

func TestPionAudioSourceDrainsBufferedFramesAfterClose(t *testing.T) {
	decoder := &fakeRTPDecoder{pcm: []byte{1, 2}}
	source, err := newPionAudioSource(decoder, func() time.Time { return time.Unix(1700000000, 0).UTC() })
	if err != nil {
		t.Fatalf("newPionAudioSource() error = %v", err)
	}
	frames := make([]audio.Frame, 3)
	for index := range frames {
		frames[index], err = audio.NewFrame([]byte{byte(index + 1), 0}, defaultASRSampleRate, time.Unix(int64(index+1), 0).UTC())
		if err != nil {
			t.Fatalf("audio.NewFrame() error = %v", err)
		}
		source.frames <- frames[index]
	}
	source.closeDone()
	for index := range frames {
		frame, readErr := source.ReadFrame(context.Background())
		if readErr != nil {
			t.Fatalf("ReadFrame(%d) error = %v", index, readErr)
		}
		if !reflect.DeepEqual(frame, frames[index]) {
			t.Fatalf("ReadFrame(%d) = %#v, want %#v", index, frame, frames[index])
		}
	}
	if _, err := source.ReadFrame(context.Background()); !errors.Is(err, io.EOF) {
		t.Fatalf("ReadFrame(after drain) error = %v, want io.EOF", err)
	}
}

func TestPionAudioSourceDecodesRemoteRTP(t *testing.T) {
	track := &remoteTrackRecorder{packets: make(chan *rtp.Packet, 1)}
	decoder := &fakeRTPDecoder{pcm: []byte{1, 2, 3, 4}}
	source, err := newPionAudioSource(decoder, func() time.Time { return time.Unix(1700000000, 0).UTC() })
	if err != nil {
		t.Fatalf("newPionAudioSource() error = %v", err)
	}
	if err := source.Attach(track); err != nil {
		t.Fatalf("Attach() error = %v", err)
	}
	track.packets <- &rtp.Packet{Header: rtp.Header{Timestamp: 10}, Payload: []byte{9}}
	frame, err := source.ReadFrame(context.Background())
	if err != nil {
		t.Fatalf("ReadFrame() error = %v", err)
	}
	if !reflect.DeepEqual(frame.PCM, decoder.pcm) || frame.SampleRate != 16_000 {
		t.Fatalf("frame = %#v", frame)
	}
	if err := source.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	close(track.packets)
	if _, err := source.ReadFrame(context.Background()); !errors.Is(err, io.EOF) {
		t.Fatalf("ReadFrame(after close) error = %v", err)
	}
}

func TestOpusDecoderDecodesWebRTCSilencePacket(t *testing.T) {
	decoder, err := NewOpusDecoder()
	if err != nil {
		t.Fatalf("NewOpusDecoder() error = %v", err)
	}
	pcm, err := decoder.Decode([]byte{0xf8, 0xff, 0xfe})
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if len(pcm) == 0 || len(pcm)%2 != 0 {
		t.Fatalf("PCM length = %d", len(pcm))
	}
}

func TestPionFactoryConfiguresMediaWhenPeerSupportsIt(t *testing.T) {
	peer := &mediaPeerRecorder{fakePionPeerConnection: &fakePionPeerConnection{
		answer: pion.SessionDescription{Type: pion.SDPTypeAnswer, SDP: "answer-sdp"}, gatherComplete: closedChannel(),
	}}
	factory := newFakePionTransportFactory(peer.fakePionPeerConnection)
	factory.newPeerConnection = func(pion.Configuration) (pionPeerConnection, error) { return peer, nil }
	transport, err := factory.Create(context.Background(), "session-1", "rtc_1", nil)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	mediaTransport, ok := transport.(*PionTransport)
	if !ok || mediaTransport.AudioSource() == nil || mediaTransport.TranslationEvents() == nil {
		t.Fatalf("media transport = %#v", transport)
	}
	if _, err := transport.Answer(context.Background(), SessionDescription{Type: "offer", SDP: l16OfferSDP(24_000)}); err != nil {
		t.Fatalf("Answer() error = %v", err)
	}
	if mediaTransport.TTSAudioTrack() == nil || mediaTransport.Playback() == nil {
		t.Fatalf("TTS media was not configured after remote offer")
	}
	if len(peer.trackAdds) != 1 || peer.dataChannelLabel != defaultDataChannelLabel {
		t.Fatalf("media setup: tracks=%d label=%q", len(peer.trackAdds), peer.dataChannelLabel)
	}
}

func TestPionTransportAnswerConfiguresTTSTrackOnceConcurrently(t *testing.T) {
	peer := &blockingMediaPeerRecorder{
		mediaPeerRecorder: &mediaPeerRecorder{fakePionPeerConnection: &fakePionPeerConnection{
			answer: pion.SessionDescription{Type: pion.SDPTypeAnswer, SDP: "answer-sdp"}, gatherComplete: closedChannel(),
		}},
		addStarted: make(chan struct{}, 2), release: make(chan struct{}),
	}
	factory := newFakePionTransportFactory(peer.fakePionPeerConnection)
	factory.newPeerConnection = func(pion.Configuration) (pionPeerConnection, error) { return peer, nil }
	transport, err := factory.Create(context.Background(), "session-1", "rtc_1", nil)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	answers := make(chan error, 2)
	answer := func() {
		_, answerErr := transport.Answer(context.Background(), SessionDescription{Type: "offer", SDP: l16OfferSDP(24_000)})
		answers <- answerErr
	}
	go answer()
	select {
	case <-peer.addStarted:
	case <-time.After(time.Second):
		t.Fatal("first Answer() did not register a TTS track")
	}
	go answer()
	select {
	case <-peer.addStarted:
		t.Fatal("second Answer() registered a duplicate TTS track")
	case <-time.After(20 * time.Millisecond):
	}
	close(peer.release)
	for range 2 {
		if err := <-answers; err != nil {
			t.Fatalf("Answer() error = %v", err)
		}
	}
	if got := peer.trackCount(); got != 1 {
		t.Fatalf("TTS track registrations = %d, want 1", got)
	}
}

func TestPionTransportCloseUnblocksAudioSource(t *testing.T) {
	peer := &mediaPeerRecorder{fakePionPeerConnection: &fakePionPeerConnection{gatherComplete: closedChannel()}}
	factory := newFakePionTransportFactory(peer.fakePionPeerConnection)
	factory.newPeerConnection = func(pion.Configuration) (pionPeerConnection, error) { return peer, nil }
	transport, err := factory.Create(context.Background(), "session-1", "rtc_1", nil)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	source := transport.(*PionTransport).AudioSource()
	if err := transport.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := source.ReadFrame(context.Background()); !errors.Is(err, io.EOF) {
		t.Fatalf("ReadFrame() error = %v, want io.EOF", err)
	}
}

func TestMediaConfigRejectsInvalidSampleRateAndChannels(t *testing.T) {
	for _, config := range []MediaConfig{{SampleRate: -1}, {SampleRate: 16_000, Channels: 2}} {
		if _, err := config.normalized(); !errors.Is(err, ErrMediaConfigInvalid) {
			t.Fatalf("normalized(%#v) error = %v, want ErrMediaConfigInvalid", config, err)
		}
	}
}

func TestPionAudioTrackRejectsInvalidChunksAndPendingPlayback(t *testing.T) {
	if _, err := newPionAudioTrack(nil, MediaConfig{}); !errors.Is(err, ErrMediaUnavailable) {
		t.Fatalf("nil track error = %v, want ErrMediaUnavailable", err)
	}
	expected := errors.New("second RTP write failed")
	recorder := &partialFailureRTPTrackRecorder{failAt: 2, err: expected}
	track, err := newPionAudioTrack(recorder, MediaConfig{SampleRate: 16_000, Channels: 1})
	if err != nil {
		t.Fatalf("newPionAudioTrack() error = %v", err)
	}
	for _, chunk := range []pipeline.AudioChunk{{PlaybackID: "", Data: []byte{1, 2}}, {PlaybackID: "p", Data: []byte{1}}} {
		if err := track.Write(context.Background(), chunk); !errors.Is(err, ErrInvalidDependency) {
			t.Fatalf("Write(%#v) error = %v, want ErrInvalidDependency", chunk, err)
		}
	}
	chunk := pipeline.AudioChunk{PlaybackID: "p", SequenceNo: 1, Data: make([]byte, (320+160)*2)}
	if err := track.Write(context.Background(), chunk); !errors.Is(err, expected) {
		t.Fatalf("first Write() error = %v, want %v", err, expected)
	}
	if err := track.Write(context.Background(), pipeline.AudioChunk{PlaybackID: "p", SequenceNo: 2, Data: []byte{1, 2}}); !errors.Is(err, ErrAudioChunkPending) {
		t.Fatalf("overlapping playback Write() error = %v, want ErrAudioChunkPending", err)
	}
	if err := track.Write(context.Background(), chunk); err != nil {
		t.Fatalf("retry Write() error = %v", err)
	}
}

func TestPionEventSinkReportsContextEncodingSendAndTerminalErrors(t *testing.T) {
	var nilSink *PionEventSink
	if err := nilSink.PublishJSON(context.Background(), nil); !errors.Is(err, ErrMediaUnavailable) {
		t.Fatalf("nil sink error = %v, want ErrMediaUnavailable", err)
	}
	channel := &dataChannelRecorder{state: pion.DataChannelStateOpen}
	sink := newPionEventSink(channel)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sink.PublishJSON(canceled, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled PublishJSON() error = %v, want context.Canceled", err)
	}
	if err := sink.PublishJSON(context.Background(), make(chan int)); err == nil || !strings.Contains(err.Error(), "encode translation event") {
		t.Fatalf("unencodable PublishJSON() error = %v", err)
	}
	sink.close(errors.New("transport failed"))
	if err := sink.PublishJSON(context.Background(), nil); !strings.Contains(err.Error(), "transport failed") {
		t.Fatalf("closed PublishJSON() error = %v, want transport failure", err)
	}

	failing := newPionEventSink(&failingDataChannelRecorder{err: errors.New("send failed")})
	if err := failing.PublishJSON(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "send translation event") {
		t.Fatalf("send failure = %v", err)
	}
}

func TestPionAudioSourceSkipsDecodeFailuresAndMaintainsMonotonicCaptureTimes(t *testing.T) {
	track := &remoteTrackRecorder{packets: make(chan *rtp.Packet, 3)}
	decoder := &sequenceRTPDecoder{outputs: [][]byte{nil, {1, 2}, {3, 4}}, errors: []error{errors.New("bad packet"), nil, nil}}
	capturedAt := time.Unix(1700000000, 0).UTC()
	source, err := newPionAudioSource(decoder, func() time.Time { return capturedAt })
	if err != nil {
		t.Fatalf("newPionAudioSource() error = %v", err)
	}
	if err := source.Attach(track); err != nil {
		t.Fatalf("Attach() error = %v", err)
	}
	if err := source.Attach(track); !errors.Is(err, ErrRemoteTrackAttached) {
		t.Fatalf("second Attach() error = %v, want ErrRemoteTrackAttached", err)
	}
	track.packets <- &rtp.Packet{Payload: []byte{1}}
	track.packets <- &rtp.Packet{Payload: []byte{2}}
	track.packets <- &rtp.Packet{Payload: []byte{3}}
	first, err := source.ReadFrame(context.Background())
	if err != nil {
		t.Fatalf("first ReadFrame() error = %v", err)
	}
	second, err := source.ReadFrame(context.Background())
	if err != nil {
		t.Fatalf("second ReadFrame() error = %v", err)
	}
	if !second.CapturedAt.After(first.CapturedAt) {
		t.Fatalf("capture times are not monotonic: %v then %v", first.CapturedAt, second.CapturedAt)
	}
	close(track.packets)
	if _, err := source.ReadFrame(context.Background()); !errors.Is(err, io.EOF) {
		t.Fatalf("terminal ReadFrame() error = %v, want io.EOF", err)
	}
}

func TestPionAudioSourceReturnsRemoteTrackError(t *testing.T) {
	if _, err := newPionAudioSource(nil, nil); !errors.Is(err, ErrDecoderRequired) {
		t.Fatalf("nil decoder error = %v, want ErrDecoderRequired", err)
	}
	trackErr := errors.New("remote read failed")
	track := &errorRemoteTrack{err: trackErr}
	source, err := newPionAudioSource(&fakeRTPDecoder{pcm: []byte{1, 2}}, nil)
	if err != nil {
		t.Fatalf("newPionAudioSource() error = %v", err)
	}
	if err := source.Attach(nil); !errors.Is(err, ErrRemoteTrackRequired) {
		t.Fatalf("nil Attach() error = %v, want ErrRemoteTrackRequired", err)
	}
	if err := source.Attach(track); err != nil {
		t.Fatalf("Attach() error = %v", err)
	}
	if _, err := source.ReadFrame(context.Background()); !errors.Is(err, trackErr) {
		t.Fatalf("ReadFrame() error = %v, want %v", err, trackErr)
	}
}

func contains(value, part string) bool {
	return strings.Contains(value, part)
}

type rtpTrackRecorder struct {
	mu      sync.Mutex
	packets []*rtp.Packet
}

type blockingRTPTrackRecorder struct {
	started chan struct{}
	release chan struct{}
}

type failingRTPTrackRecorder struct{ err error }

type partialFailureRTPTrackRecorder struct {
	mu      sync.Mutex
	packets []*rtp.Packet
	failAt  int
	err     error
	failed  bool
}

func (r *failingRTPTrackRecorder) WriteRTP(*rtp.Packet) error { return r.err }

func (r *partialFailureRTPTrackRecorder) WriteRTP(packet *rtp.Packet) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.failed && len(r.packets)+1 == r.failAt {
		r.failed = true
		return r.err
	}
	r.packets = append(r.packets, packet.Clone())
	return nil
}

func (r *partialFailureRTPTrackRecorder) Packets() []*rtp.Packet {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*rtp.Packet(nil), r.packets...)
}

func (r *blockingRTPTrackRecorder) WriteRTP(*rtp.Packet) error {
	close(r.started)
	<-r.release
	return nil
}

func (r *rtpTrackRecorder) WriteRTP(packet *rtp.Packet) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	copyPacket := packet.Clone()
	r.packets = append(r.packets, copyPacket)
	return nil
}

func (r *rtpTrackRecorder) Packets() []*rtp.Packet {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*rtp.Packet(nil), r.packets...)
}

type dataChannelRecorder struct {
	mu       sync.Mutex
	state    pion.DataChannelState
	onOpen   func()
	messages []string
}

type blockingDataChannelRecorder struct {
	started chan struct{}
	release chan struct{}
}

func (d *blockingDataChannelRecorder) OnOpen(func()) {}
func (*blockingDataChannelRecorder) ReadyState() pion.DataChannelState {
	return pion.DataChannelStateOpen
}
func (d *blockingDataChannelRecorder) SendText(string) error {
	d.started <- struct{}{}
	<-d.release
	return nil
}

func (d *dataChannelRecorder) OnOpen(handler func()) { d.mu.Lock(); d.onOpen = handler; d.mu.Unlock() }
func (d *dataChannelRecorder) ReadyState() pion.DataChannelState {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.state
}
func (d *dataChannelRecorder) SendText(message string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.messages = append(d.messages, message)
	return nil
}
func (d *dataChannelRecorder) Open() {
	d.mu.Lock()
	d.state = pion.DataChannelStateOpen
	handler := d.onOpen
	d.mu.Unlock()
	if handler != nil {
		handler()
	}
}
func (d *dataChannelRecorder) Messages() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.messages...)
}

type fakeRTPDecoder struct{ pcm []byte }

func (d *fakeRTPDecoder) Decode([]byte) ([]byte, error) { return append([]byte(nil), d.pcm...), nil }

type sequenceRTPDecoder struct {
	outputs [][]byte
	errors  []error
	index   int
}

func (d *sequenceRTPDecoder) Decode([]byte) ([]byte, error) {
	index := d.index
	d.index++
	var output []byte
	if index < len(d.outputs) {
		output = d.outputs[index]
	}
	var err error
	if index < len(d.errors) {
		err = d.errors[index]
	}
	return output, err
}

type remoteTrackRecorder struct{ packets chan *rtp.Packet }

func (r *remoteTrackRecorder) ReadRTP() (*rtp.Packet, error) {
	packet, ok := <-r.packets
	if !ok {
		return nil, io.EOF
	}
	return packet, nil
}

type errorRemoteTrack struct{ err error }

func (t *errorRemoteTrack) ReadRTP() (*rtp.Packet, error) { return nil, t.err }

type failingDataChannelRecorder struct{ err error }

func (*failingDataChannelRecorder) OnOpen(func()) {}
func (*failingDataChannelRecorder) ReadyState() pion.DataChannelState {
	return pion.DataChannelStateOpen
}
func (d *failingDataChannelRecorder) SendText(string) error { return d.err }

type mediaPeerRecorder struct {
	*fakePionPeerConnection
	trackAdds        []pion.TrackLocal
	dataChannelLabel string
	dataChannel      *dataChannelRecorder
	dataChannelState pion.DataChannelState
	onTrack          func(pionRemoteTrack)
}

func (p *mediaPeerRecorder) AddTrack(track pion.TrackLocal) (*pion.RTPSender, error) {
	p.trackAdds = append(p.trackAdds, track)
	return nil, nil
}
func (p *mediaPeerRecorder) CreateDataChannel(label string, _ *pion.DataChannelInit) (pionDataChannel, error) {
	p.dataChannelLabel = label
	state := p.dataChannelState
	if state == pion.DataChannelStateUnknown {
		state = pion.DataChannelStateOpen
	}
	p.dataChannel = &dataChannelRecorder{state: state}
	return p.dataChannel, nil
}
func (p *mediaPeerRecorder) OnTrack(handler func(pionRemoteTrack)) { p.onTrack = handler }

var _ pionMediaPeerConnection = (*mediaPeerRecorder)(nil)

type blockingMediaPeerRecorder struct {
	*mediaPeerRecorder
	addStarted chan struct{}
	release    chan struct{}
	mu         sync.Mutex
	adds       int
}

func (p *blockingMediaPeerRecorder) AddTrack(track pion.TrackLocal) (*pion.RTPSender, error) {
	p.mu.Lock()
	p.adds++
	p.mu.Unlock()
	p.addStarted <- struct{}{}
	<-p.release
	return nil, nil
}

func (p *blockingMediaPeerRecorder) trackCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.adds
}

var _ pionMediaPeerConnection = (*blockingMediaPeerRecorder)(nil)
