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

	boom := errors.New("connection boom")
	for _, test := range []struct {
		name string
		err  error
		want error
	}{
		{name: "request", err: controlplane.ErrClientRequest, want: sessions.ErrInvalidRequest},
		{name: "unauthorized", err: controlplane.ErrClientUnauthorized, want: sessions.ErrUnauthorized},
		{name: "dependency", err: controlplane.ErrClientDependency, want: sessions.ErrInvalidDependency},
		{name: "canceled", err: context.Canceled, want: context.Canceled},
		{name: "deadline", err: context.DeadlineExceeded, want: context.DeadlineExceeded},
		{name: "unknown", err: boom, want: sessions.ErrWebRTCUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			reader, err := NewWebRTCConnectionReader(connectionClientFake{err: test.err})
			if err != nil {
				t.Fatalf("NewWebRTCConnectionReader() error = %v", err)
			}
			_, err = reader.GetConnectionState(t.Context(), "session-1")
			if !errors.Is(err, test.want) {
				t.Fatalf("GetConnectionState() error = %v, want %v", err, test.want)
			}
			if test.name == "canceled" || test.name == "deadline" || test.name == "unknown" {
				if !errors.Is(err, test.err) {
					t.Fatalf("GetConnectionState() error = %v, want cause %v", err, test.err)
				}
			}
		})
	}

	t.Run("rejects_mismatched_snapshot", func(t *testing.T) {
		reader, err := NewWebRTCConnectionReader(connectionClientFake{snapshot: realtimev1.ConnectionSnapshot{
			SessionID:    "other",
			ConnectionID: "connection-1",
			State:        realtimev1.ConnectionConnected,
			Version:      1,
			UpdatedAt:    now,
		}})
		if err != nil {
			t.Fatalf("NewWebRTCConnectionReader() error = %v", err)
		}
		_, err = reader.GetConnectionState(t.Context(), "session-1")
		if !errors.Is(err, sessions.ErrWebRTCUnavailable) {
			t.Fatalf("GetConnectionState() error = %v, want ErrWebRTCUnavailable", err)
		}
	})

	t.Run("rejects_missing_updated_at", func(t *testing.T) {
		reader, err := NewWebRTCConnectionReader(connectionClientFake{snapshot: realtimev1.ConnectionSnapshot{
			SessionID:    "session-1",
			ConnectionID: "connection-1",
			State:        realtimev1.ConnectionConnected,
			Version:      1,
		}})
		if err != nil {
			t.Fatalf("NewWebRTCConnectionReader() error = %v", err)
		}
		_, err = reader.GetConnectionState(t.Context(), "session-1")
		if !errors.Is(err, sessions.ErrWebRTCUnavailable) {
			t.Fatalf("GetConnectionState() error = %v, want ErrWebRTCUnavailable", err)
		}
	})
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
		InitialMode: realtimev1.ModeAssistant,
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if started.StartOperationID != "operation-1" ||
		started.RuntimeState != sessions.RuntimeListening ||
		client.startRequest.OperationID != "operation-1" ||
		client.startRequest.TraceID != "trace-start" ||
		client.startRequest.StartedBy != "account-1" ||
		client.startRequest.InitialMode != realtimev1.ModeAssistant {
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

	client.runtimeErr = nil
	lastError := "runtime_failed"
	client.runtime = realtimev1.RuntimeSnapshot{
		SessionID:        "session-1",
		StartOperationID: "operation-1",
		RuntimeState:     realtimev1.RuntimeFailed,
		LastErrorCode:    &lastError,
		UpdatedAt:        now,
	}
	snapshot, err := lifecycle.GetRuntimeState(t.Context(), "session-1")
	if err != nil {
		t.Fatalf("GetRuntimeState(runtime failed) error = %v", err)
	}
	if snapshot.RuntimeState != sessions.RuntimeFailed || snapshot.LastErrorCode == nil || *snapshot.LastErrorCode != lastError {
		t.Fatalf("GetRuntimeState(runtime failed) snapshot = %#v", snapshot)
	}

	client.runtime = realtimev1.RuntimeSnapshot{
		SessionID:        "session-1",
		StartOperationID: "operation-1",
		RuntimeState:     realtimev1.RuntimeAssistantProcessing,
		UpdatedAt:        now,
	}
	snapshot, err = lifecycle.GetRuntimeState(t.Context(), "session-1")
	if err != nil {
		t.Fatalf("GetRuntimeState(assistant processing) error = %v", err)
	}
	if snapshot.RuntimeState != sessions.RuntimeAssistantProcessing {
		t.Fatalf("GetRuntimeState(assistant processing) state = %q", snapshot.RuntimeState)
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

func TestRealtimeLifecycleMapsModeCommandsAndSnapshots(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	operationID := "mode-operation-1"
	client := &lifecycleClientFake{
		mode: realtimev1.ModeStateSnapshot{
			SessionID: "session-1", RuntimeInstanceID: "runtime-1",
			ActiveMode: realtimev1.ModeInterpretation, Generation: 1,
			Phase: realtimev1.ModePhaseActive, UpdatedAt: now,
		},
	}
	lifecycle, err := NewRealtimeLifecycle(client)
	if err != nil {
		t.Fatalf("NewRealtimeLifecycle() error = %v", err)
	}
	state, err := lifecycle.GetModeState(t.Context(), "session-1")
	if err != nil {
		t.Fatalf("GetModeState() error = %v", err)
	}
	if state.RuntimeInstanceID != "runtime-1" || state.ActiveMode != realtimev1.ModeInterpretation {
		t.Fatalf("mode state = %#v", state)
	}

	command := sessions.SwitchModeCommand{
		SessionID: "session-1", RuntimeInstanceID: "runtime-1",
		OperationID: operationID, TraceID: "trace-1", ExpectedGeneration: 1,
		TargetMode: realtimev1.ModeAssistant,
	}
	client.modeResult = realtimev1.SwitchModeResult{
		OperationID: operationID,
		Status:      realtimev1.ModeSwitchApplied,
		State: realtimev1.ModeStateSnapshot{
			SessionID: "session-1", RuntimeInstanceID: "runtime-1",
			ActiveMode: realtimev1.ModeAssistant, Generation: 2,
			Phase: realtimev1.ModePhaseActive, LastOperationID: &operationID,
			UpdatedAt: now,
		},
	}
	result, err := lifecycle.SwitchMode(t.Context(), command)
	if err != nil {
		t.Fatalf("SwitchMode() error = %v", err)
	}
	if result.OperationID != operationID || result.State.ActiveMode != realtimev1.ModeAssistant {
		t.Fatalf("mode result = %#v", result)
	}
	if client.modeRequest.SessionID != command.SessionID ||
		client.modeRequest.RuntimeInstanceID != command.RuntimeInstanceID ||
		client.modeRequest.OperationID != command.OperationID ||
		client.modeRequest.TraceID != command.TraceID ||
		client.modeRequest.ExpectedGeneration != command.ExpectedGeneration ||
		client.modeRequest.TargetMode != command.TargetMode {
		t.Fatalf("mode request = %#v", client.modeRequest)
	}
}

func TestRealtimeLifecycleMapsModeErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want error
	}{
		{name: "request", err: controlplane.ErrClientRequest, want: sessions.ErrInvalidRequest},
		{name: "not available", err: controlplane.ErrModeNotAvailable, want: sessions.ErrModeNotAvailable},
		{name: "generation", err: controlplane.ErrModeGenerationConflict, want: sessions.ErrModeGenerationConflict},
		{name: "runtime mismatch", err: controlplane.ErrModeRuntimeInstanceMismatch, want: sessions.ErrModeRuntimeMismatch},
		{name: "operation", err: controlplane.ErrModeOperationConflict, want: sessions.ErrModeOperationConflict},
		{name: "runtime missing", err: controlplane.ErrRuntimeNotFound, want: sessions.ErrModeUnavailable},
		{name: "dependency", err: controlplane.ErrDependencyUnavailable, want: sessions.ErrModeUnavailable},
		{name: "unauthorized dependency", err: controlplane.ErrClientUnauthorized, want: sessions.ErrModeUnavailable},
		{name: "canceled", err: context.Canceled, want: context.Canceled},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &lifecycleClientFake{modeErr: test.err}
			lifecycle, err := NewRealtimeLifecycle(client)
			if err != nil {
				t.Fatalf("NewRealtimeLifecycle() error = %v", err)
			}
			_, err = lifecycle.SwitchMode(t.Context(), validAdapterModeCommand())
			if !errors.Is(err, test.want) {
				t.Fatalf("SwitchMode() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestRealtimeLifecycleRejectsInvalidModeResponses(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	base := realtimev1.ModeStateSnapshot{
		SessionID: "session-1", RuntimeInstanceID: "runtime-1",
		ActiveMode: realtimev1.ModeInterpretation, Generation: 1,
		Phase: realtimev1.ModePhaseActive, UpdatedAt: now,
	}
	for _, test := range []struct {
		name string
		edit func(*realtimev1.ModeStateSnapshot)
	}{
		{name: "session mismatch", edit: func(s *realtimev1.ModeStateSnapshot) { s.SessionID = "other" }},
		{name: "runtime missing", edit: func(s *realtimev1.ModeStateSnapshot) { s.RuntimeInstanceID = "" }},
		{name: "mode invalid", edit: func(s *realtimev1.ModeStateSnapshot) { s.ActiveMode = "future" }},
		{name: "generation invalid", edit: func(s *realtimev1.ModeStateSnapshot) { s.Generation = 0 }},
		{name: "phase invalid", edit: func(s *realtimev1.ModeStateSnapshot) { s.Phase = "pending" }},
		{name: "timestamp missing", edit: func(s *realtimev1.ModeStateSnapshot) { s.UpdatedAt = time.Time{} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			snapshot := base
			test.edit(&snapshot)
			lifecycle, err := NewRealtimeLifecycle(&lifecycleClientFake{mode: snapshot})
			if err != nil {
				t.Fatalf("NewRealtimeLifecycle() error = %v", err)
			}
			_, err = lifecycle.GetModeState(t.Context(), "session-1")
			if !errors.Is(err, sessions.ErrModeUnavailable) {
				t.Fatalf("GetModeState() error = %v, want ErrModeUnavailable", err)
			}
		})
	}
}

func TestRealtimeLifecycleRejectsInvalidModeSwitchResponses(t *testing.T) {
	command := validAdapterModeCommand()
	base := realtimev1.SwitchModeResult{
		OperationID: command.OperationID,
		Status:      realtimev1.ModeSwitchApplied,
		State: realtimev1.ModeStateSnapshot{
			SessionID:         command.SessionID,
			RuntimeInstanceID: command.RuntimeInstanceID,
			ActiveMode:        command.TargetMode,
			Generation:        command.ExpectedGeneration + 1,
			Phase:             realtimev1.ModePhaseActive,
			LastOperationID:   &command.OperationID,
			UpdatedAt:         time.Unix(1700000000, 0).UTC(),
		},
	}
	for _, test := range []struct {
		name string
		edit func(*realtimev1.SwitchModeResult)
	}{
		{name: "operation mismatch", edit: func(result *realtimev1.SwitchModeResult) { result.OperationID = "other" }},
		{name: "last operation missing", edit: func(result *realtimev1.SwitchModeResult) { result.State.LastOperationID = nil }},
		{name: "last operation mismatch", edit: func(result *realtimev1.SwitchModeResult) {
			other := "other"
			result.State.LastOperationID = &other
		}},
		{name: "runtime mismatch", edit: func(result *realtimev1.SwitchModeResult) { result.State.RuntimeInstanceID = "other" }},
		{name: "target mismatch", edit: func(result *realtimev1.SwitchModeResult) { result.State.ActiveMode = realtimev1.ModeInterpretation }},
		{name: "generation mismatch", edit: func(result *realtimev1.SwitchModeResult) { result.State.Generation = command.ExpectedGeneration }},
		{name: "phase switching", edit: func(result *realtimev1.SwitchModeResult) { result.State.Phase = realtimev1.ModePhaseSwitching }},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := base
			test.edit(&result)
			lifecycle, err := NewRealtimeLifecycle(&lifecycleClientFake{modeResult: result})
			if err != nil {
				t.Fatalf("NewRealtimeLifecycle() error = %v", err)
			}
			_, err = lifecycle.SwitchMode(t.Context(), command)
			if !errors.Is(err, sessions.ErrModeUnavailable) {
				t.Fatalf("SwitchMode() error = %v, want ErrModeUnavailable", err)
			}
		})
	}
}

