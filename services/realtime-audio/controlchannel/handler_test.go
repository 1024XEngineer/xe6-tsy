package controlchannel

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/runtime"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/session"
)

func TestHandlerRequiresConfiguredModeControl(t *testing.T) {
	handler := NewHandler()
	response := handler.HandleModeSwitch(t.Context(), "session-1", "rtc-1", "request-1", validCommand())
	assertControlError(t, response, realtimev1.ErrorControlUnavailable)
}

func TestHandlerRejectsInvalidConnectionBinding(t *testing.T) {
	tests := []struct {
		name         string
		sessionID    string
		connectionID string
	}{
		{name: "trimmed session mismatch", sessionID: " session-1", connectionID: "rtc-1"},
		{name: "trimmed connection mismatch", sessionID: "session-1", connectionID: "rtc-1 "},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			control := &modeControlFake{}
			handler := configuredHandler(t, control)
			response := handler.HandleModeSwitch(t.Context(), test.sessionID, test.connectionID, "request-1", validCommand())
			assertControlError(t, response, realtimev1.ErrorControlUnauthorizedSession)
			if control.switchCalls != 0 {
				t.Fatalf("SwitchMode() calls = %d, want 0", control.switchCalls)
			}
		})
	}
}

func TestHandlerPromotesBoundModeCommand(t *testing.T) {
	control := &modeControlFake{result: validResult()}
	handler := configuredHandler(t, control)
	command := validCommand()
	response := handler.HandleModeSwitch(t.Context(), "session-1", "rtc-1", "request-1", command)

	if err := response.Validate(); err != nil {
		t.Fatalf("response Validate() error = %v", err)
	}
	if response.Type != realtimev1.ControlMessageModeSwitchResult || response.Result == nil || response.Error != nil {
		t.Fatalf("response = %#v, want mode switch result", response)
	}
	if control.switchCalls != 1 {
		t.Fatalf("SwitchMode() calls = %d, want 1", control.switchCalls)
	}
	wantTrace := controlTraceID("session-1", command.OperationID)
	if control.command.SessionID != "session-1" || control.command.RuntimeInstanceID != command.RuntimeInstanceID ||
		control.command.OperationID != command.OperationID || control.command.TraceID != wantTrace ||
		control.command.ExpectedGeneration != command.ExpectedGeneration || control.command.TargetMode != command.TargetMode {
		t.Fatalf("SwitchMode() command = %#v", control.command)
	}
	firstCommand := control.command
	second := handler.HandleModeSwitch(t.Context(), "session-1", "rtc-1", "request-2", command)
	if control.switchCalls != 2 || control.command != firstCommand {
		t.Fatalf("duplicate SwitchMode calls = %d, latest command = %#v, first = %#v", control.switchCalls, control.command, firstCommand)
	}
	if response.Result == nil || second.Result == nil || *response.Result != *second.Result {
		t.Fatalf("duplicate responses = %#v, %#v", response, second)
	}
}

func TestHandlerMapsModeControlErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code realtimev1.ControlPlaneErrorCode
	}{
		{name: "operation conflict", err: runtime.ErrModeOperationConflict, code: realtimev1.ErrorModeOperationConflict},
		{name: "runtime mismatch", err: runtime.ErrModeRuntimeInstanceMismatch, code: realtimev1.ErrorModeRuntimeInstanceMismatch},
		{name: "generation conflict", err: runtime.ErrModeGenerationConflict, code: realtimev1.ErrorModeGenerationConflict},
		{name: "mode unavailable", err: runtime.ErrModeNotAvailable, code: realtimev1.ErrorModeNotAvailable},
		{name: "runtime operation conflict", err: session.ErrRuntimeOperationConflict, code: realtimev1.ErrorRuntimeOperationConflict},
		{name: "runtime missing", err: session.ErrRuntimeNotFound, code: realtimev1.ErrorControlUnavailable},
		{name: "event unavailable", err: runtime.ErrModeEventUnavailable, code: realtimev1.ErrorControlUnavailable},
		{name: "invalid runtime command", err: runtime.ErrModeCommandInvalid, code: realtimev1.ErrorControlInvalidMessage},
		{name: "unknown dependency", err: errors.New("provider failed"), code: realtimev1.ErrorControlUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			control := &modeControlFake{err: test.err}
			response := configuredHandler(t, control).HandleModeSwitch(
				t.Context(), "session-1", "rtc-1", "request-1", validCommand(),
			)
			assertControlError(t, response, test.code)
		})
	}
}

func TestHandlerMapsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	control := &modeControlFake{}
	response := configuredHandler(t, control).HandleModeSwitch(ctx, "session-1", "rtc-1", "request-1", validCommand())
	assertControlError(t, response, realtimev1.ErrorControlConnectionClosed)
	if control.switchCalls != 0 {
		t.Fatalf("SwitchMode() calls = %d, want 0", control.switchCalls)
	}
}

func TestHandlerSetModeControlIsConcurrencySafe(t *testing.T) {
	handler := NewHandler()
	const attempts = 16
	results := make(chan error, attempts)
	var ready sync.WaitGroup
	ready.Add(attempts)
	start := make(chan struct{})
	for range attempts {
		go func() {
			ready.Done()
			<-start
			results <- handler.SetModeControl(&modeControlFake{})
		}()
	}
	ready.Wait()
	close(start)

	successes := 0
	for range attempts {
		err := <-results
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrModeControlAlreadyConfigured):
		default:
			t.Fatalf("SetModeControl() error = %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("successful SetModeControl() calls = %d, want 1", successes)
	}
}

func TestHandlerRejectsMalformedTypedCommand(t *testing.T) {
	control := &modeControlFake{}
	response := configuredHandler(t, control).HandleModeSwitch(
		t.Context(), "session-1", "rtc-1", "request-1", realtimev1.ControlModeSwitchCommand{},
	)
	assertControlError(t, response, realtimev1.ErrorControlInvalidMessage)
	if control.switchCalls != 0 {
		t.Fatalf("SwitchMode() calls = %d, want 0", control.switchCalls)
	}
}

func TestHandlerOmitsMalformedRequestIDFromError(t *testing.T) {
	control := &modeControlFake{}
	response := configuredHandler(t, control).HandleModeSwitch(
		t.Context(), "session-1", "rtc-1", "bad\nrequest", validCommand(),
	)
	assertControlError(t, response, realtimev1.ErrorControlInvalidMessage)
	if response.RequestID != "" {
		t.Fatalf("response RequestID = %q, want empty", response.RequestID)
	}
}

func configuredHandler(t *testing.T, modes ModeControl) *Handler {
	t.Helper()
	handler := NewHandler()
	if err := handler.SetModeControl(modes); err != nil {
		t.Fatalf("SetModeControl() error = %v", err)
	}
	return handler
}

func validCommand() realtimev1.ControlModeSwitchCommand {
	return realtimev1.ControlModeSwitchCommand{
		RuntimeInstanceID: "runtime-1", OperationID: "mode-1",
		ExpectedGeneration: 1, TargetMode: realtimev1.ModeAssistant,
	}
}

func validResult() realtimev1.SwitchModeResult {
	operationID := "mode-1"
	return realtimev1.SwitchModeResult{
		OperationID: operationID,
		Status:      realtimev1.ModeSwitchApplied,
		State: realtimev1.ModeStateSnapshot{
			SessionID: "session-1", RuntimeInstanceID: "runtime-1", ActiveMode: realtimev1.ModeAssistant,
			Generation: 2, Phase: realtimev1.ModePhaseActive, LastOperationID: &operationID, UpdatedAt: time.Unix(10, 0).UTC(),
		},
	}
}

func assertControlError(t *testing.T, response realtimev1.ControlResponse, code realtimev1.ControlPlaneErrorCode) {
	t.Helper()
	if err := response.Validate(); err != nil {
		t.Fatalf("response Validate() error = %v for %#v", err, response)
	}
	if response.Type != realtimev1.ControlMessageError || response.Result != nil || response.Error == nil || response.Error.Code != code {
		t.Fatalf("response = %#v, want error code %q", response, code)
	}
}

type modeControlFake struct {
	switchCalls int
	command     realtimev1.SwitchModeCommand
	result      realtimev1.SwitchModeResult
	err         error
}

func (f *modeControlFake) SwitchMode(_ context.Context, command realtimev1.SwitchModeCommand) (realtimev1.SwitchModeResult, error) {
	f.switchCalls++
	f.command = command
	return f.result, f.err
}
