package realtimeaccess

import (
	"context"

	"github.com/1024XEngineer/xe6-tsy/services/api/sessions"
)

// DeferredRealtime keeps sessions Create/List/Get usable when the realtime
// control-plane URL is not configured. Start and Stop stay fail-closed with
// ErrNotImplemented. sessions.Service.End only calls Stop for active sessions,
// so ending a still-created session succeeds without realtime; ending an
// active session surfaces ErrNotImplemented until the control-plane client is
// wired. Missing runtime snapshots synthesize stopped for created/ended/failed
// business states.
type DeferredRealtime struct{}

func (DeferredRealtime) Start(context.Context, sessions.StartRealtimeCommand) (sessions.RuntimeSnapshot, error) {
	return sessions.RuntimeSnapshot{}, sessions.ErrNotImplemented
}

func (DeferredRealtime) Stop(context.Context, sessions.StopRealtimeCommand) (sessions.RuntimeSnapshot, error) {
	return sessions.RuntimeSnapshot{}, sessions.ErrNotImplemented
}

func (DeferredRealtime) GetRuntimeState(context.Context, string) (sessions.RuntimeSnapshot, error) {
	return sessions.RuntimeSnapshot{}, sessions.ErrRuntimeSnapshotNotFound
}

// DeferredWebRTCConnection fails start readiness until a realtime control-plane
// client is wired. Create and list paths do not call this reader.
type DeferredWebRTCConnection struct{}

func (DeferredWebRTCConnection) GetConnectionState(
	context.Context,
	string,
) (sessions.WebRTCConnectionSnapshot, error) {
	return sessions.WebRTCConnectionSnapshot{}, sessions.ErrNotImplemented
}

var (
	_ sessions.RealtimeLifecycle      = DeferredRealtime{}
	_ sessions.WebRTCConnectionReader = DeferredWebRTCConnection{}
)