func validAdapterModeCommand() sessions.SwitchModeCommand {
	return sessions.SwitchModeCommand{
		SessionID: "session-1", RuntimeInstanceID: "runtime-1",
		OperationID: "mode-operation-1", TraceID: "trace-1",
		ExpectedGeneration: 1, TargetMode: realtimev1.ModeAssistant,
	}
}

func TestRealtimeLifecycleMapsBoundaryErrors(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	boom := errors.New("boom")

	t.Run("start", func(t *testing.T) {
		for _, test := range []struct {
			name string
			err  error
			want error
		}{
			{name: "request", err: controlplane.ErrClientRequest, want: sessions.ErrInvalidRequest},
			{name: "unauthorized", err: controlplane.ErrClientUnauthorized, want: sessions.ErrUnauthorized},
			{name: "dependency", err: controlplane.ErrClientDependency, want: sessions.ErrInvalidDependency},
			{name: "canceled", err: context.Canceled, want: context.Canceled},
			{name: "deadline", err: context.DeadlineExceeded, want: context.DeadlineExceeded},
			{name: "unknown", err: boom, want: sessions.ErrRealtimeStartFailed},
		} {
			t.Run(test.name, func(t *testing.T) {
				lifecycle, err := NewRealtimeLifecycle(&lifecycleClientFake{
					runtime: realtimev1.RuntimeSnapshot{
						SessionID:        "session-1",
						StartOperationID: "operation-1",
						RuntimeState:     realtimev1.RuntimeListening,
						UpdatedAt:        now,
					},
					startErr: test.err,
				})
				if err != nil {
					t.Fatalf("NewRealtimeLifecycle() error = %v", err)
				}
				_, err = lifecycle.Start(t.Context(), sessions.StartRealtimeCommand{
					SessionID:   "session-1",
					OperationID: "operation-1",
				})
				if !errors.Is(err, test.want) {
					t.Fatalf("Start() error = %v, want %v", err, test.want)
				}
				if test.name == "canceled" || test.name == "deadline" || test.name == "unknown" {
					if !errors.Is(err, test.err) {
						t.Fatalf("Start() error = %v, want cause %v", err, test.err)
					}
				}
			})
		}
	})

	t.Run("stop", func(t *testing.T) {
		for _, test := range []struct {
			name string
			err  error
			want error
		}{
			{name: "request", err: controlplane.ErrClientRequest, want: sessions.ErrInvalidRequest},
			{name: "unauthorized", err: controlplane.ErrClientUnauthorized, want: sessions.ErrUnauthorized},
			{name: "dependency", err: controlplane.ErrClientDependency, want: sessions.ErrInvalidDependency},
			{name: "canceled", err: context.Canceled, want: context.Canceled},
			{name: "deadline", err: context.DeadlineExceeded, want: context.DeadlineExceeded},
			{name: "unknown", err: boom, want: sessions.ErrRealtimeStopFailed},
		} {
			t.Run(test.name, func(t *testing.T) {
				lifecycle, err := NewRealtimeLifecycle(&lifecycleClientFake{
					runtime: realtimev1.RuntimeSnapshot{
						SessionID:        "session-1",
						StartOperationID: "operation-1",
						RuntimeState:     realtimev1.RuntimeListening,
						UpdatedAt:        now,
					},
					stopErr: test.err,
				})
				if err != nil {
					t.Fatalf("NewRealtimeLifecycle() error = %v", err)
				}
				_, err = lifecycle.Stop(t.Context(), sessions.StopRealtimeCommand{
					SessionID: "session-1",
					TraceID:   "trace-stop",
					Reason:    sessions.EndReasonUserRequested,
					EndedAt:   now,
				})
				if !errors.Is(err, test.want) {
					t.Fatalf("Stop() error = %v, want %v", err, test.want)
				}
				if test.name == "canceled" || test.name == "deadline" || test.name == "unknown" {
					if !errors.Is(err, test.err) {
						t.Fatalf("Stop() error = %v, want cause %v", err, test.err)
					}
				}
			})
		}
	})

	t.Run("runtime", func(t *testing.T) {
		for _, test := range []struct {
			name string
			err  error
			want error
		}{
			{name: "not_found", err: controlplane.ErrRuntimeNotFound, want: sessions.ErrRuntimeSnapshotNotFound},
			{name: "request", err: controlplane.ErrClientRequest, want: sessions.ErrInvalidRequest},
			{name: "unauthorized", err: controlplane.ErrClientUnauthorized, want: sessions.ErrUnauthorized},
			{name: "dependency", err: controlplane.ErrClientDependency, want: sessions.ErrInvalidDependency},
			{name: "canceled", err: context.Canceled, want: context.Canceled},
			{name: "deadline", err: context.DeadlineExceeded, want: context.DeadlineExceeded},
			{name: "unknown", err: boom, want: sessions.ErrRuntimeUnavailable},
		} {
			t.Run(test.name, func(t *testing.T) {
				lifecycle, err := NewRealtimeLifecycle(&lifecycleClientFake{
					runtime: realtimev1.RuntimeSnapshot{
						SessionID:        "session-1",
						StartOperationID: "operation-1",
						RuntimeState:     realtimev1.RuntimeListening,
						UpdatedAt:        now,
					},
					runtimeErr: test.err,
				})
				if err != nil {
					t.Fatalf("NewRealtimeLifecycle() error = %v", err)
				}
				_, err = lifecycle.GetRuntimeState(t.Context(), "session-1")
				if !errors.Is(err, test.want) {
					t.Fatalf("GetRuntimeState() error = %v, want %v", err, test.want)
				}
				if test.name == "canceled" || test.name == "deadline" || test.name == "unknown" {
					if !errors.Is(err, test.err) {
						t.Fatalf("GetRuntimeState() error = %v, want cause %v", err, test.err)
					}
				}
			})
		}
	})
}

