package localruntime

import (
	"context"
	"errors"
	"strings"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/controlplane"
)

var errSessionIDRequired = errors.New("session_id is required")

// StaticWebRTCConfig returns a session-scoped WebRTC config for browser clients.
type StaticWebRTCConfig struct {
	ICEServers    []controlplane.ICEServer
	TTL           time.Duration
	Now           func() time.Time
	UplinkCodec   string
	DownlinkCodec string
	SampleRateHz  int
	Channels      int
}

func (c StaticWebRTCConfig) GetConfig(_ context.Context, sessionID string) (controlplane.WebRTCConfig, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return controlplane.WebRTCConfig{}, errSessionIDRequired
	}
	now := c.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	ttl := c.TTL
	if ttl <= 0 {
		ttl = time.Hour
	}
	servers := c.ICEServers
	if len(servers) == 0 {
		servers = []controlplane.ICEServer{{
			URLs: []string{"stun:stun.l.google.com:19302"},
		}}
	}
	uplink := strings.TrimSpace(c.UplinkCodec)
	if uplink == "" {
		uplink = "opus"
	}
	downlink := strings.TrimSpace(c.DownlinkCodec)
	if downlink == "" {
		downlink = "none"
	}
	sampleRate := c.SampleRateHz
	if sampleRate <= 0 {
		sampleRate = 48000
	}
	channels := c.Channels
	if channels <= 0 {
		channels = 1
	}
	return controlplane.WebRTCConfig{
		SessionID:          sessionID,
		ExpiresAt:          now().Add(ttl),
		ICEServers:         append([]controlplane.ICEServer(nil), servers...),
		ICETransportPolicy: "all",
		DataChannel: controlplane.DataChannelConfig{
			Label:   "translation-events",
			Ordered: true,
		},
		ControlDataChannel: controlplane.ControlDataChannelConfig{
			Label:           realtimev1.ControlDataChannelLabel,
			Ordered:         true,
			ProtocolVersion: realtimev1.ControlProtocolVersion,
		},
		Audio: controlplane.AudioConfig{
			UplinkCodec:   uplink,
			DownlinkCodec: downlink,
			SampleRateHz:  sampleRate,
			Channels:      channels,
		},
	}, nil
}
