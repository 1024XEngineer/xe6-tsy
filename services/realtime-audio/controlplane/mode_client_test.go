package controlplane

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/runtime"
)

func TestClientGetsAndSwitchesMode(t *testing.T) {
	fixture := newFixture(t)
	server := httptest.NewServer(fixture.handler)
	t.Cleanup(server.Close)
	client := newTestClient(t, server.URL)

	state, err := client.GetModeState(t.Context(), "session-1")
	if err != nil || state.RuntimeInstanceID != "runtime-1" || state.ActiveMode != realtimev1.ModeInterpretation {
		t.Fatalf("GetModeState() = %#v, %v", state, err)
	}
	result, err := client.SwitchMode(t.Context(), "session-1", realtimev1.SwitchModeCommand{
		SessionID: "session-1", RuntimeInstanceID: state.RuntimeInstanceID,
		OperationID: "mode-1", TraceID: "trace-1", ExpectedGeneration: state.Generation,
		TargetMode: realtimev1.ModeAssistant,
	})
	if err != nil || result.Status != realtimev1.ModeSwitchApplied ||
		result.State.ActiveMode != realtimev1.ModeAssistant || result.State.Generation != 2 {
		t.Fatalf("SwitchMode() = %#v, %v", result, err)
	}
	if fixture.lifecycle.starts != 0 || fixture.lifecycle.stops != 0 || fixture.signaling.offerCalls != 0 {
		t.Fatalf("mode client touched lifecycle/signaling: starts=%d stops=%d offers=%d",
			fixture.lifecycle.starts, fixture.lifecycle.stops, fixture.signaling.offerCalls)
	}
}

func TestClientRejectsInvalidModeRequests(t *testing.T) {
	client := newTestClient(t, "https://realtime.example")
	valid := realtimev1.SwitchModeCommand{
		SessionID: "session-1", RuntimeInstanceID: "runtime-1", OperationID: "mode-1",
		TraceID: "trace-1", ExpectedGeneration: 1, TargetMode: realtimev1.ModeAssistant,
	}
	tests := []struct {
		name      string
		sessionID string
		mutate    func(*realtimev1.SwitchModeCommand)
	}{
		{name: "empty session", mutate: func(*realtimev1.SwitchModeCommand) {}},
		{name: "session mismatch", sessionID: "session-1", mutate: func(command *realtimev1.SwitchModeCommand) { command.SessionID = "other" }},
		{name: "empty runtime", sessionID: "session-1", mutate: func(command *realtimev1.SwitchModeCommand) { command.RuntimeInstanceID = "" }},
		{name: "empty operation", sessionID: "session-1", mutate: func(command *realtimev1.SwitchModeCommand) { command.OperationID = "" }},
		{name: "empty trace", sessionID: "session-1", mutate: func(command *realtimev1.SwitchModeCommand) { command.TraceID = "" }},
		{name: "invalid generation", sessionID: "session-1", mutate: func(command *realtimev1.SwitchModeCommand) { command.ExpectedGeneration = 0 }},
		{name: "unknown mode", sessionID: "session-1", mutate: func(command *realtimev1.SwitchModeCommand) { command.TargetMode = "english_practice" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command := valid
			test.mutate(&command)
			if _, err := client.SwitchMode(t.Context(), test.sessionID, command); !errors.Is(err, ErrClientRequest) {
				t.Fatalf("SwitchMode() error = %v, want ErrClientRequest", err)
			}
		})
	}
	if _, err := client.GetModeState(t.Context(), ""); !errors.Is(err, ErrClientRequest) {
		t.Fatalf("GetModeState() error = %v, want ErrClientRequest", err)
	}
}

func TestClientMapsModeErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want error
	}{
		{name: "unavailable mode", err: runtime.ErrModeNotAvailable, want: ErrModeNotAvailable},
		{name: "generation conflict", err: runtime.ErrModeGenerationConflict, want: ErrModeGenerationConflict},
		{name: "runtime mismatch", err: runtime.ErrModeRuntimeInstanceMismatch, want: ErrModeRuntimeInstanceMismatch},
		{name: "operation conflict", err: runtime.ErrModeOperationConflict, want: ErrModeOperationConflict},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newFixture(t)
			fixture.modes.switchErr = test.err
			server := httptest.NewServer(fixture.handler)
			t.Cleanup(server.Close)
			_, err := newTestClient(t, server.URL).SwitchMode(t.Context(), "session-1", realtimev1.SwitchModeCommand{
				SessionID: "session-1", RuntimeInstanceID: "runtime-1", OperationID: "mode-1",
				TraceID: "trace-1", ExpectedGeneration: 1, TargetMode: realtimev1.ModeAssistant,
			})
			if !errors.Is(err, test.want) {
				t.Fatalf("SwitchMode() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestClientRejectsInvalidModeResponses(t *testing.T) {
	tests := []struct {
		name   string
		method string
		body   string
		call   func(*Client) error
	}{
		{
			name: "invalid state", method: http.MethodGet,
			body: `{"session_id":"session-1","runtime_instance_id":"","active_mode":"interpretation","generation":1,"phase":"active","last_operation_id":null,"updated_at":"2023-11-14T22:13:20Z"}`,
			call: func(client *Client) error { _, err := client.GetModeState(t.Context(), "session-1"); return err },
		},
		{
			name: "invalid applied generation", method: http.MethodPost,
			body: `{"operation_id":"mode-1","status":"applied","state":{"session_id":"session-1","runtime_instance_id":"runtime-1","active_mode":"assistant","generation":1,"phase":"active","last_operation_id":"mode-1","updated_at":"2023-11-14T22:13:20Z"}}`,
			call: func(client *Client) error {
				_, err := client.SwitchMode(t.Context(), "session-1", realtimev1.SwitchModeCommand{
					SessionID: "session-1", RuntimeInstanceID: "runtime-1", OperationID: "mode-1",
					TraceID: "trace-1", ExpectedGeneration: 1, TargetMode: realtimev1.ModeAssistant,
				})
				return err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.Method != test.method {
					t.Errorf("method = %s, want %s", request.Method, test.method)
				}
				writer.Header().Set("Content-Type", "application/json")
				_, _ = writer.Write([]byte(test.body))
			}))
			t.Cleanup(server.Close)
			if err := test.call(newTestClient(t, server.URL)); !errors.Is(err, ErrInvalidResponse) {
				t.Fatalf("mode call error = %v, want ErrInvalidResponse", err)
			}
		})
	}
}

func TestValidModeStateAcceptsSwitchingSnapshot(t *testing.T) {
	state := realtimev1.ModeStateSnapshot{
		SessionID: "session-1", RuntimeInstanceID: "runtime-1", ActiveMode: realtimev1.ModeAssistant,
		Generation: 2, Phase: realtimev1.ModePhaseSwitching, UpdatedAt: time.Unix(1700000000, 0).UTC(),
	}
	if !validModeState(state, state.SessionID) {
		t.Fatalf("validModeState(%#v) = false", state)
	}
}
