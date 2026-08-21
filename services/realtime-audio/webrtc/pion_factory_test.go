package webrtc

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
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

func TestValidateTTSAudioOfferAcceptsOpusWhenConfigured(t *testing.T) {
	config, err := (MediaConfig{DownlinkCodec: "opus"}).normalized()
	if err != nil {
		t.Fatalf("MediaConfig.normalized() error = %v", err)
	}
	if err := validateTTSAudioOffer(opusOfferSDP(), config); err != nil {
		t.Fatalf("validateTTSAudioOffer(opus) error = %v", err)
	}
	if err := validateTTSAudioOffer(l16OfferSDP(24_000), config); !errors.Is(err, ErrTTSCodecUnsupported) {
		t.Fatalf("validateTTSAudioOffer(L16) error = %v, want ErrTTSCodecUnsupported", err)
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

func TestPionTransportRecoversConnectionStateWithoutRebuildingPeer(t *testing.T) {
	fake := &fakePionPeerConnection{gatherComplete: closedChannel()}
	factory := newFakePionTransportFactory(fake)
	created := 0
	factory.newPeerConnection = func(pion.Configuration) (pionPeerConnection, error) {
		created++
		return fake, nil
	}
	states := make(chan realtimev1.ConnectionState, 3)
	transport, err := factory.Create(context.Background(), "session-1", "rtc_1", func(state realtimev1.ConnectionState, _ time.Time) {
		states <- state
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	for _, state := range []pion.PeerConnectionState{
		pion.PeerConnectionStateConnected,
		pion.PeerConnectionStateDisconnected,
		pion.PeerConnectionStateConnected,
	} {
		fake.triggerState(state)
	}
	for _, want := range []realtimev1.ConnectionState{
		realtimev1.ConnectionConnected,
		realtimev1.ConnectionDisconnected,
		realtimev1.ConnectionConnected,
	} {
		select {
		case got := <-states:
			if got != want {
				t.Fatalf("state = %q, want %q", got, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for state %q", want)
		}
	}
	if created != 1 || transport.(*PionTransport).peerConnection != fake || fake.closeCalls != 0 {
		t.Fatalf("peer rebuilt or closed: creates=%d close_calls=%d", created, fake.closeCalls)
	}
}

func TestPionTransportFactoryCreateValidatesInputsAndFactoryFailures(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	buildErr := errors.New("build peer failed")
	tests := []struct {
		name    string
		factory *PionTransportFactory
		ctx     context.Context
		session string
		id      string
		want    error
	}{
		{name: "canceled context", factory: newFakePionTransportFactory(&fakePionPeerConnection{}), ctx: canceled, session: "session-1", id: "rtc-1", want: context.Canceled},
		{name: "missing session", factory: newFakePionTransportFactory(&fakePionPeerConnection{}), ctx: context.Background(), id: "rtc-1", want: ErrSessionIDRequired},
		{name: "missing connection", factory: newFakePionTransportFactory(&fakePionPeerConnection{}), ctx: context.Background(), session: "session-1", want: ErrConnectionIDRequired},
		{name: "nil factory", ctx: context.Background(), session: "session-1", id: "rtc-1", want: ErrInvalidDependency},
		{name: "peer creation failure", factory: &PionTransportFactory{newPeerConnection: func(pion.Configuration) (pionPeerConnection, error) { return nil, buildErr }}, ctx: context.Background(), session: "session-1", id: "rtc-1", want: buildErr},
		{name: "nil peer", factory: &PionTransportFactory{newPeerConnection: func(pion.Configuration) (pionPeerConnection, error) { return nil, nil }}, ctx: context.Background(), session: "session-1", id: "rtc-1", want: ErrTransportRequired},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := test.factory.Create(test.ctx, test.session, test.id, nil)
			if !errors.Is(err, test.want) {
				t.Fatalf("Create() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestPionTransportFactoryClosesPeerWhenMediaSetupFails(t *testing.T) {
	peer := &failingMediaPeer{fakePionPeerConnection: &fakePionPeerConnection{}, createChannelErr: errors.New("channel failed")}
	factory := newFakePionTransportFactory(peer.fakePionPeerConnection)
	factory.newPeerConnection = func(pion.Configuration) (pionPeerConnection, error) { return peer, nil }
	if _, err := factory.Create(context.Background(), "session-1", "rtc-1", nil); err == nil || !strings.Contains(err.Error(), "create translation DataChannel") {
		t.Fatalf("Create() error = %v, want wrapped channel creation error", err)
	}
	if peer.closeCalls != 1 {
		t.Fatalf("peer Close() calls = %d, want 1", peer.closeCalls)
	}
}

func TestPionConfigurationRejectsEmptyAndUnsafeServers(t *testing.T) {
	tests := []struct {
		name string
		urls []string
	}{
		{name: "empty URL list"},
		{name: "leading whitespace", urls: []string{" stun:stun.example.test:3478"}},
		{name: "embedded endpoint credentials", urls: []string{"stun:stun.example.test@invalid"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := pionConfiguration(PionTransportConfig{ICEServers: []ICEServerConfig{{URLs: test.urls}}})
			if !errors.Is(err, ErrICEConfigurationInvalid) {
				t.Fatalf("pionConfiguration() error = %v, want ErrICEConfigurationInvalid", err)
			}
		})
	}
}

func TestValidateTTSAudioOfferSkipsDisabledAndNonAudioMedia(t *testing.T) {
	config, err := (MediaConfig{}).normalized()
	if err != nil {
		t.Fatalf("MediaConfig.normalized() error = %v", err)
	}
	tests := []struct {
		name string
		sdp  string
	}{
		{name: "disabled audio", sdp: "v=0\r\no=- 0 0 IN IP4 127.0.0.1\r\ns=-\r\nt=0 0\r\nm=audio 0 UDP/TLS/RTP/SAVPF 118\r\na=rtpmap:118 L16/24000/1\r\n"},
		{name: "video advertises L16", sdp: "v=0\r\no=- 0 0 IN IP4 127.0.0.1\r\ns=-\r\nt=0 0\r\nm=video 9 UDP/TLS/RTP/SAVPF 118\r\na=rtpmap:118 L16/24000/1\r\n"},
		{name: "wrong channels", sdp: "v=0\r\no=- 0 0 IN IP4 127.0.0.1\r\ns=-\r\nt=0 0\r\nm=audio 9 UDP/TLS/RTP/SAVPF 118\r\na=rtpmap:118 L16/24000/2\r\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := validateTTSAudioOffer(test.sdp, config); !errors.Is(err, ErrTTSCodecUnsupported) {
				t.Fatalf("validateTTSAudioOffer() error = %v, want ErrTTSCodecUnsupported", err)
			}
		})
	}
}

func TestValidateTTSAudioOfferContinuesPastInvalidFormats(t *testing.T) {
	config, err := (MediaConfig{}).normalized()
	if err != nil {
		t.Fatalf("MediaConfig.normalized() error = %v", err)
	}
	sdp := "v=0\r\n" +
		"o=- 0 0 IN IP4 127.0.0.1\r\n" +
		"s=-\r\n" +
		"t=0 0\r\n" +
		"m=audio 9 UDP/TLS/RTP/SAVPF bad 117 118\r\n" +
		"a=rtpmap:117 opus/48000/2\r\n" +
		"a=rtpmap:118 L16/24000/1\r\n"
	if err := validateTTSAudioOffer(sdp, config); err != nil {
		t.Fatalf("validateTTSAudioOffer() error = %v", err)
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

type failingMediaPeer struct {
	*fakePionPeerConnection
	createChannelErr error
}

func (*failingMediaPeer) AddTrack(pion.TrackLocal) (*pion.RTPSender, error) { return nil, nil }

func (p *failingMediaPeer) CreateDataChannel(string, *pion.DataChannelInit) (pionDataChannel, error) {
	return nil, p.createChannelErr
}

func (*failingMediaPeer) OnTrack(func(pionRemoteTrack)) {}

var _ pionMediaPeerConnection = (*failingMediaPeer)(nil)

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
