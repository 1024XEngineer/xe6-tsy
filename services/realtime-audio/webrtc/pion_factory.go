package webrtc

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/playback"
	"github.com/pion/rtp"
	"github.com/pion/sdp/v3"
	pion "github.com/pion/webrtc/v4"
)

// ICEServerConfig keeps Pion types behind the realtime-audio adapter boundary.
type ICEServerConfig struct {
	URLs       []string
	Username   string
	Credential string
}

// PionTransportConfig supplies the STUN/TURN servers used by new PeerConnections.
type PionTransportConfig struct {
	ICEServers []ICEServerConfig
	Media      MediaConfig
}

// PionTransportFactory creates one Pion PeerConnection per connection generation.
type PionTransportFactory struct {
	configuration     pion.Configuration
	newPeerConnection func(pion.Configuration) (pionPeerConnection, error)
	now               func() time.Time
	media             MediaConfig
}

type pionPeerConnection interface {
	SetRemoteDescription(pion.SessionDescription) error
	CreateAnswer(*pion.AnswerOptions) (pion.SessionDescription, error)
	GatheringComplete() <-chan struct{}
	SetLocalDescription(pion.SessionDescription) error
	LocalDescription() *pion.SessionDescription
	AddICECandidate(pion.ICECandidateInit) error
	OnConnectionStateChange(func(pion.PeerConnectionState))
	Close() error
}

type pionMediaPeerConnection interface {
	pionPeerConnection
	AddTrack(pion.TrackLocal) (*pion.RTPSender, error)
	CreateDataChannel(string, *pion.DataChannelInit) (pionDataChannel, error)
	OnTrack(func(pionRemoteTrack))
}

type pionPeerConnectionAdapter struct {
	*pion.PeerConnection
}

func (p *pionPeerConnectionAdapter) GatheringComplete() <-chan struct{} {
	return pion.GatheringCompletePromise(p.PeerConnection)
}

func (p *pionPeerConnectionAdapter) CreateDataChannel(label string, options *pion.DataChannelInit) (pionDataChannel, error) {
	return p.PeerConnection.CreateDataChannel(label, options)
}

func (p *pionPeerConnectionAdapter) OnTrack(handler func(pionRemoteTrack)) {
	p.PeerConnection.OnTrack(func(track *pion.TrackRemote, _ *pion.RTPReceiver) {
		if handler != nil && track.Kind() == pion.RTPCodecTypeAudio && track.Codec().MimeType == pion.MimeTypeOpus {
			handler(&pionRemoteTrackAdapter{track: track})
		}
	})
}

type pionRemoteTrackAdapter struct {
	track *pion.TrackRemote
}

func (t *pionRemoteTrackAdapter) ReadRTP() (*rtp.Packet, error) {
	packet, _, err := t.track.ReadRTP()
	return packet, err
}

// NewPionTransportFactory validates config before it can create network resources.
func NewPionTransportFactory(config PionTransportConfig) (*PionTransportFactory, error) {
	configuration, err := pionConfiguration(config)
	if err != nil {
		return nil, err
	}
	mediaConfig, err := config.Media.normalized()
	if err != nil {
		return nil, err
	}
	api, err := newPionAPI(mediaConfig)
	if err != nil {
		return nil, err
	}
	return &PionTransportFactory{
		configuration: configuration,
		newPeerConnection: func(configuration pion.Configuration) (pionPeerConnection, error) {
			connection, err := api.NewPeerConnection(configuration)
			if err != nil {
				return nil, err
			}
			return &pionPeerConnectionAdapter{PeerConnection: connection}, nil
		},
		now:   func() time.Time { return time.Now().UTC() },
		media: mediaConfig,
	}, nil
}

// Create allocates a Pion transport and wires transport state into the manager callback.
func (f *PionTransportFactory) Create(
	ctx context.Context,
	sessionID, connectionID string,
	onState ConnectionStateHandler,
) (ConnectionTransport, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch {
	case sessionID == "":
		return nil, ErrSessionIDRequired
	case connectionID == "":
		return nil, ErrConnectionIDRequired
	case f == nil || f.newPeerConnection == nil:
		return nil, ErrInvalidDependency
	}
	connection, err := f.newPeerConnection(f.configuration)
	if err != nil {
		return nil, fmt.Errorf("create Pion PeerConnection: %w", err)
	}
	if connection == nil {
		return nil, ErrTransportRequired
	}
	now := f.now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	connection.OnConnectionStateChange(func(state pion.PeerConnectionState) {
		mapped, ok := mapPionConnectionState(state)
		if !ok || onState == nil {
			return
		}
		onState(mapped, now())
	})
	transport := &PionTransport{peerConnection: connection}
	if mediaConnection, ok := connection.(pionMediaPeerConnection); ok {
		if err := configurePionMedia(transport, mediaConnection, f.media, now); err != nil {
			_ = connection.Close()
			return nil, err
		}
	}
	return transport, nil
}

