package realtimeaccess

import (
	"errors"
	"testing"

	"github.com/1024XEngineer/xe6-tsy/services/api/sessions"
)

func TestDeferredRealtimeFailClosedExceptMissingRuntime(t *testing.T) {
	realtime := DeferredRealtime{}
	if _, err := realtime.Start(t.Context(), sessions.StartRealtimeCommand{}); !errors.Is(err, sessions.ErrNotImplemented) {
		t.Fatalf("Start() error = %v, want ErrNotImplemented", err)
	}
	if _, err := realtime.Stop(t.Context(), sessions.StopRealtimeCommand{}); !errors.Is(err, sessions.ErrNotImplemented) {
		t.Fatalf("Stop() error = %v, want ErrNotImplemented", err)
	}
	if _, err := realtime.GetRuntimeState(t.Context(), "session-1"); !errors.Is(err, sessions.ErrRuntimeSnapshotNotFound) {
		t.Fatalf("GetRuntimeState() error = %v, want ErrRuntimeSnapshotNotFound", err)
	}
}

func TestDeferredWebRTCConnectionNotImplemented(t *testing.T) {
	reader := DeferredWebRTCConnection{}
	if _, err := reader.GetConnectionState(t.Context(), "session-1"); !errors.Is(err, sessions.ErrNotImplemented) {
		t.Fatalf("GetConnectionState() error = %v, want ErrNotImplemented", err)
	}
}
