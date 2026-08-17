package session

import (
	"context"
	"errors"
	"testing"
)

func TestLifecycleReportsPipelineRuntimeProgress(t *testing.T) {
	service := newTestLifecycleService(t, SessionSnapshot{SessionID: "session-1", Status: "created"}, &fakePipeline{}, &fakeConnection{})
	if err := service.deps.Runtimes.Save(context.Background(), RuntimeSnapshot{
		SessionID: "session-1", StartOperationID: "operation-1", RuntimeState: RuntimeListening,
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	turnID := "turn-1"
	playbackID := "playback-1"
	updates := []ProcessingStateUpdate{
		{SessionID: "session-1", RuntimeState: RuntimeASRProcessing, CurrentTurnID: &turnID},
		{SessionID: "session-1", RuntimeState: RuntimeTranslating, CurrentTurnID: &turnID},
		{SessionID: "session-1", RuntimeState: RuntimeTTSProcessing, CurrentTurnID: &turnID, CurrentPlaybackID: &playbackID},
		{SessionID: "session-1", RuntimeState: RuntimePlaying, CurrentTurnID: &turnID, CurrentPlaybackID: &playbackID},
		{SessionID: "session-1", RuntimeState: RuntimeListening},
	}
	for _, update := range updates {
		if err := service.SetProcessingState(context.Background(), update); err != nil {
			t.Fatalf("SetProcessingState(%q) error = %v", update.RuntimeState, err)
		}
	}

	got, err := service.GetRuntimeState(context.Background(), "session-1")
	if err != nil || got.StartOperationID != "operation-1" || got.RuntimeState != RuntimeListening || got.CurrentTurnID != nil || got.CurrentPlaybackID != nil {
		t.Fatalf("runtime = %#v, %v", got, err)
	}
}

func TestLifecycleReportsAssistantProcessingProgress(t *testing.T) {
	service := newTestLifecycleService(t, SessionSnapshot{SessionID: "session-1", Status: "created"}, &fakePipeline{}, &fakeConnection{})
	if err := service.deps.Runtimes.Save(t.Context(), RuntimeSnapshot{
		SessionID: "session-1", StartOperationID: "operation-1", RuntimeState: RuntimeASRProcessing,
		CurrentTurnID: stringPointer("turn-1"),
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	turnID, playbackID := "turn-1", "assistant-playback-1"
	for _, update := range []ProcessingStateUpdate{
		{SessionID: "session-1", RuntimeState: RuntimeAssistantProcessing, CurrentTurnID: &turnID},
		{SessionID: "session-1", RuntimeState: RuntimeTTSProcessing, CurrentTurnID: &turnID, CurrentPlaybackID: &playbackID},
	} {
		if err := service.SetProcessingState(t.Context(), update); err != nil {
			t.Fatalf("SetProcessingState(%q) error = %v", update.RuntimeState, err)
		}
	}
}

func TestLifecycleRejectsInvalidRuntimeProgress(t *testing.T) {
	tests := []struct {
		name    string
		current RuntimeSnapshot
		update  ProcessingStateUpdate
		want    error
	}{
		{
			name: "missing turn", current: RuntimeSnapshot{SessionID: "session-1", RuntimeState: RuntimeListening},
			update: ProcessingStateUpdate{SessionID: "session-1", RuntimeState: RuntimeASRProcessing}, want: ErrInvalidRuntimeUpdate,
		},
		{
			name: "stopped runtime", current: RuntimeSnapshot{SessionID: "session-1", RuntimeState: RuntimeStopped},
			update: ProcessingStateUpdate{SessionID: "session-1", RuntimeState: RuntimeListening}, want: ErrInvalidRuntimeTransition,
		},
		{
			name: "stale turn", current: RuntimeSnapshot{SessionID: "session-1", RuntimeState: RuntimeASRProcessing, CurrentTurnID: stringPointer("turn-1")},
			update: ProcessingStateUpdate{SessionID: "session-1", RuntimeState: RuntimeTranslating, CurrentTurnID: stringPointer("turn-2")}, want: ErrRuntimeIdentityConflict,
		},
		{
			name: "listening with empty identity pointers", current: RuntimeSnapshot{SessionID: "session-1", RuntimeState: RuntimePlaying, CurrentTurnID: stringPointer("turn-1"), CurrentPlaybackID: stringPointer("playback-1")},
			update: ProcessingStateUpdate{SessionID: "session-1", RuntimeState: RuntimeListening, CurrentTurnID: stringPointer(""), CurrentPlaybackID: stringPointer("")}, want: ErrInvalidRuntimeUpdate,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := newTestLifecycleService(t, SessionSnapshot{SessionID: "session-1", Status: "created"}, &fakePipeline{}, &fakeConnection{})
			if err := service.deps.Runtimes.Save(context.Background(), test.current); err != nil {
				t.Fatalf("Save() error = %v", err)
			}
			if err := service.SetProcessingState(context.Background(), test.update); !errors.Is(err, test.want) {
				t.Fatalf("SetProcessingState() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestLifecycleListeningCleanupRequiresExpectedPlaybackOwner(t *testing.T) {
	t.Parallel()
	service := newTestLifecycleService(t, SessionSnapshot{SessionID: "session-1", Status: "created"}, &fakePipeline{}, &fakeConnection{})
	if err := service.deps.Runtimes.Save(t.Context(), RuntimeSnapshot{
		SessionID: "session-1", RuntimeState: RuntimePlaying,
		CurrentTurnID: stringPointer("command_wake-1"), CurrentPlaybackID: stringPointer("command_playback_wake-1"),
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	turnID, playbackID := "command_wake-1", "command_playback_wake-1"
	if err := service.SetProcessingState(t.Context(), ProcessingStateUpdate{
		SessionID: "session-1", RuntimeState: RuntimeListening,
		ExpectedTurnID: &turnID, ExpectedPlaybackID: &playbackID,
	}); err != nil {
		t.Fatalf("SetProcessingState() error = %v", err)
	}
	got, err := service.GetRuntimeState(t.Context(), "session-1")
	if err != nil || got.RuntimeState != RuntimeListening || got.CurrentTurnID != nil || got.CurrentPlaybackID != nil {
		t.Fatalf("runtime after conditional cleanup = %#v, %v", got, err)
	}
}

func TestLifecycleListeningCleanupRejectsNewTurnOwner(t *testing.T) {
	t.Parallel()
	service := newTestLifecycleService(t, SessionSnapshot{SessionID: "session-1", Status: "created"}, &fakePipeline{}, &fakeConnection{})
	if err := service.deps.Runtimes.Save(t.Context(), RuntimeSnapshot{
		SessionID: "session-1", RuntimeState: RuntimeASRProcessing,
		CurrentTurnID: stringPointer("turn-2"),
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	turnID, playbackID := "command_wake-1", "command_playback_wake-1"
	err := service.SetProcessingState(t.Context(), ProcessingStateUpdate{
		SessionID: "session-1", RuntimeState: RuntimeListening,
		ExpectedTurnID: &turnID, ExpectedPlaybackID: &playbackID,
	})
	if !errors.Is(err, ErrRuntimeIdentityConflict) {
		t.Fatalf("SetProcessingState() error = %v, want identity conflict", err)
	}
	got, getErr := service.GetRuntimeState(t.Context(), "session-1")
	if getErr != nil || got.RuntimeState != RuntimeASRProcessing || got.CurrentTurnID == nil || *got.CurrentTurnID != "turn-2" {
		t.Fatalf("new Turn state was overwritten: runtime=%#v error=%v", got, getErr)
	}
}

func TestLifecycleSetRuntimeFailedPersistsTerminalState(t *testing.T) {
	service := newTestLifecycleService(t, SessionSnapshot{SessionID: "session-1", Status: "created"}, &fakePipeline{}, &fakeConnection{})
	if err := service.deps.Runtimes.Save(context.Background(), RuntimeSnapshot{
		SessionID:         "session-1",
		StartOperationID:  "operation-1",
		RuntimeState:      RuntimePlaying,
		CurrentTurnID:     stringPointer("turn-1"),
		CurrentPlaybackID: stringPointer("playback-1"),
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := service.SetRuntimeFailed(context.Background(), "session-1", ""); err != nil {
		t.Fatalf("SetRuntimeFailed() error = %v", err)
	}
	got, err := service.GetRuntimeState(context.Background(), "session-1")
	if err != nil {
		t.Fatalf("GetRuntimeState() error = %v", err)
	}
	if got.StartOperationID != "operation-1" || got.RuntimeState != RuntimeFailed || got.CurrentTurnID != nil || got.CurrentPlaybackID != nil || got.LastErrorCode == nil || *got.LastErrorCode != string(ErrorCodePipelineFailed) {
		t.Fatalf("failed runtime = %#v", got)
	}
	if err := service.SetRuntimeFailed(context.Background(), "session-1", ErrorCodeTranslationRejected); err != nil {
		t.Fatalf("repeated SetRuntimeFailed() error = %v", err)
	}
}

func TestLifecycleSetRuntimeFailedPersistsTranslationRejectedCode(t *testing.T) {
	service := newTestLifecycleService(t, SessionSnapshot{SessionID: "session-1", Status: "created"}, &fakePipeline{}, &fakeConnection{})
	if err := service.deps.Runtimes.Save(context.Background(), RuntimeSnapshot{
		SessionID:        "session-1",
		StartOperationID: "operation-1",
		RuntimeState:     RuntimeTranslating,
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := service.SetRuntimeFailed(context.Background(), "session-1", ErrorCodeTranslationRejected); err != nil {
		t.Fatalf("SetRuntimeFailed() error = %v", err)
	}
	got, err := service.GetRuntimeState(context.Background(), "session-1")
	if err != nil {
		t.Fatalf("GetRuntimeState() error = %v", err)
	}
	if got.LastErrorCode == nil || *got.LastErrorCode != string(ErrorCodeTranslationRejected) {
		t.Fatalf("last_error_code = %#v, want %q", got.LastErrorCode, ErrorCodeTranslationRejected)
	}
}

func TestLifecycleSetRuntimeFailedRejectsUnknownCode(t *testing.T) {
	service := newTestLifecycleService(t, SessionSnapshot{SessionID: "session-1", Status: "created"}, &fakePipeline{}, &fakeConnection{})
	if err := service.deps.Runtimes.Save(context.Background(), RuntimeSnapshot{
		SessionID:        "session-1",
		StartOperationID: "operation-1",
		RuntimeState:     RuntimeListening,
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	err := service.SetRuntimeFailed(context.Background(), "session-1", "not_a_real_code")
	if !errors.Is(err, ErrInvalidRuntimeErrorCode) {
		t.Fatalf("SetRuntimeFailed() error = %v, want %v", err, ErrInvalidRuntimeErrorCode)
	}
}

func TestLifecycleSetRuntimeFailedDoesNotOverrideShutdown(t *testing.T) {
	for _, state := range []RuntimeState{RuntimeStopping, RuntimeStopped, RuntimeFailed} {
		t.Run(string(state), func(t *testing.T) {
			service := newTestLifecycleService(t, SessionSnapshot{SessionID: "session-1", Status: "created"}, &fakePipeline{}, &fakeConnection{})
			lastError := "existing_error"
			want := RuntimeSnapshot{SessionID: "session-1", RuntimeState: state, LastErrorCode: &lastError}
			if err := service.deps.Runtimes.Save(context.Background(), want); err != nil {
				t.Fatalf("Save() error = %v", err)
			}
			if err := service.SetRuntimeFailed(context.Background(), "session-1", ErrorCodeTranslationRejected); err != nil {
				t.Fatalf("SetRuntimeFailed() error = %v", err)
			}
			got, err := service.GetRuntimeState(context.Background(), "session-1")
			if err != nil {
				t.Fatalf("GetRuntimeState() error = %v", err)
			}
			if got.RuntimeState != want.RuntimeState || got.LastErrorCode == nil || *got.LastErrorCode != lastError {
				t.Fatalf("shutdown state overwritten: got %#v want %#v", got, want)
			}
		})
	}
}
