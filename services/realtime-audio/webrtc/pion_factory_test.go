package webrtc

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	pion "github.com/pion/webrtc/v4"
)

func TestNewPionTransportFactoryValidatesAndMapsICEServers(t *testing.T) {
	factory, err := NewPionTransportFactory(PionTransportConfig{
		ICEServers: []ICEServerConfig{{
			URLs:       []string{"stun:stun.example.test:3478", "turns:turn.example.test:5349?transport=tcp"},
			Username:   "turn-user",
			Credential: "turn-secret",
		}},
	})
	if err != nil {
		t.Fatalf("NewPionTransportFactory() error = %v", err)
	}
	if got, want := factory.configuration.ICEServers, []pion.ICEServer{{
		URLs:       []string{"stun:stun.example.test:3478", "turns:turn.example.test:5349?transport=tcp"},
		Username:   "turn-user",
		Credential: "turn-secret",
	}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ICE servers = %#v, want %#v", got, want)
	}
}

func TestMediaConfigDefaultsTTSOutputToQwenSampleRate(t *testing.T) {
	config, err := (MediaConfig{}).normalized()
	if err != nil {
		t.Fatalf("MediaConfig.normalized() error = %v", err)
	}
	if config.SampleRate != 24_000 {
		t.Fatalf("default TTS sample rate = %d, want 24000", config.SampleRate)
	}
}

