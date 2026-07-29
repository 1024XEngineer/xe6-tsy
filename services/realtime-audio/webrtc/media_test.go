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

func TestPionFactoryConfiguresMediaWhenPeerSupportsIt(t *testing.T) {
	peer := &mediaPeerRecorder{fakePionPeerConnection: &fakePionPeerConnection{gatherComplete: closedChannel()}}
	factory := newFakePionTransportFactory(peer.fakePionPeerConnection)
	factory.newPeerConnection = func(pion.Configuration) (pionPeerConnection, error) { return peer, nil }
	transport, err := factory.Create(context.Background(), "session-1", "rtc_1", nil)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	mediaTransport, ok := transport.(*PionTransport)
	if !ok || mediaTransport.AudioSource() == nil || mediaTransport.TTSAudioTrack() == nil || mediaTransport.TranslationEvents() == nil {
		t.Fatalf("media transport = %#v", transport)
	}
	if len(peer.trackAdds) != 1 || peer.dataChannelLabel != defaultDataChannel {
		t.Fatalf("media setup: tracks=%d label=%q", len(peer.trackAdds), peer.dataChannelLabel)
	}
}

func contains(value, part string) bool {
	return strings.Contains(value, part)
}

type rtpTrackRecorder struct {
	mu      sync.Mutex
	packets []*rtp.Packet
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

type remoteTrackRecorder struct{ packets chan *rtp.Packet }

func (r *remoteTrackRecorder) ReadRTP() (*rtp.Packet, error) {
	packet, ok := <-r.packets
	if !ok {
		return nil, io.EOF
	}
	return packet, nil
}

type mediaPeerRecorder struct {
	*fakePionPeerConnection
	trackAdds        []pion.TrackLocal
	dataChannelLabel string
	dataChannel      *dataChannelRecorder
	onTrack          func(pionRemoteTrack)
}

func (p *mediaPeerRecorder) AddTrack(track pion.TrackLocal) (*pion.RTPSender, error) {
	p.trackAdds = append(p.trackAdds, track)
	return nil, nil
}
func (p *mediaPeerRecorder) CreateDataChannel(label string, _ *pion.DataChannelInit) (pionDataChannel, error) {
	p.dataChannelLabel = label
	p.dataChannel = &dataChannelRecorder{state: pion.DataChannelStateOpen}
	return p.dataChannel, nil
}
func (p *mediaPeerRecorder) OnTrack(handler func(pionRemoteTrack)) { p.onTrack = handler }

var _ pionMediaPeerConnection = (*mediaPeerRecorder)(nil)
