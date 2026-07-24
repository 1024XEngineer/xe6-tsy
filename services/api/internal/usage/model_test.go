package usage

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestRecordInputUsesUsageFactContractFields(t *testing.T) {
	input := RecordInput{
		EventVersion:    UsageEventVersion,
		ID:              "usage-1",
		TraceID:         "trace-1",
		IdempotencyKey:  "usage-1",
		AccountID:       "account-1",
		SessionID:       "session-1",
		TurnID:          "turn-1",
		ServiceType:     StageTranslation,
		Provider:        "provider-placeholder",
		Model:           "model-placeholder",
		InputTokens:     10,
		OutputTokens:    8,
		AudioDurationMS: 0,
		CostAmount:      "0.010000",
		Currency:        "CNY",
		OccurredAt:      time.Date(2026, 7, 24, 1, 0, 0, 0, time.UTC),
	}

	payload, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	encoded := string(payload)
	for _, field := range []string{
		`"event_version":1`, `"id"`, `"trace_id"`, `"idempotency_key"`,
		`"account_id"`, `"session_id"`, `"turn_id"`, `"service_type"`,
		`"provider"`, `"model"`, `"occurred_at"`,
		`"input_tokens"`, `"output_tokens"`, `"audio_duration_ms"`,
		`"cost_amount"`, `"currency"`,
	} {
		if !strings.Contains(encoded, field) {
			t.Errorf("payload %s does not contain %s", encoded, field)
		}
	}
	for _, obsolete := range []string{`"stage"`, `"quantity"`, `"unit"`} {
		if strings.Contains(encoded, obsolete) {
			t.Errorf("payload %s contains obsolete field %s", encoded, obsolete)
		}
	}
}
