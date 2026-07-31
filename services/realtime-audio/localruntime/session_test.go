package localruntime

import (
	"context"
	"testing"
)

func TestTrustSessionReaderReturnsCreatedSnapshot(t *testing.T) {
	snapshot, err := TrustSessionReader{}.GetSession(context.Background(), "vs_1")
	if err != nil {
		t.Fatalf("GetSession() error = %v", err)
	}
	if snapshot.SessionID != "vs_1" || snapshot.Status != "created" {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestStaticWebRTCConfigScopesSessionID(t *testing.T) {
	config, err := (StaticWebRTCConfig{}).GetConfig(context.Background(), "vs_1")
	if err != nil {
		t.Fatalf("GetConfig() error = %v", err)
	}
	if config.SessionID != "vs_1" {
		t.Fatalf("SessionID = %q", config.SessionID)
	}
	if len(config.ICEServers) == 0 || config.DataChannel.Label == "" {
		t.Fatalf("config incomplete: %#v", config)
	}
}
