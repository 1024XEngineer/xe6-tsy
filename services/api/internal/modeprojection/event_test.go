package modeprojection

import (
	"testing"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
)

func TestHashModeChangedEventCoversEveryContractField(t *testing.T) {
	base := modeEvent(
		"event-1",
		"runtime-1",
		2,
		realtimev1.ModeInterpretation,
		realtimev1.ModeAssistant,
		time.Date(2026, time.August, 11, 8, 0, 0, 123, time.UTC),
	)
	baseHash, err := hashModeChangedEvent(base)
	if err != nil {
		t.Fatalf("hashModeChangedEvent() error = %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*realtimev1.ModeChangedEvent)
	}{
		{name: "event version", mutate: func(event *realtimev1.ModeChangedEvent) { event.EventVersion++ }},
		{name: "event id", mutate: func(event *realtimev1.ModeChangedEvent) { event.EventID += "-changed" }},
		{name: "trace id", mutate: func(event *realtimev1.ModeChangedEvent) { event.TraceID += "-changed" }},
		{name: "session id", mutate: func(event *realtimev1.ModeChangedEvent) { event.SessionID += "-changed" }},
		{name: "runtime id", mutate: func(event *realtimev1.ModeChangedEvent) { event.RuntimeInstanceID += "-changed" }},
		{name: "operation id", mutate: func(event *realtimev1.ModeChangedEvent) { event.OperationID += "-changed" }},
		{name: "from mode", mutate: func(event *realtimev1.ModeChangedEvent) { event.FromMode = realtimev1.ModeAssistant }},
		{name: "to mode", mutate: func(event *realtimev1.ModeChangedEvent) { event.ToMode = realtimev1.ModeInterpretation }},
		{name: "generation", mutate: func(event *realtimev1.ModeChangedEvent) { event.ResultingGeneration++ }},
		{name: "occurred at", mutate: func(event *realtimev1.ModeChangedEvent) { event.OccurredAt = event.OccurredAt.Add(time.Nanosecond) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			changed := base
			test.mutate(&changed)
			changedHash, err := hashModeChangedEvent(changed)
			if err != nil {
				t.Fatalf("hashModeChangedEvent() error = %v", err)
			}
			if changedHash == baseHash {
				t.Fatalf("hash did not change after mutating %s", test.name)
			}
		})
	}
}
