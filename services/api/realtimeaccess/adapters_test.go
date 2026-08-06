package realtimeaccess

import (
	"context"
	"errors"
	"testing"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	"github.com/1024XEngineer/xe6-tsy/services/api/languages"
	"github.com/1024XEngineer/xe6-tsy/services/api/sessions"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/controlplane"
)

func TestLanguageConfigReaderMapsActivePairs(t *testing.T) {
	reader, err := NewLanguageConfigReader(languageReaderFake{snapshot: languages.LanguageConfigSnapshot{
		SessionID: "session-1",
		Version:   1,
		LanguagePairs: []languages.LanguagePair{
			{Source: "en-US", Target: "zh-CN"},
			{Source: "zh-CN", Target: "en-US"},
		},
		Status: languages.StatusActive,
	}})
	if err != nil {
		t.Fatalf("NewLanguageConfigReader() error = %v", err)
	}

	snapshot, err := reader.GetCurrentConfig(t.Context(), "session-1")
	if err != nil {
		t.Fatalf("GetCurrentConfig() error = %v", err)
	}
	if snapshot.SessionID != "session-1" ||
		snapshot.Version != 1 ||
		snapshot.LanguagePairCount != 2 ||
		snapshot.Status != sessions.LanguageConfigActive {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestLanguageConfigReaderMapsNotReady(t *testing.T) {
	reader, err := NewLanguageConfigReader(languageReaderFake{err: languages.ErrNoActiveConfig})
	if err != nil {
		t.Fatalf("NewLanguageConfigReader() error = %v", err)
	}
	_, err = reader.GetCurrentConfig(t.Context(), "session-1")
	if !errors.Is(err, sessions.ErrLanguageConfigNotReady) {
		t.Fatalf("GetCurrentConfig() error = %v, want ErrLanguageConfigNotReady", err)
	}

	reader, err = NewLanguageConfigReader(languageReaderFake{snapshot: languages.LanguageConfigSnapshot{
		SessionID: "session-1",
		Status:    "draft",
	}})
	if err != nil {
		t.Fatalf("NewLanguageConfigReader() error = %v", err)
	}
	_, err = reader.GetCurrentConfig(t.Context(), "session-1")
	if !errors.Is(err, sessions.ErrLanguageConfigNotReady) {
		t.Fatalf("unknown status error = %v, want ErrLanguageConfigNotReady", err)
	}
}

func TestWebRTCConnectionReaderMapsStatesAndErrors(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	for _, test := range []struct {
		name string
		in   realtimev1.ConnectionState
		want sessions.ConnectionState
	}{
		{name: "new", in: realtimev1.ConnectionNew, want: sessions.ConnectionNew},
		{name: "connecting", in: realtimev1.ConnectionConnecting, want: sessions.ConnectionConnecting},
		{name: "connected", in: realtimev1.ConnectionConnected, want: sessions.ConnectionConnected},
		{name: "disconnected", in: realtimev1.ConnectionDisconnected, want: sessions.ConnectionDisconnected},
		{name: "failed", in: realtimev1.ConnectionFailed, want: sessions.ConnectionFailed},
		{name: "closed", in: realtimev1.ConnectionClosed, want: sessions.ConnectionClosed},
	} {
		t.Run(test.name, func(t *testing.T) {
			reader, err := NewWebRTCConnectionReader(connectionClientFake{
				snapshot: realtimev1.ConnectionSnapshot{
					SessionID: "session-1", ConnectionID: "connection-1",
					State: test.in, Version: 1, UpdatedAt: now,
				},
			})
			if err != nil {
				t.Fatalf("NewWebRTCConnectionReader() error = %v", err)
			}
			snapshot, err := reader.GetConnectionState(t.Context(), "session-1")
			if err != nil {
				t.Fatalf("GetConnectionState() error = %v", err)
			}
			if snapshot.ConnectionState != test.want {
				t.Fatalf("ConnectionState = %q, want %q", snapshot.ConnectionState, test.want)
			}
		})
	}

	reader, err := NewWebRTCConnectionReader(connectionClientFake{err: controlplane.ErrConnectionNotFound})
	if err != nil {
		t.Fatalf("NewWebRTCConnectionReader() error = %v", err)
	}
	_, err = reader.GetConnectionState(t.Context(), "session-1")
	if !errors.Is(err, sessions.ErrWebRTCNotReady) {
		t.Fatalf("not found error = %v, want ErrWebRTCNotReady", err)
	}
}

func TestRealtimeLifecycleMapsCommandsSnapshotsAndErrors(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	client := &lifecycleClientFake{runtime: realtimev1.RuntimeSnapshot{
		SessionID: "session-1", StartOperationID: "operation-1",
		RuntimeState: realtimev1.RuntimeListening, UpdatedAt: now,
	}}
	lifecycle, err := NewRealtimeLifecycle(client)
	if err != nil {
		t.Fatalf("NewRealtimeLifecycle() error = %v", err)
	}

	started, err := lifecycle.Start(t.Context(), sessions.StartRealtimeCommand{
		SessionID: "session-1", OperationID: "operation-1",
		TraceID: "trace-start", StartedBy: "account-1",
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if started.StartOperationID != "operation-1" ||
		started.RuntimeState != sessions.RuntimeListening ||
		client.startRequest.OperationID != "operation-1" ||
		client.startRequest.TraceID != "trace-start" ||
		client.startRequest.StartedBy != "account-1" {
		t.Fatalf("Start() snapshot=%#v request=%#v", started, client.startRequest)
	}

	endedAt := now.Add(time.Minute)
	stopped, err := lifecycle.Stop(t.Context(), sessions.StopRealtimeCommand{
		SessionID: "session-1",
		TraceID:   "trace-stop",
		Reason:    sessions.EndReasonUserRequested,
		EndedAt:   endedAt,
	})
	if err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if stopped.RuntimeState != sessions.RuntimeListening ||
		client.stopRequest.Reason != "user_requested" ||
		!client.stopRequest.EndedAt.Equal(endedAt) {
		t.Fatalf("Stop() snapshot=%#v request=%#v", stopped, client.stopRequest)
	}

	client.startErr = controlplane.ErrRuntimeOperationConflict
	_, err = lifecycle.Start(t.Context(), sessions.StartRealtimeCommand{
		SessionID: "session-1", OperationID: "operation-2",
	})
	if !errors.Is(err, sessions.ErrRealtimeAlreadyRunning) {
		t.Fatalf("Start(conflict) error = %v, want ErrRealtimeAlreadyRunning", err)
	}

	client.runtimeErr = controlplane.ErrRuntimeNotFound
	_, err = lifecycle.GetRuntimeState(t.Context(), "session-1")
	if !errors.Is(err, sessions.ErrRuntimeSnapshotNotFound) {
		t.Fatalf("GetRuntimeState() error = %v, want ErrRuntimeSnapshotNotFound", err)
	}
}

func TestRealtimeLifecycleRejectsUnknownMappings(t *testing.T) {
	lifecycle, err := NewRealtimeLifecycle(&lifecycleClientFake{runtime: realtimev1.RuntimeSnapshot{
		SessionID: "session-1", RuntimeState: "unknown",
		UpdatedAt: time.Unix(1700000000, 0).UTC(),
	}})
	if err != nil {
		t.Fatalf("NewRealtimeLifecycle() error = %v", err)
	}
	_, err = lifecycle.GetRuntimeState(t.Context(), "session-1")
	if !errors.Is(err, sessions.ErrRuntimeUnavailable) {
		t.Fatalf("GetRuntimeState() error = %v, want ErrRuntimeUnavailable", err)
	}

	_, err = lifecycle.Stop(t.Context(), sessions.StopRealtimeCommand{
		SessionID: "session-1",
		TraceID:   "trace-stop",
		Reason:    "unknown",
		EndedAt:   time.Unix(1700000000, 0).UTC(),
	})
	if !errors.Is(err, sessions.ErrInvalidRequest) {
		t.Fatalf("Stop(unknown reason) error = %v, want ErrInvalidRequest", err)
	}
}

type languageReaderFake struct {
	snapshot languages.LanguageConfigSnapshot
	err      error
}

func (f languageReaderFake) GetCurrentConfig(
	context.Context,
	string,
) (languages.LanguageConfigSnapshot, error) {
	return f.snapshot, f.err
}

type connectionClientFake struct {
	snapshot realtimev1.ConnectionSnapshot
	err      error
}

func (f connectionClientFake) GetConnection(
	context.Context,
	string,
) (realtimev1.ConnectionSnapshot, error) {
	return f.snapshot, f.err
}

type lifecycleClientFake struct {
	runtime      realtimev1.RuntimeSnapshot
	startErr     error
	stopErr      error
	runtimeErr   error
	startRequest realtimev1.StartRequest
	stopRequest  realtimev1.StopRequest
}

func (f *lifecycleClientFake) Start(
	_ context.Context,
	_ string,
	request realtimev1.StartRequest,
) (realtimev1.RuntimeSnapshot, error) {
	f.startRequest = request
	return f.runtime, f.startErr
}

func (f *lifecycleClientFake) Stop(
	_ context.Context,
	_ string,
	request realtimev1.StopRequest,
) (realtimev1.RuntimeSnapshot, error) {
	f.stopRequest = request
	return f.runtime, f.stopErr
}

func (f *lifecycleClientFake) GetRuntimeState(
	context.Context,
	string,
) (realtimev1.RuntimeSnapshot, error) {
	return f.runtime, f.runtimeErr
}