func TestValidateTTSAudioOfferRequiresConfiguredL16Codec(t *testing.T) {
	config, err := (MediaConfig{}).normalized()
	if err != nil {
		t.Fatalf("MediaConfig.normalized() error = %v", err)
	}
	tests := []struct {
		name string
		sdp  string
		want error
	}{
		{name: "matching L16", sdp: l16OfferSDP(24_000), want: nil},
		{name: "opus only", sdp: opusOfferSDP(), want: ErrTTSCodecUnsupported},
		{name: "no audio", sdp: videoOnlyOfferSDP(), want: ErrTTSCodecUnsupported},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateTTSAudioOffer(test.sdp, config)
			if !errors.Is(err, test.want) {
				t.Fatalf("validateTTSAudioOffer() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestPionTransportSkipsTTSCodecCheckWhenConfigured(t *testing.T) {
	config, err := (MediaConfig{SkipTTSTrack: true}).normalized()
	if err != nil {
		t.Fatalf("MediaConfig.normalized() error = %v", err)
	}
	transport := &PionTransport{
		mediaConnection: &mediaPeerRecorder{},
		mediaConfig:     config,
	}
	if err := transport.validateTTSAudioOffer(opusOfferSDP()); err != nil {
		t.Fatalf("transport.validateTTSAudioOffer() error = %v", err)
	}
	if !transport.skipTTSTrack() {
		t.Fatal("skipTTSTrack() = false")
	}
}

func TestPionTransportRejectsOfferWithoutTTSCodecBeforeAddingTrack(t *testing.T) {
	peer := &mediaPeerRecorder{fakePionPeerConnection: &fakePionPeerConnection{
		answer: pion.SessionDescription{Type: pion.SDPTypeAnswer, SDP: "answer-sdp"}, gatherComplete: closedChannel(),
	}}
	factory := newFakePionTransportFactory(peer.fakePionPeerConnection)
	factory.newPeerConnection = func(pion.Configuration) (pionPeerConnection, error) { return peer, nil }
	transport, err := factory.Create(context.Background(), "session-1", "rtc_1", nil)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	_, err = transport.Answer(context.Background(), SessionDescription{Type: "offer", SDP: opusOfferSDP()})
	if !errors.Is(err, ErrTTSCodecUnsupported) {
		t.Fatalf("Answer() error = %v, want ErrTTSCodecUnsupported", err)
	}
	if len(peer.trackAdds) != 0 {
		t.Fatalf("TTS track registrations = %d, want 0", len(peer.trackAdds))
	}
}

func l16OfferSDP(sampleRate int) string {
	return "v=0\r\n" +
		"o=- 0 0 IN IP4 127.0.0.1\r\n" +
		"s=-\r\n" +
		"t=0 0\r\n" +
		"m=audio 9 UDP/TLS/RTP/SAVPF 118\r\n" +
		"a=rtpmap:118 L16/" + fmt.Sprint(sampleRate) + "/1\r\n"
}

func opusOfferSDP() string {
	return "v=0\r\n" +
		"o=- 0 0 IN IP4 127.0.0.1\r\n" +
		"s=-\r\n" +
		"t=0 0\r\n" +
		"m=audio 9 UDP/TLS/RTP/SAVPF 111\r\n" +
		"a=rtpmap:111 opus/48000/2\r\n"
}

func videoOnlyOfferSDP() string {
	return "v=0\r\n" +
		"o=- 0 0 IN IP4 127.0.0.1\r\n" +
		"s=-\r\n" +
		"t=0 0\r\n" +
		"m=video 9 UDP/TLS/RTP/SAVPF 96\r\n" +
		"a=rtpmap:96 VP8/90000\r\n"
}

func TestNewPionTransportFactoryRejectsUnsafeICEServerURL(t *testing.T) {
	for _, rawURL := range []string{"https://example.test/ice", "turn:", "://missing-scheme", "stun://user:password@example.test"} {
		t.Run(rawURL, func(t *testing.T) {
			_, err := NewPionTransportFactory(PionTransportConfig{
				ICEServers: []ICEServerConfig{{URLs: []string{rawURL}}},
			})
			if !errors.Is(err, ErrICEConfigurationInvalid) {
				t.Fatalf("error = %v, want ErrICEConfigurationInvalid", err)
			}
		})
	}
}

func TestMapPionConnectionState(t *testing.T) {
	tests := []struct {
		state pion.PeerConnectionState
		want  realtimev1.ConnectionState
		ok    bool
	}{
		{state: pion.PeerConnectionStateNew, want: realtimev1.ConnectionNew, ok: true},
		{state: pion.PeerConnectionStateConnecting, want: realtimev1.ConnectionConnecting, ok: true},
		{state: pion.PeerConnectionStateConnected, want: realtimev1.ConnectionConnected, ok: true},
		{state: pion.PeerConnectionStateDisconnected, want: realtimev1.ConnectionDisconnected, ok: true},
		{state: pion.PeerConnectionStateFailed, want: realtimev1.ConnectionFailed, ok: true},
		{state: pion.PeerConnectionStateClosed, want: realtimev1.ConnectionClosed, ok: true},
		{state: pion.PeerConnectionStateUnknown, ok: false},
	}
	for _, test := range tests {
		got, ok := mapPionConnectionState(test.state)
		if got != test.want || ok != test.ok {
			t.Errorf("mapPionConnectionState(%q) = %q, %t; want %q, %t", test.state, got, ok, test.want, test.ok)
		}
	}
}

func TestPionTransportMapsConnectionStateCallback(t *testing.T) {
	fake := &fakePionPeerConnection{gatherComplete: closedChannel()}
	fixedNow := time.Unix(1700000000, 0).UTC()
	factory := newFakePionTransportFactory(fake)
	factory.now = func() time.Time { return fixedNow }
	updates := make(chan transportStateUpdate, 1)
	if _, err := factory.Create(context.Background(), "session-1", "rtc_1", func(state realtimev1.ConnectionState, updatedAt time.Time) {
		updates <- transportStateUpdate{state: state, updatedAt: updatedAt}
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	fake.triggerState(pion.PeerConnectionStateConnected)
	select {
	case update := <-updates:
		if update.state != realtimev1.ConnectionConnected || !update.updatedAt.Equal(fixedNow) {
			t.Fatalf("state update = %#v", update)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for connection state callback")
	}
}

func newFakePionTransportFactory(fake *fakePionPeerConnection) *PionTransportFactory {
	return &PionTransportFactory{
		newPeerConnection: func(pion.Configuration) (pionPeerConnection, error) { return fake, nil },
		now:               func() time.Time { return time.Now().UTC() },
	}
}

func closedChannel() <-chan struct{} {
	channel := make(chan struct{})
	close(channel)
	return channel
}

type fakePionPeerConnection struct {
	mu                sync.Mutex
	remoteDescription pion.SessionDescription
	answer            pion.SessionDescription
	localDescription  *pion.SessionDescription
	gatherComplete    <-chan struct{}
	localSet          chan struct{}
	calls             []string
	candidates        []pion.ICECandidateInit
	closeCalls        int
	stateHandler      func(pion.PeerConnectionState)
}

func (f *fakePionPeerConnection) SetRemoteDescription(description pion.SessionDescription) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "remote")
	f.remoteDescription = description
	return nil
}

func (f *fakePionPeerConnection) GatheringComplete() <-chan struct{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "gather")
	return f.gatherComplete
}

func (f *fakePionPeerConnection) CreateAnswer(*pion.AnswerOptions) (pion.SessionDescription, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "create-answer")
	return f.answer, nil
}

func (f *fakePionPeerConnection) SetLocalDescription(description pion.SessionDescription) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "local")
	f.localDescription = &description
	if f.localSet != nil {
		close(f.localSet)
		f.localSet = nil
	}
	return nil
}

func (f *fakePionPeerConnection) LocalDescription() *pion.SessionDescription {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "description")
	return f.localDescription
}

func (f *fakePionPeerConnection) AddICECandidate(candidate pion.ICECandidateInit) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.candidates = append(f.candidates, candidate)
	return nil
}

func (f *fakePionPeerConnection) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closeCalls++
	return nil
}

func (f *fakePionPeerConnection) OnConnectionStateChange(handler func(pion.PeerConnectionState)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stateHandler = handler
}

func (f *fakePionPeerConnection) triggerState(state pion.PeerConnectionState) {
	f.mu.Lock()
	handler := f.stateHandler
	f.mu.Unlock()
	if handler != nil {
		handler(state)
	}
}

var _ pionPeerConnection = (*fakePionPeerConnection)(nil)
