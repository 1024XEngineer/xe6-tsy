package pipeline

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
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

func TestUsageFactValidatesUsageRecordedV1Contract(t *testing.T) {
	valid := validUsageFact()
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid UsageFact error = %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*UsageFact)
	}{
		{name: "event version", mutate: func(fact *UsageFact) { fact.EventVersion = 2 }},
		{name: "id", mutate: func(fact *UsageFact) { fact.ID = "" }},
		{name: "trace id", mutate: func(fact *UsageFact) { fact.TraceID = "" }},
		{name: "idempotency key", mutate: func(fact *UsageFact) { fact.IdempotencyKey = "" }},
		{name: "long idempotency key", mutate: func(fact *UsageFact) { fact.IdempotencyKey = strings.Repeat("x", 201) }},
		{name: "account id", mutate: func(fact *UsageFact) { fact.AccountID = "" }},
		{name: "session id", mutate: func(fact *UsageFact) { fact.SessionID = "" }},
		{name: "turn id", mutate: func(fact *UsageFact) { fact.TurnID = "" }},
		{name: "service type", mutate: func(fact *UsageFact) { fact.ServiceType = "unknown" }},
		{name: "provider", mutate: func(fact *UsageFact) { fact.Provider = "" }},
		{name: "model", mutate: func(fact *UsageFact) { fact.Model = "" }},
		{name: "input tokens", mutate: func(fact *UsageFact) { fact.InputTokens = -1 }},
		{name: "output tokens", mutate: func(fact *UsageFact) { fact.OutputTokens = -1 }},
		{name: "audio duration", mutate: func(fact *UsageFact) { fact.AudioDurationMS = -1 }},
		{name: "cost amount", mutate: func(fact *UsageFact) { fact.CostAmount = "-1" }},
		{name: "currency", mutate: func(fact *UsageFact) { fact.Currency = "usd" }},
		{name: "occurred at", mutate: func(fact *UsageFact) { fact.OccurredAt = time.Time{} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fact := validUsageFact()
			test.mutate(&fact)
			if err := fact.Validate(); !errors.Is(err, ErrInvalidUsageFact) {
				t.Fatalf("Validate() error = %v, want ErrInvalidUsageFact", err)
			}
		})
	}
}

func TestUsageFactAcceptsAssistantLLMStage(t *testing.T) {
	fact := validUsageFact()
	fact.ServiceType = "assistant_llm"
	if err := fact.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func validUsageFact() UsageFact {
	return UsageFact{
		EventVersion: UsageEventVersion, ID: "usage-1", TraceID: "trace-1",
		IdempotencyKey: "usage:turn-1:asr", AccountID: "account-1", SessionID: "session-1",
		TurnID: "turn-1", ServiceType: "asr", Provider: "mock-asr", Model: "v1",
		OccurredAt: time.Unix(1700000000, 0).UTC(),
	}
}
