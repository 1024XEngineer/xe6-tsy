package session

import (
	"context"
	"errors"
	"testing"
)

func TestLifecycleReportsPipelineRuntimeProgress(t *testing.T) {
	service := newTestLifecycleService(t, SessionSnapshot{SessionID: "session-1", Status: "created"}, &fakePipeline{}, &fakeConnection{})
	if err := service.deps.Runtimes.Save(context.Background(), RuntimeSnapshot{SessionID: "session-1", RuntimeState: RuntimeListening}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	turnID := "turn-1"
	playbackID := "playback-1"
	updates := []RuntimeStateUpdate{
		{SessionID: "session-1", RuntimeState: RuntimeASRProcessing, CurrentTurnID: &turnID},
		{SessionID: "session-1", RuntimeState: RuntimeTranslating, CurrentTurnID: &turnID},
		{SessionID: "session-1", RuntimeState: RuntimeTTSProcessing, CurrentTurnID: &turnID, CurrentPlaybackID: &playbackID},
		{SessionID: "session-1", RuntimeState: RuntimePlaying, CurrentTurnID: &turnID, CurrentPlaybackID: &playbackID},
		{SessionID: "session-1", RuntimeState: RuntimeListening},
	}
	for _, update := range updates {
		if err := service.ReportRuntimeState(context.Background(), update); err != nil {
			t.Fatalf("ReportRuntimeState(%q) error = %v", update.RuntimeState, err)
		}
	}

	got, err := service.GetRuntimeState(context.Background(), "session-1")
	if err != nil || got.RuntimeState != RuntimeListening || got.CurrentTurnID != nil || got.CurrentPlaybackID != nil {
		t.Fatalf("runtime = %#v, %v", got, err)
	}
}

func TestLifecycleRejectsInvalidRuntimeProgress(t *testing.T) {
	tests := []struct {
		name    string
		current RuntimeSnapshot
		update  RuntimeStateUpdate
		want    error
	}{
		{
			name: "missing turn", current: RuntimeSnapshot{SessionID: "session-1", RuntimeState: RuntimeListening},
			update: RuntimeStateUpdate{SessionID: "session-1", RuntimeState: RuntimeASRProcessing}, want: ErrInvalidRuntimeUpdate,
		},
		{
			name: "stopped runtime", current: RuntimeSnapshot{SessionID: "session-1", RuntimeState: RuntimeStopped},
			update: RuntimeStateUpdate{SessionID: "session-1", RuntimeState: RuntimeListening}, want: ErrInvalidRuntimeTransition,
		},
		{
			name: "stale turn", current: RuntimeSnapshot{SessionID: "session-1", RuntimeState: RuntimeASRProcessing, CurrentTurnID: stringPointer("turn-1")},
			update: RuntimeStateUpdate{SessionID: "session-1", RuntimeState: RuntimeTranslating, CurrentTurnID: stringPointer("turn-2")}, want: ErrRuntimeIdentityConflict,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := newTestLifecycleService(t, SessionSnapshot{SessionID: "session-1", Status: "created"}, &fakePipeline{}, &fakeConnection{})
			if err := service.deps.Runtimes.Save(context.Background(), test.current); err != nil {
				t.Fatalf("Save() error = %v", err)
			}
			if err := service.ReportRuntimeState(context.Background(), test.update); !errors.Is(err, test.want) {
				t.Fatalf("ReportRuntimeState() error = %v, want %v", err, test.want)
			}
		})
	}
}
