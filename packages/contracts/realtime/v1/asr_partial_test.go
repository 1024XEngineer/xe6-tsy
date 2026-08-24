package realtimev1

import (
	"errors"
	"testing"
	"time"
)

func TestASRPartialEventValidation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*ASRPartialEvent)
	}{
		{name: "type", mutate: func(event *ASRPartialEvent) { event.Type = "translation.partial" }},
		{name: "version", mutate: func(event *ASRPartialEvent) { event.EventVersion = 2 }},
		{name: "session", mutate: func(event *ASRPartialEvent) { event.SessionID = "" }},
		{name: "turn", mutate: func(event *ASRPartialEvent) { event.TurnID = " turn-1" }},
		{name: "text", mutate: func(event *ASRPartialEvent) { event.Text = "" }},
		{name: "source language", mutate: func(event *ASRPartialEvent) { event.SourceLanguage = " zh-CN" }},
		{name: "occurred at", mutate: func(event *ASRPartialEvent) { event.OccurredAt = time.Time{} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			event := validASRPartialEvent()
			test.mutate(&event)
			if err := event.Validate(); !errors.Is(err, ErrInvalidASRPartialEvent) {
				t.Fatalf("Validate() error = %v, want ErrInvalidASRPartialEvent", err)
			}
		})
	}

	if err := validASRPartialEvent().Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestASRPartialEventAllowsUnknownSourceLanguage(t *testing.T) {
	event := validASRPartialEvent()
	event.SourceLanguage = ""
	if err := event.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestASRPartialEventAllowsStashOnlySnapshot(t *testing.T) {
	event := validASRPartialEvent()
	event.Text = ""
	event.Stash = "听得见吗？"
	if err := event.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func validASRPartialEvent() ASRPartialEvent {
	return ASRPartialEvent{
		Type: ASRPartialTopic, EventVersion: ASRPartialEventVersion,
		SessionID: "session-1", TurnID: "turn-1", Text: "你好",
		SourceLanguage: "zh-CN", OccurredAt: time.Unix(1700000000, 0).UTC(),
	}
}
