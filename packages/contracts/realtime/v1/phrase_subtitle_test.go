package realtimev1

import (
	"errors"
	"testing"
	"time"
)

func TestPhraseSubtitleEventValidation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*PhraseSubtitleEvent)
	}{
		{name: "type", mutate: func(event *PhraseSubtitleEvent) { event.Type = "subtitle.phrase" }},
		{name: "version", mutate: func(event *PhraseSubtitleEvent) { event.EventVersion = 2 }},
		{name: "utterance", mutate: func(event *PhraseSubtitleEvent) { event.UtteranceID = "" }},
		{name: "sequence", mutate: func(event *PhraseSubtitleEvent) { event.PhraseSequence = 0 }},
		{name: "source", mutate: func(event *PhraseSubtitleEvent) { event.SourceText = "" }},
		{name: "status", mutate: func(event *PhraseSubtitleEvent) { event.Status = "pending" }},
		{name: "translated without translation status", mutate: func(event *PhraseSubtitleEvent) { event.TranslatedText = "Hello" }},
		{name: "translated missing text", mutate: func(event *PhraseSubtitleEvent) { event.Status = PhraseSubtitleTranslated }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			event := validPhraseSubtitleEvent()
			test.mutate(&event)
			if err := event.Validate(); !errors.Is(err, ErrInvalidPhraseSubtitleEvent) {
				t.Fatalf("Validate() error = %v, want ErrInvalidPhraseSubtitleEvent", err)
			}
		})
	}

	if err := validPhraseSubtitleEvent().Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestPhraseSubtitleEventAllowsTranslatedState(t *testing.T) {
	event := validPhraseSubtitleEvent()
	event.Status = PhraseSubtitleTranslated
	event.TranslatedText = "Hello"
	if err := event.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func validPhraseSubtitleEvent() PhraseSubtitleEvent {
	return PhraseSubtitleEvent{
		Type: PhraseSubtitleTopic, EventVersion: PhraseSubtitleEventVersion,
		SessionID: "session-1", UtteranceID: "turn-1", PhraseSequence: 1,
		SourceText: "你好", Status: PhraseSubtitleSourceStable,
		OccurredAt: time.Unix(1700000000, 0).UTC(),
	}
}
