package metrics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/runtime"
)

func TestObservedModeControlClassifiesEveryCommandOnce(t *testing.T) {
	tests := []struct {
		name   string
		result realtimev1.SwitchModeResult
		err    error
		assert func(testing.TB, ModeCommandSnapshot)
	}{
		{name: "applied response", result: realtimev1.SwitchModeResult{Status: realtimev1.ModeSwitchApplied}, assert: func(t testing.TB, got ModeCommandSnapshot) { assertCounter(t, got.AppliedResponse) }},
		{name: "unchanged response", result: realtimev1.SwitchModeResult{Status: realtimev1.ModeSwitchUnchanged}, assert: func(t testing.TB, got ModeCommandSnapshot) { assertCounter(t, got.UnchangedResponse) }},
		{name: "generation conflict", err: runtime.ErrModeGenerationConflict, assert: func(t testing.TB, got ModeCommandSnapshot) { assertCounter(t, got.GenerationConflict) }},
		{name: "runtime mismatch", err: runtime.ErrModeRuntimeInstanceMismatch, assert: func(t testing.TB, got ModeCommandSnapshot) { assertCounter(t, got.RuntimeMismatch) }},
		{name: "operation conflict", err: runtime.ErrModeOperationConflict, assert: func(t testing.TB, got ModeCommandSnapshot) { assertCounter(t, got.OperationConflict) }},
		{name: "mode unavailable", err: runtime.ErrModeNotAvailable, assert: func(t testing.TB, got ModeCommandSnapshot) { assertCounter(t, got.ModeUnavailable) }},
		{name: "transition pending", err: runtime.ErrModeTransitionPending, assert: func(t testing.TB, got ModeCommandSnapshot) { assertCounter(t, got.TransitionPending) }},
		{name: "wrapped event unavailable", err: fmt.Errorf("publish: %w", runtime.ErrModeEventUnavailable), assert: func(t testing.TB, got ModeCommandSnapshot) { assertCounter(t, got.EventUnavailable) }},
		{name: "unexpected failure", err: errors.New("unexpected"), assert: func(t testing.TB, got ModeCommandSnapshot) { assertCounter(t, got.OtherFailure) }},
		{name: "unknown success status", result: realtimev1.SwitchModeResult{}, assert: func(t testing.TB, got ModeCommandSnapshot) { assertCounter(t, got.OtherFailure) }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			registry := &Registry{}
			next := &stubModeControl{result: test.result, err: test.err}
			observed := ObserveModeControl(next, registry)
			result, err := observed.SwitchMode(context.Background(), realtimev1.SwitchModeCommand{})
			if !errors.Is(err, test.err) || result.Status != test.result.Status {
				t.Fatalf("SwitchMode() = (%#v, %v), want (%#v, %v)", result, err, test.result, test.err)
			}
			got := registry.Current().ModeCommands
			if got.Total != 1 || modeCommandOutcomeSum(got) != got.Total {
				t.Fatalf("mode command counters = %#v, want one classified command", got)
			}
			test.assert(t, got)
		})
	}
}

func TestObservedModeControlPassesThroughStateReads(t *testing.T) {
	want := realtimev1.ModeStateSnapshot{SessionID: "session-1", Generation: 3}
	next := &stubModeControl{state: want}
	registry := &Registry{}
	got, err := ObserveModeControl(next, registry).GetModeState(context.Background(), want.SessionID)
	if err != nil || got != want {
		t.Fatalf("GetModeState() = (%#v, %v), want (%#v, nil)", got, err, want)
	}
	if snapshot := registry.Current(); snapshot != (Snapshot{}) {
		t.Fatalf("state read changed counters: %#v", snapshot)
	}
}

func TestObservedModeChangedSinkCountsAcceptanceAndFailure(t *testing.T) {
	wantErr := errors.New("outbox unavailable")
	next := &stubModeChangedSink{errors: []error{wantErr, nil}}
	registry := &Registry{}
	observed := ObserveModeChangedSink(next, registry)

	if err := observed.Publish(context.Background(), realtimev1.ModeChangedEvent{}); !errors.Is(err, wantErr) {
		t.Fatalf("first Publish() error = %v, want %v", err, wantErr)
	}
	if err := observed.Publish(context.Background(), realtimev1.ModeChangedEvent{}); err != nil {
		t.Fatalf("second Publish() error = %v", err)
	}

	got := registry.Current().ModeChangePublications
	if got.Attempted != 2 || got.Accepted != 1 || got.Failed != 1 || got.Attempted != got.Accepted+got.Failed {
		t.Fatalf("publication counters = %#v", got)
	}
}

func TestMetricsHandlerReturnsJSONSnapshot(t *testing.T) {
	registry := &Registry{}
	registry.recordModeCommand(realtimev1.SwitchModeResult{Status: realtimev1.ModeSwitchApplied}, nil)
	mux := http.NewServeMux()
	Register(mux, registry)

	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if contentType := response.Header().Get("Content-Type"); contentType != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", contentType)
	}
	var got Snapshot
	if err := json.NewDecoder(response.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.ModeCommands.Total != 1 || got.ModeCommands.AppliedResponse != 1 {
		t.Fatalf("snapshot = %#v", got)
	}
}

func modeCommandOutcomeSum(snapshot ModeCommandSnapshot) uint64 {
	return snapshot.AppliedResponse + snapshot.UnchangedResponse + snapshot.GenerationConflict +
		snapshot.RuntimeMismatch + snapshot.OperationConflict + snapshot.ModeUnavailable +
		snapshot.TransitionPending + snapshot.EventUnavailable + snapshot.OtherFailure
}

func assertCounter(t testing.TB, got uint64) {
	t.Helper()
	if got != 1 {
		t.Fatalf("classified counter = %d, want 1", got)
	}
}

type stubModeControl struct {
	state  realtimev1.ModeStateSnapshot
	result realtimev1.SwitchModeResult
	err    error
}

func (s *stubModeControl) GetModeState(context.Context, string) (realtimev1.ModeStateSnapshot, error) {
	return s.state, s.err
}

func (s *stubModeControl) SwitchMode(context.Context, realtimev1.SwitchModeCommand) (realtimev1.SwitchModeResult, error) {
	return s.result, s.err
}

type stubModeChangedSink struct {
	errors []error
	calls  int
}

func (s *stubModeChangedSink) Publish(context.Context, realtimev1.ModeChangedEvent) error {
	err := s.errors[s.calls]
	s.calls++
	return err
}
