package localruntime

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/controlplane"
)

var errSessionIDRequired = errors.New("session_id is required")

// StaticWebRTCConfig returns a session-scoped WebRTC config for browser clients.
type StaticWebRTCConfig struct {
	ICEServers []controlplane.ICEServer
	TTL        time.Duration
	Now        func() time.Time
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
	return controlplane.WebRTCConfig{
		SessionID:          sessionID,
		ExpiresAt:          now().Add(ttl),
		ICEServers:         append([]controlplane.ICEServer(nil), servers...),
		ICETransportPolicy: "all",
		DataChannel: controlplane.DataChannelConfig{
			Label:   "translation-events",
			Ordered: true,
		},
		Audio: controlplane.AudioConfig{
			UplinkCodec:   "opus",
			DownlinkCodec: "opus",
			SampleRateHz:  48000,
			Channels:      1,
		},
	}, nil
}