func TestRealtimeLifecycleRejectsInvalidSnapshots(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	lastError := "runtime_failed"
	lifecycle, err := NewRealtimeLifecycle(&lifecycleClientFake{
		runtime: realtimev1.RuntimeSnapshot{
			SessionID:        "session-1",
			StartOperationID: "operation-1",
			RuntimeState:     realtimev1.RuntimeFailed,
			LastErrorCode:    &lastError,
			UpdatedAt:        now,
		},
	})
	if err != nil {
		t.Fatalf("NewRealtimeLifecycle() error = %v", err)
	}

	snapshot, err := lifecycle.GetRuntimeState(t.Context(), "session-1")
	if err != nil {
		t.Fatalf("GetRuntimeState() error = %v", err)
	}
	if snapshot.RuntimeState != sessions.RuntimeFailed || snapshot.LastErrorCode == nil || *snapshot.LastErrorCode != lastError {
		t.Fatalf("snapshot = %#v", snapshot)
	}

	for _, test := range []struct {
		name    string
		runtime realtimev1.RuntimeSnapshot
	}{
		{
			name: "session_mismatch",
			runtime: realtimev1.RuntimeSnapshot{
				SessionID:        "other",
				StartOperationID: "operation-1",
				RuntimeState:     realtimev1.RuntimeListening,
				UpdatedAt:        now,
			},
		},
		{
			name: "missing_updated_at",
			runtime: realtimev1.RuntimeSnapshot{
				SessionID:        "session-1",
				StartOperationID: "operation-1",
				RuntimeState:     realtimev1.RuntimeListening,
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			lifecycle, err := NewRealtimeLifecycle(&lifecycleClientFake{runtime: test.runtime})
			if err != nil {
				t.Fatalf("NewRealtimeLifecycle() error = %v", err)
			}
			_, err = lifecycle.GetRuntimeState(t.Context(), "session-1")
			if !errors.Is(err, sessions.ErrRuntimeUnavailable) {
				t.Fatalf("GetRuntimeState() error = %v, want ErrRuntimeUnavailable", err)
			}
		})
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
	mode         realtimev1.ModeStateSnapshot
	modeResult   realtimev1.SwitchModeResult
	startErr     error
	stopErr      error
	runtimeErr   error
	modeErr      error
	startRequest realtimev1.StartRequest
	stopRequest  realtimev1.StopRequest
	modeRequest  realtimev1.SwitchModeCommand
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

func (f *lifecycleClientFake) GetModeState(
	context.Context,
	string,
) (realtimev1.ModeStateSnapshot, error) {
	return f.mode, f.modeErr
}

func (f *lifecycleClientFake) SwitchMode(
	_ context.Context,
	_ string,
	command realtimev1.SwitchModeCommand,
) (realtimev1.SwitchModeResult, error) {
	f.modeRequest = command
	return f.modeResult, f.modeErr
}
