package realtimev1

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestCommandResultEventValidation(t *testing.T) {
	t.Parallel()
	for _, status := range []CommandResultStatus{
		CommandResultApplied, CommandResultUnchanged, CommandResultClarificationRequired,
		CommandResultUnsupported, CommandResultFailed,
	} {
		t.Run(string(status), func(t *testing.T) {
			event := validCommandResultEvent()
			event.Status = status
			if status != CommandResultApplied && status != CommandResultUnchanged {
				event.RuntimeInstanceID = ""
				event.Generation = 0
				event.Action = ""
				event.TargetMode = ""
			}
			if err := event.Validate(); err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
		})
	}

	tests := []struct {
		name   string
		mutate func(*CommandResultEvent)
	}{
		{name: "type", mutate: func(event *CommandResultEvent) { event.Type = "command.completed" }},
		{name: "version", mutate: func(event *CommandResultEvent) { event.EventVersion = 2 }},
		{name: "command id", mutate: func(event *CommandResultEvent) { event.CommandID = " command-1" }},
		{name: "session id", mutate: func(event *CommandResultEvent) { event.SessionID = "" }},
		{name: "status", mutate: func(event *CommandResultEvent) { event.Status = "unknown" }},
		{name: "action", mutate: func(event *CommandResultEvent) { event.Action = strings.Repeat("a", maxCommandResultActionLength+1) }},
		{name: "target mode", mutate: func(event *CommandResultEvent) { event.TargetMode = "english_practice" }},
		{name: "message", mutate: func(event *CommandResultEvent) { event.Message = "" }},
		{name: "occurred at", mutate: func(event *CommandResultEvent) { event.OccurredAt = time.Time{} }},
		{name: "success runtime id", mutate: func(event *CommandResultEvent) { event.RuntimeInstanceID = "" }},
		{name: "success generation", mutate: func(event *CommandResultEvent) { event.Generation = 0 }},
		{name: "success action", mutate: func(event *CommandResultEvent) { event.Action = "" }},
		{name: "partial failure runtime", mutate: func(event *CommandResultEvent) {
			event.Status = CommandResultFailed
			event.RuntimeInstanceID = ""
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			event := validCommandResultEvent()
			test.mutate(&event)
			if err := event.Validate(); !errors.Is(err, ErrInvalidCommandResultEvent) {
				t.Fatalf("Validate() error = %v, want ErrInvalidCommandResultEvent", err)
			}
		})
	}
}

func TestCommandResultEventJSONOmitsUnknownExecutionFields(t *testing.T) {
	t.Parallel()
	event := validCommandResultEvent()
	event.Status = CommandResultUnsupported
	event.RuntimeInstanceID = ""
	event.Generation = 0
	event.Action = ""
	event.TargetMode = ""
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	for _, field := range []string{"runtime_instance_id", "generation", "action", "target_mode"} {
		if strings.Contains(string(raw), `"`+field+`"`) {
			t.Fatalf("JSON unexpectedly contains %q: %s", field, raw)
		}
	}
}

func validCommandResultEvent() CommandResultEvent {
	return CommandResultEvent{
		Type: CommandResultTopic, EventVersion: CommandResultEventVersion,
		CommandID: "command-1", SessionID: "session-1", RuntimeInstanceID: "runtime-1",
		Generation: 2, Status: CommandResultApplied, Action: "activate_mode",
		TargetMode: ModeInterpretation, Message: "已进入同声传译模式",
		OccurredAt: time.Unix(1700000000, 0).UTC(),
	}
}
