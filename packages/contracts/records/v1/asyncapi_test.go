package recordsv1

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestAsyncAPIFinalTurnContract(t *testing.T) {
	spec := readAsyncAPI(t)
	if got, want := spec["asyncapi"], "3.0.0"; got != want {
		t.Fatalf("AsyncAPI version = %v, want %q", got, want)
	}
	channels := mapValue(t, spec, "channels")
	finalTurnChannel := mapValue(t, channels, "finalTurn")
	if got, want := finalTurnChannel["address"], FinalTurnTopic; got != want {
		t.Fatalf("final turn topic = %v, want %q", got, want)
	}

	schemas := mapValue(t, mapValue(t, spec, "components"), "schemas")
	schema := mapValue(t, schemas, "FinalTurnEvent")
	required := stringSet(t, schema["required"])
	for _, field := range []string{
		"event_version", "event_id", "turn_id", "session_id", "participant_id",
		"sequence_no", "language_config_version", "attribution_status", "occurred_at",
	} {
		if !required[field] {
			t.Fatalf("FinalTurnEvent does not require %q", field)
		}
	}

	properties := mapValue(t, schema, "properties")
	if got, want := mapValue(t, properties, "event_version")["const"], FinalTurnEventVersion; got != want {
		t.Fatalf("event_version const = %v, want %d", got, want)
	}
	if got, want := mapValue(t, properties, "language_config_version")["minimum"], 1; got != want {
		t.Fatalf("language_config_version minimum = %v, want %d", got, want)
	}
	if got, want := mapValue(t, properties, "speaker_code")["minLength"], 1; got != want {
		t.Fatalf("speaker_code minLength = %v, want %d", got, want)
	}
	if description := mapValue(t, properties, "speaker_code")["description"].(string); !strings.Contains(description, "speaker_pending") {
		t.Fatalf("speaker_code description = %q, want pending code semantics", description)
	}
	if !allowsNull(t, mapValue(t, properties, "participant_id")) {
		t.Fatal("participant_id must allow null")
	}
	if !allowsNull(t, mapValue(t, properties, "speaker_label_snapshot")) {
		t.Fatal("speaker_label_snapshot must allow null")
	}
	if !allowsNull(t, mapValue(t, properties, "provider_speaker_id")) {
		t.Fatal("provider_speaker_id must allow null")
	}
	if required["provider_speaker_id"] {
		t.Fatal("provider_speaker_id must not be required")
	}
	statuses := stringSet(t, mapValue(t, schemas, "AttributionStatus")["enum"])
	if got, want := len(statuses), 4; got != want {
		t.Fatalf("AttributionStatus enum length = %d, want %d", got, want)
	}
	for _, status := range []AttributionStatus{
		AttributionPending,
		AttributionProvisional,
		AttributionConfirmed,
		AttributionCorrected,
	} {
		if !statuses[string(status)] {
			t.Fatalf("AttributionStatus does not allow %q", status)
		}
	}
}

func TestAsyncAPIFinalTurnPayloadExamples(t *testing.T) {
	schema := finalTurnSchema(t)

	valid := map[string]any{
		"event_version":           FinalTurnEventVersion,
		"event_id":                "evt_01",
		"trace_id":                "trace_01",
		"turn_id":                 "turn_01",
		"session_id":              "session_01",
		"participant_id":          nil,
		"sequence_no":             int64(1),
		"source_language":         "zh-CN",
		"target_language":         "en-US",
		"language_config_version": int64(1),
		"source_text":             "source",
		"translated_text":         "translation",
		"speaker_code":            "speaker_01",
		"speaker_label_snapshot":  nil,
		"provider_speaker_id":     "diar_01",
		"speaker_confidence":      nil,
		"attribution_status":      string(AttributionPending),
		"started_at":              "2026-07-27T08:00:00Z",
		"ended_at":                "2026-07-27T08:00:01Z",
		"occurred_at":             "2026-07-27T08:00:02Z",
	}
	if err := validateFinalTurnPayload(schema, valid); err != nil {
		t.Fatalf("valid payload rejected: %v", err)
	}

	tests := []struct {
		name    string
		mutate  func(map[string]any)
		wantErr string
	}{
		{
			name: "missing required field",
			mutate: func(payload map[string]any) {
				delete(payload, "translated_text")
			},
			wantErr: "translated_text is required",
		},
		{
			name: "invalid attribution status",
			mutate: func(payload map[string]any) {
				payload["attribution_status"] = "partial"
			},
			wantErr: "attribution_status",
		},
		{
			name: "invalid event version",
			mutate: func(payload map[string]any) {
				payload["event_version"] = 2
			},
			wantErr: "event_version",
		},
		{
			name: "invalid language configuration version",
			mutate: func(payload map[string]any) {
				payload["language_config_version"] = int64(0)
			},
			wantErr: "language_config_version",
		},
		{
			name: "empty speaker code",
			mutate: func(payload map[string]any) {
				payload["speaker_code"] = ""
			},
			wantErr: "speaker_code",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			payload := clonePayload(valid)
			test.mutate(payload)
			err := validateFinalTurnPayload(schema, payload)
			if err == nil {
				t.Fatal("invalid payload accepted")
			}
			if !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("validation error = %q, want substring %q", err, test.wantErr)
			}
		})
	}
}

