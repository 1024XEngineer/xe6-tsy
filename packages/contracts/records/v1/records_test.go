package recordsv1

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

type finalTurnSinkStub struct{}

func (finalTurnSinkStub) Publish(context.Context, FinalTurnEvent) error { return nil }

type finalTurnConsumerStub struct{}

func (finalTurnConsumerStub) ConsumeFinalTurn(context.Context, FinalTurnEvent) error { return nil }

type speakerAttributionReaderStub struct{}

func (speakerAttributionReaderStub) GetProvisionalAttribution(context.Context, SpeakerObservation) (SpeakerAttribution, error) {
	return SpeakerAttribution{}, nil
}

type turnReaderStub struct{}

func (turnReaderStub) ReadFinalTurns(context.Context, string, []string) ([]FinalTurnSnapshot, error) {
	return nil, nil
}

type sessionOwnerReaderStub struct{}

func (sessionOwnerReaderStub) AccountIDForSession(context.Context, string) (string, error) {
	return "", nil
}

var (
	_ FinalTurnSink            = finalTurnSinkStub{}
	_ FinalTurnConsumer        = finalTurnConsumerStub{}
	_ SpeakerAttributionReader = speakerAttributionReaderStub{}
	_ TurnReader               = turnReaderStub{}
	_ SessionOwnerReader       = sessionOwnerReaderStub{}
)

func TestFinalTurnEventJSONPreservesNullableAttribution(t *testing.T) {
	event := validFinalTurnEvent()

	body, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal final turn event: %v", err)
	}

	var actual map[string]any
	if err := json.Unmarshal(body, &actual); err != nil {
		t.Fatalf("unmarshal final turn event: %v", err)
	}

	if actual["participant_id"] != nil {
		t.Fatalf("participant_id = %v, want null", actual["participant_id"])
	}
	if actual["speaker_label_snapshot"] != nil {
		t.Fatalf("speaker_label_snapshot = %v, want null", actual["speaker_label_snapshot"])
	}
	if got, want := actual["language_config_version"], float64(3); got != want {
		t.Fatalf("language_config_version = %v, want %v", got, want)
	}
	if got, want := actual["event_version"], float64(FinalTurnEventVersion); got != want {
		t.Fatalf("event_version = %v, want %v", got, want)
	}
	if got, want := actual["attribution_status"], string(AttributionPending); got != want {
		t.Fatalf("attribution_status = %v, want %q", got, want)
	}
}

func TestFinalTurnEventValidatesRequiredFields(t *testing.T) {
	valid := validFinalTurnEvent()
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid FinalTurnEvent error = %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*FinalTurnEvent)
	}{
		{name: "event version", mutate: func(event *FinalTurnEvent) { event.EventVersion = 2 }},
		{name: "event id", mutate: func(event *FinalTurnEvent) { event.EventID = "" }},
		{name: "trace id", mutate: func(event *FinalTurnEvent) { event.TraceID = "" }},
		{name: "turn id", mutate: func(event *FinalTurnEvent) { event.TurnID = "" }},
		{name: "session id", mutate: func(event *FinalTurnEvent) { event.SessionID = "" }},
		{name: "sequence number", mutate: func(event *FinalTurnEvent) { event.SequenceNo = 0 }},
		{name: "source language", mutate: func(event *FinalTurnEvent) { event.SourceLanguage = "" }},
		{name: "target language", mutate: func(event *FinalTurnEvent) { event.TargetLanguage = "" }},
		{name: "source text", mutate: func(event *FinalTurnEvent) { event.SourceText = "" }},
		{name: "translated text", mutate: func(event *FinalTurnEvent) { event.TranslatedText = "" }},
		{name: "language config version", mutate: func(event *FinalTurnEvent) { event.LanguageConfigVersion = 0 }},
		{name: "attribution status", mutate: func(event *FinalTurnEvent) { event.AttributionStatus = "unknown" }},
		{name: "started at", mutate: func(event *FinalTurnEvent) { event.StartedAt = time.Time{} }},
		{name: "ended before start", mutate: func(event *FinalTurnEvent) { event.EndedAt = event.StartedAt.Add(-time.Second) }},
		{name: "occurred at", mutate: func(event *FinalTurnEvent) { event.OccurredAt = time.Time{} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			event := validFinalTurnEvent()
			test.mutate(&event)
			if err := event.Validate(); !errors.Is(err, ErrInvalidFinalTurnEvent) {
				t.Fatalf("Validate() error = %v, want ErrInvalidFinalTurnEvent", err)
			}
		})
	}
}

func validFinalTurnEvent() FinalTurnEvent {
	return FinalTurnEvent{
		EventVersion:          FinalTurnEventVersion,
		EventID:               "evt_01",
		TraceID:               "trace_01",
		TurnID:                "vt_01",
		SessionID:             "vs_01",
		SequenceNo:            1,
		SourceLanguage:        "zh-CN",
		TargetLanguage:        "en-US",
		LanguageConfigVersion: 3,
		SourceText:            "hello",
		TranslatedText:        "hello",
		SpeakerCode:           "speaker_01",
		AttributionStatus:     AttributionPending,
		StartedAt:             time.Date(2026, 7, 24, 8, 0, 0, 0, time.UTC),
		EndedAt:               time.Date(2026, 7, 24, 8, 0, 1, 0, time.UTC),
		OccurredAt:            time.Date(2026, 7, 24, 8, 0, 2, 0, time.UTC),
	}
}

func TestVoiceTurnJSONExposesPublicFieldNames(t *testing.T) {
	turn := VoiceTurn{
		ID:                    "vt_01",
		SessionID:             "vs_01",
		SpeakerCode:           "speaker_01",
		SequenceNo:            1,
		SourceLanguage:        "zh-CN",
		TargetLanguage:        "en-US",
		LanguageConfigVersion: 3,
		SourceText:            "hello",
		TranslatedText:        "hello",
		AttributionStatus:     AttributionProvisional,
		StartedAt:             time.Date(2026, 7, 24, 8, 0, 0, 0, time.UTC),
		EndedAt:               time.Date(2026, 7, 24, 8, 0, 1, 0, time.UTC),
		CreatedAt:             time.Date(2026, 7, 24, 8, 0, 2, 0, time.UTC),
	}

	body, err := json.Marshal(turn)
	if err != nil {
		t.Fatalf("marshal voice turn: %v", err)
	}

	var actual map[string]any
	if err := json.Unmarshal(body, &actual); err != nil {
		t.Fatalf("unmarshal voice turn: %v", err)
	}

	for _, field := range []string{
		"participant_id",
		"source_language",
		"target_language",
		"language_config_version",
		"attribution_status",
		"corrected_by",
	} {
		if _, ok := actual[field]; !ok {
			t.Fatalf("voice turn JSON does not include %q", field)
		}
	}
}

func TestListTurnsQueryJSONIncludesSessionID(t *testing.T) {
	query := ListTurnsQuery{SessionID: "vs_01"}

	body, err := json.Marshal(query)
	if err != nil {
		t.Fatalf("marshal list turns query: %v", err)
	}

	var actual map[string]any
	if err := json.Unmarshal(body, &actual); err != nil {
		t.Fatalf("unmarshal list turns query: %v", err)
	}

	if got, want := actual["session_id"], "vs_01"; got != want {
		t.Fatalf("session_id = %v, want %q", got, want)
	}
}
