package runtime

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
)

func TestModeSwitchLogIncludesOperationAndState(t *testing.T) {
	var output bytes.Buffer
	manager := &Manager{logger: slog.New(slog.NewJSONHandler(&output, nil))}
	command := realtimev1.SwitchModeCommand{
		SessionID: "session-1", RuntimeInstanceID: "runtime-1", OperationID: "operation-1",
		TraceID: "trace-1", ExpectedGeneration: 1, TargetMode: realtimev1.ModeAssistant,
	}
	result := realtimev1.SwitchModeResult{
		OperationID: command.OperationID, Status: realtimev1.ModeSwitchApplied,
		State: realtimev1.ModeStateSnapshot{
			SessionID: "session-1", RuntimeInstanceID: "runtime-1",
			ActiveMode: realtimev1.ModeAssistant, Generation: 2,
		},
	}
	manager.logModeSwitch(command, result, nil)
	fields := decodeModeLog(t, output.Bytes())
	for key, want := range map[string]any{
		"session_id": "session-1", "operation_id": "operation-1", "trace_id": "trace-1",
		"mode": "assistant", "generation": float64(2), "target_mode": "assistant",
	} {
		if fields[key] != want {
			t.Fatalf("field %s = %#v, want %#v", key, fields[key], want)
		}
	}
	for _, key := range []string{"turn_id", "provider"} {
		if _, ok := fields[key]; ok {
			t.Fatalf("field %s unexpectedly present", key)
		}
	}

	manager.logModeSwitch(realtimev1.SwitchModeCommand{SessionID: "session-1"}, realtimev1.SwitchModeResult{}, errors.New("invalid command"))
	lines := bytes.Split(bytes.TrimSpace(output.Bytes()), []byte{'\n'})
	if len(lines) != 2 {
		t.Fatalf("log lines = %d, want 2", len(lines))
	}
	var rejected map[string]any
	if err := json.Unmarshal(lines[1], &rejected); err != nil {
		t.Fatalf("decode rejected mode log: %v", err)
	}
	for _, key := range []string{"operation_id", "turn_id", "provider", "mode", "generation"} {
		if _, ok := rejected[key]; ok {
			t.Fatalf("rejected log field %s unexpectedly present", key)
		}
	}
}

func decodeModeLog(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var fields map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(data), &fields); err != nil {
		t.Fatalf("decode structured mode log: %v (%s)", err, data)
	}
	return fields
}
