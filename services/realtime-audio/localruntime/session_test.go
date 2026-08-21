package localruntime

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/controlplane"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/session"
)

func TestTrustSessionReaderReturnsCreatedSnapshot(t *testing.T) {
	snapshot, err := TrustSessionReader{}.GetSession(context.Background(), "vs_1")
	if err != nil {
		t.Fatalf("GetSession() error = %v", err)
	}
	if snapshot.SessionID != "vs_1" || snapshot.Status != "created" || snapshot.AccountID == "" {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestTrustSessionReaderRejectsEmptySessionID(t *testing.T) {
	_, err := TrustSessionReader{}.GetSession(context.Background(), "")
	if !errors.Is(err, session.ErrSessionIDRequired) {
		t.Fatalf("GetSession() error = %v, want ErrSessionIDRequired", err)
	}
}

func TestStaticWebRTCConfigScopesSessionID(t *testing.T) {
	fixed := time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC)
	config, err := (StaticWebRTCConfig{
		TTL: time.Minute,
		Now: func() time.Time { return fixed },
		ICEServers: []controlplane.ICEServer{{
			URLs: []string{"stun:example.test:3478"},
		}},
	}).GetConfig(context.Background(), "vs_1")
	if err != nil {
		t.Fatalf("GetConfig() error = %v", err)
	}
	if config.SessionID != "vs_1" {
		t.Fatalf("SessionID = %q", config.SessionID)
	}
	if !config.ExpiresAt.Equal(fixed.Add(time.Minute)) {
		t.Fatalf("ExpiresAt = %v", config.ExpiresAt)
	}
	if len(config.ICEServers) != 1 || config.DataChannel.Label == "" {
		t.Fatalf("config incomplete: %#v", config)
	}
}

func TestStaticWebRTCConfigRequiresICEServers(t *testing.T) {
	_, err := (StaticWebRTCConfig{}).GetConfig(context.Background(), "vs_2")
	if !errors.Is(err, errICEServersRequired) {
		t.Fatalf("GetConfig() error = %v, want errICEServersRequired", err)
	}
	config, err := (StaticWebRTCConfig{ICEServers: []controlplane.ICEServer{{URLs: []string{"turns:turn.example.test:5349"}}}}).GetConfig(context.Background(), "vs_2")
	if err != nil {
		t.Fatalf("configured GetConfig() error = %v", err)
	}
	if config.Audio.DownlinkCodec != "none" || config.Audio.SampleRateHz != 48000 || config.Audio.Channels != 1 {
		t.Fatalf("audio config = %#v, want none/48000/1", config.Audio)
	}
}

func TestStaticWebRTCConfigRespectsDownlinkCodec(t *testing.T) {
	config, err := (StaticWebRTCConfig{
		ICEServers:    []controlplane.ICEServer{{URLs: []string{"turns:turn.example.test:5349"}}},
		DownlinkCodec: "pcm",
		SampleRateHz:  24000,
	}).GetConfig(context.Background(), "vs_pcm")
	if err != nil {
		t.Fatalf("GetConfig() error = %v", err)
	}
	if config.Audio.DownlinkCodec != "pcm" || config.Audio.SampleRateHz != 24000 {
		t.Fatalf("audio config = %#v, want pcm/24000", config.Audio)
	}
}

func TestStaticWebRTCConfigRejectsEmptySessionID(t *testing.T) {
	_, err := (StaticWebRTCConfig{}).GetConfig(context.Background(), "  ")
	if !errors.Is(err, errSessionIDRequired) {
		t.Fatalf("GetConfig() error = %v, want errSessionIDRequired", err)
	}
}

func TestNoopPipelineLifecycle(t *testing.T) {
	var pipeline NoopPipeline
	if err := pipeline.Start(context.Background(), session.SessionSnapshot{SessionID: "vs_1"}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := pipeline.Stop(context.Background(), "vs_1"); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if !pipeline.PipelineActive("vs_1") {
		t.Fatal("PipelineActive() = false")
	}
}
