package device

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTPModeTransportGetsAndSwitchesMode(t *testing.T) {
	now := time.Date(2026, 8, 11, 1, 2, 3, 0, time.UTC)
	state := makeModeState("session-1", "runtime-1", 1, ModeInterpretation, now)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer ticket-1" {
			t.Fatalf("authorization = %q", r.Header.Get("Authorization"))
		}
		if r.URL.Path != "/realtime/v1/sessions/session-1/mode" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(state)
		case http.MethodPost:
			if r.Header.Get("Idempotency-Key") != "mode:operation-1" {
				t.Fatalf("idempotency key = %q", r.Header.Get("Idempotency-Key"))
			}
			var command SwitchModeCommand
			if err := json.NewDecoder(r.Body).Decode(&command); err != nil {
				t.Fatalf("decode command: %v", err)
			}
			if command.TargetMode != ModeAssistant || command.ExpectedGeneration != 1 {
				t.Fatalf("command = %#v", command)
			}
			changed := makeModeState(command.SessionID, command.RuntimeInstanceID, 2, ModeAssistant, now.Add(time.Second))
			last := command.OperationID
			changed.LastOperationID = &last
			_ = json.NewEncoder(w).Encode(SwitchModeResult{OperationID: command.OperationID, Status: ModeSwitchApplied, State: changed})
		default:
			http.Error(w, "method", http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	transport := &HTTPModeTransport{BaseURL: server.URL, Ticket: TicketSourceFunc(func(context.Context, string) (string, error) {
		return "ticket-1", nil
	})}
	got, err := transport.GetModeState(context.Background(), "session-1")
	if err != nil || got != state {
		t.Fatalf("GetModeState() = %#v, %v", got, err)
	}
	operation := SwitchModeCommand{SessionID: "session-1", RuntimeInstanceID: "runtime-1", OperationID: "operation-1", TraceID: "trace-1", ExpectedGeneration: 1, TargetMode: ModeAssistant}
	result, err := transport.SwitchMode(context.Background(), operation)
	if err != nil || result.State.ActiveMode != ModeAssistant || result.State.Generation != 2 {
		t.Fatalf("SwitchMode() = %#v, %v", result, err)
	}
}