func configurePionMedia(transport *PionTransport, connection pionMediaPeerConnection, config MediaConfig, now func() time.Time) error {
	if transport == nil || connection == nil {
		return ErrMediaUnavailable
	}
	normalized, err := config.normalized()
	if err != nil {
		return err
	}
	channel, err := connection.CreateDataChannel(normalized.DataChannelLabel, nil)
	if err != nil {
		return fmt.Errorf("create translation DataChannel: %w", err)
	}
	decoder, err := NewOpusDecoder()
	if err != nil {
		return fmt.Errorf("create Opus decoder: %w", err)
	}
	source, err := newPionAudioSource(decoder, now)
	if err != nil {
		return err
	}
	connection.OnTrack(func(track pionRemoteTrack) {
		_ = source.Attach(track)
	})
	transport.mu.Lock()
	transport.audioSource = source
	transport.events = newPionEventSink(channel)
	transport.mediaConnection = connection
	transport.mediaConfig = normalized
	transport.mediaNow = now
	transport.mu.Unlock()
	return nil
}

func configurePionTTSTrack(transport *PionTransport) error {
	if transport == nil {
		return nil
	}
	transport.mediaSetupMu.Lock()
	defer transport.mediaSetupMu.Unlock()
	transport.mu.Lock()
	if transport.closeDone != nil {
		transport.mu.Unlock()
		return ErrTransportClosed
	}
	mediaConnection := transport.mediaConnection
	config := transport.mediaConfig
	mediaNow := transport.mediaNow
	ttsTrack := transport.ttsTrack
	events := transport.events
	alreadyConfigured := transport.playback != nil
	transport.mu.Unlock()
	if mediaConnection == nil || ttsTrack != nil || alreadyConfigured {
		return nil
	}
	var audioTrack playback.AudioTrack
	if strings.EqualFold(config.DownlinkCodec, "opus") {
		track, err := pion.NewTrackLocalStaticSample(pion.RTPCodecCapability{
			MimeType: pion.MimeTypeOpus, ClockRate: 48_000, Channels: 2,
		}, config.TTSTrackID, "realtime-audio")
		if err != nil {
			return fmt.Errorf("create Opus TTS track: %w", err)
		}
		opusTrack, err := newOpusSampleTrack(track, config)
		if err != nil {
			return err
		}
		if _, err := mediaConnection.AddTrack(track); err != nil {
			return fmt.Errorf("add Opus TTS track: %w", err)
		}
		audioTrack = opusTrack
	} else {
		track, err := pion.NewTrackLocalStaticRTP(pion.RTPCodecCapability{
			MimeType: "audio/L16", ClockRate: uint32(config.SampleRate), Channels: uint16(config.Channels),
		}, config.TTSTrackID, "realtime-audio")
		if err != nil {
			return fmt.Errorf("create TTS track: %w", err)
		}
		l16Track, err := newPionAudioTrack(track, config)
		if err != nil {
			return err
		}
		if _, err := mediaConnection.AddTrack(track); err != nil {
			return fmt.Errorf("add TTS track: %w", err)
		}
		audioTrack = l16Track
		transport.mu.Lock()
		transport.ttsTrack = l16Track
		transport.mu.Unlock()
	}
	playbackService, err := playback.NewService(playback.Dependencies{Track: audioTrack, Events: events, Now: mediaNow})
	if err != nil {
		return fmt.Errorf("create playback service: %w", err)
	}
	transport.mu.Lock()
	transport.playback = playbackService
	transport.mu.Unlock()
	return nil
}

