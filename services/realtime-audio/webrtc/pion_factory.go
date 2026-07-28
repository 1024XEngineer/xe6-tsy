package webrtc

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
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
}

// PionTransportFactory creates one Pion PeerConnection per connection generation.
type PionTransportFactory struct {
	configuration     pion.Configuration
	newPeerConnection func(pion.Configuration) (pionPeerConnection, error)
	now               func() time.Time
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

type pionPeerConnectionAdapter struct {
	*pion.PeerConnection
}

func (p *pionPeerConnectionAdapter) GatheringComplete() <-chan struct{} {
	return pion.GatheringCompletePromise(p.PeerConnection)
}

// NewPionTransportFactory validates config before it can create network resources.
func NewPionTransportFactory(config PionTransportConfig) (*PionTransportFactory, error) {
	configuration, err := pionConfiguration(config)
	if err != nil {
		return nil, err
	}
	return &PionTransportFactory{
		configuration: configuration,
		newPeerConnection: func(configuration pion.Configuration) (pionPeerConnection, error) {
			connection, err := pion.NewPeerConnection(configuration)
			if err != nil {
				return nil, err
			}
			return &pionPeerConnectionAdapter{PeerConnection: connection}, nil
		},
		now: func() time.Time { return time.Now().UTC() },
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
	return &PionTransport{peerConnection: connection}, nil
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
