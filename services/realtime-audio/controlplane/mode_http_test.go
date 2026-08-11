package controlplane

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/runtime"
)

func TestHandlerGetsAuthoritativeModeState(t *testing.T) {
	fixture := newFixture(t)
	response := fixture.request(http.MethodGet, "/realtime/v1/sessions/session-1/mode", "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("mode state status = %d, body=%s", response.Code, response.Body.String())
	}
	var state realtimev1.ModeStateSnapshot
	if err := json.NewDecoder(response.Body).Decode(&state); err != nil {
		t.Fatalf("decode mode state: %v", err)
	}
	if state != fixture.modes.state || fixture.modes.getCalls != 1 {
		t.Fatalf("mode state = %#v, calls = %d", state, fixture.modes.getCalls)
	}
}

func TestHandlerSwitchesModeIdempotently(t *testing.T) {
	fixture := newFixture(t)
	body := `{"session_id":"session-1","runtime_instance_id":"runtime-1","operation_id":"mode-1","trace_id":"trace-1","expected_generation":1,"target_mode":"assistant"}`

	first := fixture.request(http.MethodPost, "/realtime/v1/sessions/session-1/mode", body, "mode:mode-1")
	second := fixture.request(http.MethodPost, "/realtime/v1/sessions/session-1/mode", body, "mode:mode-1")
	if first.Code != http.StatusOK || second.Code != http.StatusOK {
		t.Fatalf("mode switch statuses = %d, %d; bodies=%s %s", first.Code, second.Code, first.Body.String(), second.Body.String())
	}
	if fixture.modes.switchCalls != 1 || fixture.modes.command.OperationID != "mode-1" ||
		fixture.modes.command.TargetMode != realtimev1.ModeAssistant {
		t.Fatalf("mode calls = %d, command = %#v", fixture.modes.switchCalls, fixture.modes.command)
	}
	var result realtimev1.SwitchModeResult
	if err := json.NewDecoder(second.Body).Decode(&result); err != nil {
		t.Fatalf("decode replayed mode result: %v", err)
	}
	if result.OperationID != "mode-1" || result.State.ActiveMode != realtimev1.ModeAssistant || result.State.Generation != 2 {
		t.Fatalf("mode result = %#v", result)
	}
}

func TestHandlerRejectsInvalidModeCommands(t *testing.T) {
	fixture := newFixture(t)
	valid := `{"session_id":"session-1","runtime_instance_id":"runtime-1","operation_id":"mode-1","trace_id":"trace-1","expected_generation":1,"target_mode":"assistant"}`
	tests := []struct {
		name string
		body string
		key  string
	}{
		{name: "missing idempotency key", body: valid},
		{name: "session mismatch", body: `{"session_id":"other","runtime_instance_id":"runtime-1","operation_id":"mode-1","trace_id":"trace-1","expected_generation":1,"target_mode":"assistant"}`, key: "mode:mode-1"},
		{name: "missing runtime", body: `{"session_id":"session-1","operation_id":"mode-1","trace_id":"trace-1","expected_generation":1,"target_mode":"assistant"}`, key: "mode:mode-1"},
		{name: "invalid generation", body: `{"session_id":"session-1","runtime_instance_id":"runtime-1","operation_id":"mode-1","trace_id":"trace-1","expected_generation":0,"target_mode":"assistant"}`, key: "mode:mode-1"},
		{name: "unknown mode", body: `{"session_id":"session-1","runtime_instance_id":"runtime-1","operation_id":"mode-1","trace_id":"trace-1","expected_generation":1,"target_mode":"english_practice"}`, key: "mode:mode-1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := fixture.request(http.MethodPost, "/realtime/v1/sessions/session-1/mode", test.body, test.key)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
			}
		})
	}
	if fixture.modes.switchCalls != 0 {
		t.Fatalf("invalid commands reached mode control %d times", fixture.modes.switchCalls)
	}
}

func TestHandlerMapsModeErrors(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
		code   realtimev1.ControlPlaneErrorCode
	}{
		{name: "unavailable mode", err: runtime.ErrModeNotAvailable, status: http.StatusUnprocessableEntity, code: realtimev1.ErrorModeNotAvailable},
		{name: "generation conflict", err: runtime.ErrModeGenerationConflict, status: http.StatusConflict, code: realtimev1.ErrorModeGenerationConflict},
		{name: "runtime mismatch", err: runtime.ErrModeRuntimeInstanceMismatch, status: http.StatusConflict, code: realtimev1.ErrorModeRuntimeInstanceMismatch},
		{name: "operation conflict", err: runtime.ErrModeOperationConflict, status: http.StatusConflict, code: realtimev1.ErrorModeOperationConflict},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newFixture(t)
			fixture.modes.switchErr = test.err
			body := `{"session_id":"session-1","runtime_instance_id":"runtime-1","operation_id":"mode-1","trace_id":"trace-1","expected_generation":1,"target_mode":"assistant"}`
			response := fixture.request(http.MethodPost, "/realtime/v1/sessions/session-1/mode", body, "mode:mode-1")
			if response.Code != test.status || !stringsContainErrorCode(response.Body.String(), string(test.code)) {
				t.Fatalf("response = %d %s", response.Code, response.Body.String())
			}
		})
	}
}

func stringsContainErrorCode(body, code string) bool {
	var response struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	return json.Unmarshal([]byte(body), &response) == nil && response.Error.Code == code
}

type modeControlFake struct {
	state       realtimev1.ModeStateSnapshot
	command     realtimev1.SwitchModeCommand
	getErr      error
	switchErr   error
	getCalls    int
	switchCalls int
}

func (f *modeControlFake) GetModeState(context.Context, string) (realtimev1.ModeStateSnapshot, error) {
	f.getCalls++
	return f.state, f.getErr
}

func (f *modeControlFake) SwitchMode(_ context.Context, command realtimev1.SwitchModeCommand) (realtimev1.SwitchModeResult, error) {
	f.switchCalls++
	f.command = command
	if f.switchErr != nil {
		return realtimev1.SwitchModeResult{}, f.switchErr
	}
	operationID := command.OperationID
	state := f.state
	state.ActiveMode = command.TargetMode
	state.Generation = command.ExpectedGeneration + 1
	state.LastOperationID = &operationID
	return realtimev1.SwitchModeResult{OperationID: command.OperationID, Status: realtimev1.ModeSwitchApplied, State: state}, nil
}

var _ ModeControl = (*modeControlFake)(nil)