func finalTurnSchema(t *testing.T) map[string]any {
	t.Helper()
	spec := readAsyncAPI(t)
	return mapValue(t, mapValue(t, mapValue(t, spec, "components"), "schemas"), "FinalTurnEvent")
}

func readAsyncAPI(t *testing.T) map[string]any {
	t.Helper()
	path := filepath.Join("..", "..", "events.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read AsyncAPI spec: %v", err)
	}

	var spec map[string]any
	if err := yaml.Unmarshal(data, &spec); err != nil {
		t.Fatalf("parse AsyncAPI spec: %v", err)
	}
	return spec
}

func validateFinalTurnPayload(schema, payload map[string]any) error {
	required, err := stringSetValue(schema["required"])
	if err != nil {
		return err
	}
	for field := range required {
		if _, ok := payload[field]; !ok {
			return fmt.Errorf("%s is required", field)
		}
	}

	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		return fmt.Errorf("FinalTurnEvent properties are invalid")
	}
	if got, want := payload["event_version"], properties["event_version"].(map[string]any)["const"]; got != want {
		return fmt.Errorf("event_version = %v, want %v", got, want)
	}
	if version, ok := payload["language_config_version"].(int64); !ok || version < 1 {
		return fmt.Errorf("language_config_version must be at least 1")
	}
	if status, ok := payload["attribution_status"].(string); !ok || !isAttributionStatus(status) {
		return fmt.Errorf("attribution_status is invalid")
	}
	if speakerCode, ok := payload["speaker_code"].(string); !ok || speakerCode == "" {
		return fmt.Errorf("speaker_code must not be empty")
	}
	for _, field := range []string{"participant_id", "speaker_label_snapshot", "speaker_confidence"} {
		if payload[field] == nil && !allowsNullValue(properties[field]) {
			return fmt.Errorf("%s must not be null", field)
		}
	}
	return nil
}

func clonePayload(payload map[string]any) map[string]any {
	clone := make(map[string]any, len(payload))
	for key, value := range payload {
		clone[key] = value
	}
	return clone
}

func isAttributionStatus(value string) bool {
	switch AttributionStatus(value) {
	case AttributionPending, AttributionProvisional, AttributionConfirmed, AttributionCorrected:
		return true
	default:
		return false
	}
}

func mapValue(t *testing.T, values map[string]any, key string) map[string]any {
	t.Helper()
	value, ok := values[key].(map[string]any)
	if !ok {
		t.Fatalf("%q is not an object", key)
	}
	return value
}

func stringSet(t *testing.T, value any) map[string]bool {
	t.Helper()
	set, err := stringSetValue(value)
	if err != nil {
		t.Fatal(err)
	}
	return set
}

func stringSetValue(value any) (map[string]bool, error) {
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("expected an array")
	}
	set := make(map[string]bool, len(items))
	for _, item := range items {
		text, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("array item %v is not a string", item)
		}
		set[text] = true
	}
	return set, nil
}

func allowsNull(t *testing.T, schema map[string]any) bool {
	t.Helper()
	return allowsNullValue(schema)
}

func allowsNullValue(value any) bool {
	schema, ok := value.(map[string]any)
	if !ok {
		return false
	}
	types, ok := schema["type"].([]any)
	if !ok {
		return false
	}
	for _, value := range types {
		if value == "null" {
			return true
		}
	}
	return false
}