func newPionAPI(config MediaConfig) (*pion.API, error) {
	normalized, err := config.normalized()
	if err != nil {
		return nil, err
	}
	mediaEngine := &pion.MediaEngine{}
	if err := mediaEngine.RegisterDefaultCodecs(); err != nil {
		return nil, fmt.Errorf("register default codecs: %w", err)
	}
	if !normalized.SkipTTSTrack && !strings.EqualFold(normalized.DownlinkCodec, "opus") {
		if err := mediaEngine.RegisterCodec(pion.RTPCodecParameters{
			RTPCodecCapability: pion.RTPCodecCapability{
				MimeType: "audio/L16", ClockRate: uint32(normalized.SampleRate), Channels: uint16(normalized.Channels),
			},
			PayloadType: 118,
		}, pion.RTPCodecTypeAudio); err != nil {
			return nil, fmt.Errorf("register TTS codec: %w", err)
		}
	}
	return pion.NewAPI(pion.WithMediaEngine(mediaEngine)), nil
}

func pionConfiguration(config PionTransportConfig) (pion.Configuration, error) {
	configuration := pion.Configuration{ICEServers: make([]pion.ICEServer, 0, len(config.ICEServers))}
	for serverIndex, server := range config.ICEServers {
		if len(server.URLs) == 0 {
			return pion.Configuration{}, fmt.Errorf("%w: server %d has no URLs", ErrICEConfigurationInvalid, serverIndex)
		}
		for _, rawURL := range server.URLs {
			if !validICEServerURL(rawURL) {
				return pion.Configuration{}, fmt.Errorf("%w: server %d URL is invalid", ErrICEConfigurationInvalid, serverIndex)
			}
		}
		configuration.ICEServers = append(configuration.ICEServers, pion.ICEServer{
			URLs: append([]string(nil), server.URLs...), Username: server.Username, Credential: server.Credential,
		})
	}
	return configuration, nil
}

func validICEServerURL(rawURL string) bool {
	if rawURL == "" || strings.TrimSpace(rawURL) != rawURL {
		return false
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	switch strings.ToLower(parsed.Scheme) {
	case "stun", "stuns", "turn", "turns":
	default:
		return false
	}
	if parsed.User != nil {
		return false
	}
	endpoint := parsed.Host
	if endpoint == "" {
		endpoint = parsed.Opaque
	}
	return endpoint != "" && !strings.Contains(endpoint, "@")
}

func mapPionConnectionState(state pion.PeerConnectionState) (realtimev1.ConnectionState, bool) {
	switch state {
	case pion.PeerConnectionStateNew:
		return realtimev1.ConnectionNew, true
	case pion.PeerConnectionStateConnecting:
		return realtimev1.ConnectionConnecting, true
	case pion.PeerConnectionStateConnected:
		return realtimev1.ConnectionConnected, true
	case pion.PeerConnectionStateDisconnected:
		return realtimev1.ConnectionDisconnected, true
	case pion.PeerConnectionStateFailed:
		return realtimev1.ConnectionFailed, true
	case pion.PeerConnectionStateClosed:
		return realtimev1.ConnectionClosed, true
	default:
		return "", false
	}
}

var _ ConnectionTransportFactory = (*PionTransportFactory)(nil)

func validateTTSAudioOffer(rawSDP string, config MediaConfig) error {
	normalized, err := config.normalized()
	if err != nil {
		return err
	}
	wantOpus := strings.EqualFold(normalized.DownlinkCodec, "opus")
	var description sdp.SessionDescription
	if err := description.UnmarshalString(rawSDP); err != nil {
		return fmt.Errorf("parse remote SDP offer: %w", err)
	}
	codecs := description.GetCodecMap()
	for _, media := range description.MediaDescriptions {
		if media == nil || media.MediaName.Media != "audio" || media.MediaName.Port.Value == 0 {
			continue
		}
		for _, format := range media.MediaName.Formats {
			payloadType, err := strconv.ParseUint(format, 10, 8)
			if err != nil {
				continue
			}
			codec, ok := codecs[uint8(payloadType)]
			if !ok {
				continue
			}
			if wantOpus {
				if strings.EqualFold(codec.Name, "opus") {
					return nil
				}
				continue
			}
			if !strings.EqualFold(codec.Name, "L16") || codec.ClockRate != uint32(normalized.SampleRate) {
				continue
			}
			if codec.EncodingParameters == strconv.Itoa(normalized.Channels) {
				return nil
			}
		}
	}
	return ErrTTSCodecUnsupported
}
