package recordsv1

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

type finalTurnSinkStub struct{}

func (finalTurnSinkStub) TryPublish(FinalTurnEvent) error { return nil }

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
	event := FinalTurnEvent{
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
	if got, want := actual["attribution_status"], string(AttributionPending); got != want {
		t.Fatalf("attribution_status = %v, want %q", got, want)
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
