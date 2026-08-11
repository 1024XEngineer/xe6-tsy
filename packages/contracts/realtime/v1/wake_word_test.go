package realtimev1

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestWakeWordDetectedSignalValidation(t *testing.T) {
	signal := validWakeWordDetectedSignal()
	if err := signal.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*WakeWordDetectedSignal)
	}{
		{name: "type", mutate: func(signal *WakeWordDetectedSignal) { signal.Type = "command.detected" }},
		{name: "event version", mutate: func(signal *WakeWordDetectedSignal) { signal.EventVersion = 2 }},
		{name: "empty signal id", mutate: func(signal *WakeWordDetectedSignal) { signal.SignalID = "" }},
		{name: "surrounding signal id whitespace", mutate: func(signal *WakeWordDetectedSignal) { signal.SignalID = " wake-1" }},
		{name: "oversized signal id", mutate: func(signal *WakeWordDetectedSignal) {
			signal.SignalID = strings.Repeat("a", maxWakeWordSignalIDLength+1)
		}},
		{name: "detected at", mutate: func(signal *WakeWordDetectedSignal) { signal.DetectedAt = time.Time{} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			invalid := validWakeWordDetectedSignal()
			test.mutate(&invalid)
			if err := invalid.Validate(); !errors.Is(err, ErrInvalidWakeWordDetectedSignal) {
				t.Fatalf("Validate() error = %v, want ErrInvalidWakeWordDetectedSignal", err)
			}
		})
	}
}

func TestWakeWordDetectedSignalJSON(t *testing.T) {
	encoded, err := json.Marshal(validWakeWordDetectedSignal())
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	want := `{"type":"wake_word.detected","event_version":1,"signal_id":"wake-1","detected_at":"2023-11-14T22:13:20Z"}`
	if string(encoded) != want {
		t.Fatalf("JSON = %s, want %s", encoded, want)
	}
}

func validWakeWordDetectedSignal() WakeWordDetectedSignal {
	return WakeWordDetectedSignal{
		Type:         WakeWordDetectedType,
		EventVersion: WakeWordDetectedEventVersion,
		SignalID:     "wake-1",
		DetectedAt:   time.Unix(1700000000, 0).UTC(),
	}
}