func TestHTTPModeTransportMapsConflicts(t *testing.T) {
	for _, test := range []struct {
		code string
		want error
	}{
		{code: "mode_generation_conflict", want: ErrGenerationConflict},
		{code: "mode_runtime_instance_mismatch", want: ErrRuntimeInstanceGone},
		{code: "unauthorized", want: ErrUnauthorized},
	} {
		t.Run(test.code, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				status := http.StatusConflict
				if test.code == "unauthorized" {
					status = http.StatusUnauthorized
				}
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"error":{"code":"` + test.code + `"}}`))
			}))
			defer server.Close()
			transport := &HTTPModeTransport{BaseURL: server.URL, Ticket: TicketSourceFunc(func(context.Context, string) (string, error) { return "ticket", nil })}
			_, err := transport.GetModeState(context.Background(), "session-1")
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestModeControllerRefreshesAndDiscardsConflictOperation(t *testing.T) {
	transport := &fakeModeTransport{
		state:     makeModeState("session-1", "runtime-1", 2, ModeAssistant, time.Now().UTC()),
		switchErr: ErrGenerationConflict,
	}
	controller, err := NewModeController(transport, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	initial := makeModeState("session-1", "runtime-1", 1, ModeInterpretation, time.Now().UTC().Add(-time.Minute))
	transport.state = initial
	if _, err := controller.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	transport.state = makeModeState("session-1", "runtime-1", 2, ModeAssistant, time.Now().UTC())
	_, err = controller.SwitchOperation(context.Background(), "operation-1", "trace-1", ModeAssistant)
	if !errors.Is(err, ErrGenerationConflict) || !errors.Is(err, ErrOperationDiscarded) {
		t.Fatalf("conflict error = %v", err)
	}
	if transport.getCalls != 2 {
		t.Fatalf("GET calls = %d, want 2", transport.getCalls)
	}
	if _, err := controller.SwitchOperation(context.Background(), "operation-1", "trace-1", ModeAssistant); !errors.Is(err, ErrOperationDiscarded) {
		t.Fatalf("replayed operation error = %v", err)
	}
}

func TestModeControllerIgnoresLateOldRuntimeRefresh(t *testing.T) {
	transport := &fakeModeTransport{state: makeModeState("session-1", "runtime-1", 1, ModeInterpretation, time.Unix(1, 0).UTC())}
	controller, err := NewModeController(transport, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controller.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	transport.state = makeModeState("session-1", "runtime-2", 1, ModeAssistant, time.Unix(2, 0).UTC())
	if _, err := controller.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	transport.state = makeModeState("session-1", "runtime-1", 9, ModeInterpretation, time.Unix(9, 0).UTC())
	state, err := controller.Refresh(context.Background())
	if err != nil || state.RuntimeInstanceID != "runtime-2" {
		t.Fatalf("late refresh = %#v, %v", state, err)
	}
}

type fakeModeTransport struct {
	state      ModeStateSnapshot
	getCalls   int
	switchErr  error
	switchFunc func(SwitchModeCommand) (SwitchModeResult, error)
}

func (f *fakeModeTransport) GetModeState(context.Context, string) (ModeStateSnapshot, error) {
	f.getCalls++
	return f.state, nil
}

func (f *fakeModeTransport) SwitchMode(_ context.Context, command SwitchModeCommand) (SwitchModeResult, error) {
	if f.switchFunc != nil {
		return f.switchFunc(command)
	}
	if f.switchErr != nil {
		return SwitchModeResult{}, f.switchErr
	}
	return SwitchModeResult{}, nil
}

func TestModeControllerDiscardsLateSuccessFromRetiredRuntime(t *testing.T) {
	now := time.Now().UTC()
	started := make(chan SwitchModeCommand, 1)
	release := make(chan struct{})
	transport := &fakeModeTransport{state: makeModeState("session-1", "runtime-1", 1, ModeInterpretation, now)}
	transport.switchFunc = func(command SwitchModeCommand) (SwitchModeResult, error) {
		started <- command
		<-release
		last := command.OperationID
		state := makeModeState(command.SessionID, command.RuntimeInstanceID, 2, command.TargetMode, now.Add(2*time.Second))
		state.LastOperationID = &last
		return SwitchModeResult{OperationID: command.OperationID, Status: ModeSwitchApplied, State: state}, nil
	}
	controller, err := NewModeController(transport, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controller.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := controller.SwitchOperation(context.Background(), "operation-1", "trace-1", ModeAssistant)
		done <- err
	}()
	<-started
	transport.state = makeModeState("session-1", "runtime-2", 1, ModeInterpretation, now.Add(time.Second))
	if _, err := controller.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; !errors.Is(err, ErrOperationDiscarded) {
		t.Fatalf("late response error = %v", err)
	}
	state, _ := controller.Snapshot()
	if state.RuntimeInstanceID != "runtime-2" {
		t.Fatalf("runtime = %q, want runtime-2", state.RuntimeInstanceID)
	}
}

func makeModeState(session, runtime string, generation int64, mode Mode, updated time.Time) ModeStateSnapshot {
	return ModeStateSnapshot{SessionID: session, RuntimeInstanceID: runtime, ActiveMode: mode, Generation: generation, Phase: ModePhaseActive, UpdatedAt: updated}
}

func TestHTTPModeTransportRejectsInvalidRequest(t *testing.T) {
	transport := &HTTPModeTransport{BaseURL: "http://example.test", Ticket: TicketSourceFunc(func(context.Context, string) (string, error) { return "", nil })}
	_, err := transport.SwitchMode(context.Background(), SwitchModeCommand{SessionID: "session-1", TargetMode: ModeAssistant})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("error = %v", err)
	}
	if strings.Contains(err.Error(), "example.test") {
		t.Fatal("invalid request should fail before network")
	}
}
