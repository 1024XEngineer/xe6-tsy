package pipeline

import (
	"reflect"
	"testing"
)

func TestUsageFactMatchesUsageRecordedV1Shape(t *testing.T) {
	if UsageEventVersion != 1 {
		t.Fatalf("UsageEventVersion = %d, want 1", UsageEventVersion)
	}

	typeOfFact := reflect.TypeOf(UsageFact{})
	eventVersion, ok := typeOfFact.FieldByName("EventVersion")
	if !ok || eventVersion.Type.Kind() != reflect.Int || eventVersion.Tag.Get("json") != "event_version" {
		t.Fatalf("EventVersion field = %#v, want int with event_version JSON tag", eventVersion)
	}

	wantTags := map[string]string{
		"ID": "id", "TraceID": "trace_id", "IdempotencyKey": "idempotency_key",
		"AccountID": "account_id", "SessionID": "session_id", "TurnID": "turn_id",
		"ServiceType": "service_type", "Provider": "provider", "Model": "model",
		"InputTokens": "input_tokens", "OutputTokens": "output_tokens", "AudioDurationMS": "audio_duration_ms",
		"CostAmount": "cost_amount", "Currency": "currency", "OccurredAt": "occurred_at",
	}
	for fieldName, wantTag := range wantTags {
		field, ok := typeOfFact.FieldByName(fieldName)
		if !ok || field.Tag.Get("json") != wantTag {
			t.Fatalf("%s JSON tag = %q, want %q", fieldName, field.Tag.Get("json"), wantTag)
		}
	}
}
