package recordsv1

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestOpenAPIDefinesRecordModuleContract(t *testing.T) {
	specPath := filepath.Join("..", "..", "openapi", "voice-records.v1.yaml")
	spec, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("read OpenAPI spec: %v", err)
	}

	text := string(spec)
	for _, expected := range []string{
		"openapi: 3.1.0",
		"/api/v1/voice-sessions/{id}/participants:",
		"/api/v1/voice-sessions/{id}/participants/{participant_id}:",
		"/api/v1/voice-sessions/{id}/turns:",
		"/api/v1/voice-turns/{id}:",
		"/api/v1/voice-turns/{id}/attribution:",
		"/api/v1/translation-history:",
		"name: cursor",
		"name: limit",
		"next_cursor:",
		"language_config_version:",
		"securitySchemes:",
		"accountContext:",
		"systemContext:",
		"name: X-Lingow-System-Token",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("OpenAPI spec does not contain %q", expected)
		}
	}

	if strings.Contains(text, "name: account_id") {
		t.Fatal("OpenAPI spec must not accept account_id as a public query parameter")
	}
	if strings.Contains(text, "post:") {
		t.Fatal("OpenAPI spec must not expose public participant or turn creation")
	}
}

func TestOpenAPIRequiresAccountContextByDefault(t *testing.T) {
	specPath := filepath.Join("..", "..", "openapi", "voice-records.v1.yaml")
	spec, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("read OpenAPI spec: %v", err)
	}

	text := string(spec)
	pathsIndex := strings.Index(text, "paths:")
	if pathsIndex == -1 {
		t.Fatal("OpenAPI spec is missing paths")
	}

	preamble := text[:pathsIndex]
	if !strings.Contains(preamble, "security:") ||
		!strings.Contains(preamble, "- accountContext: []") {
		t.Fatal("OpenAPI spec must require accountContext by default")
	}
}

func TestOpenAPIPatchRequiresBothAccountAndSystemSecurity(t *testing.T) {
	specPath := filepath.Join("..", "..", "openapi", "voice-records.v1.yaml")
	spec, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("read OpenAPI spec: %v", err)
	}

	text := string(spec)
	for _, operation := range []struct {
		marker string
	}{
		{marker: "operationId: updateSessionParticipant"},
		{marker: "operationId: updateVoiceTurnAttribution"},
	} {
		start := strings.Index(text, operation.marker)
		if start == -1 {
			t.Fatalf("OpenAPI spec is missing %q", operation.marker)
		}
		segment := text[start : start+400]
		if !strings.Contains(segment, "accountContext: []") || !strings.Contains(segment, "systemContext: []") {
			t.Fatalf("PATCH operation %q must require both account and system security: %q", operation.marker, segment)
		}
	}

	if !strings.Contains(text, "- conflict") {
		t.Fatal("OpenAPI ErrorCode enum must include the conflict code")
	}
	if !strings.Contains(text, "'409'") || !strings.Contains(text, "responses/Conflict") {
		t.Fatal("OpenAPI participant PATCH must declare the 409 Conflict response")
	}
}

func TestOpenAPIAttributionRequestExcludesServerManagedFields(t *testing.T) {
	specPath := filepath.Join("..", "..", "openapi", "voice-records.v1.yaml")
	spec, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("read OpenAPI spec: %v", err)
	}

	text := string(spec)
	start := strings.Index(text, "UpdateAttributionRequest:")
	end := strings.Index(text, "ErrorCode:")
	if start == -1 || end == -1 || start >= end {
		t.Fatal("OpenAPI spec is missing the update attribution or error code schema")
	}

	requestSchema := text[start:end]
	for _, forbidden := range []string{"corrected_by:", "corrected_at:"} {
		if strings.Contains(requestSchema, forbidden) {
			t.Fatalf("UpdateAttributionRequest must not accept %q", strings.TrimSuffix(forbidden, ":"))
		}
	}
}

func TestOpenAPICorrectedByAllowsJSONNull(t *testing.T) {
	specPath := filepath.Join("..", "..", "openapi", "voice-records.v1.yaml")
	spec, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("read OpenAPI spec: %v", err)
	}

	text := string(spec)
	start := strings.Index(text, "corrected_by:")
	if start == -1 {
		t.Fatal("OpenAPI spec is missing the corrected_by schema")
	}
	end := strings.Index(text[start:], "started_at:")
	if end == -1 {
		t.Fatal("OpenAPI spec is missing the corrected_by schema end")
	}

	correctedBySchema := text[start : start+end]
	if !strings.Contains(correctedBySchema, "enum: [system, null]") {
		t.Fatal("corrected_by must allow the JSON null value")
	}
	if strings.Contains(correctedBySchema, "enum: [system, 'null']") {
		t.Fatal("corrected_by enum must not use the string literal 'null'")
	}
}

func TestOpenAPIErrorDetailsUseTypedFieldSchema(t *testing.T) {
	specPath := filepath.Join("..", "..", "openapi", "voice-records.v1.yaml")
	data, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("read OpenAPI spec: %v", err)
	}
	var spec map[string]any
	if err := yaml.Unmarshal(data, &spec); err != nil {
		t.Fatalf("parse OpenAPI spec: %v", err)
	}

	schemas := mapValue(t, mapValue(t, spec, "components"), "schemas")
	apiError := mapValue(t, schemas, "APIError")
	if _, required := stringSet(t, apiError["required"])["details"]; required {
		t.Fatal("APIError details must remain optional")
	}
	detailsRef, ok := mapValue(t, apiError, "properties")["details"].(map[string]any)
	if !ok || detailsRef["$ref"] != "#/components/schemas/APIErrorDetails" {
		t.Fatalf("APIError details = %#v, want APIErrorDetails reference", detailsRef)
	}
	details := mapValue(t, schemas, "APIErrorDetails")
	if !stringSet(t, details["required"])["field"] || mapValue(t, details, "properties")["field"].(map[string]any)["type"] != "string" {
		t.Fatalf("APIErrorDetails = %#v, want required string field", details)
	}
}

func TestOpenAPISpeakerPendingBelongsToVoiceTurns(t *testing.T) {
	specPath := filepath.Join("..", "..", "openapi", "voice-records.v1.yaml")
	data, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("read OpenAPI spec: %v", err)
	}
	var spec map[string]any
	if err := yaml.Unmarshal(data, &spec); err != nil {
		t.Fatalf("parse OpenAPI spec: %v", err)
	}
	schemas := mapValue(t, mapValue(t, spec, "components"), "schemas")
	participantDescription := mapValue(t, mapValue(t, schemas, "Participant"), "properties")["speaker_code"].(map[string]any)["description"].(string)
	turnDescription := mapValue(t, mapValue(t, schemas, "VoiceTurn"), "properties")["speaker_code"].(map[string]any)["description"].(string)
	if strings.Contains(participantDescription, "speaker_pending") {
		t.Fatalf("Participant speaker_code description = %q", participantDescription)
	}
	if !strings.Contains(turnDescription, "speaker_pending") {
		t.Fatalf("VoiceTurn speaker_code description = %q", turnDescription)
	}
}

func TestPublicRecordSchemasExcludeInternalSpeechProfileIDs(t *testing.T) {
	voiceRecordsData, err := os.ReadFile(filepath.Join("..", "..", "openapi", "voice-records.v1.yaml"))
	if err != nil {
		t.Fatalf("read voice-records OpenAPI spec: %v", err)
	}
	var voiceRecords map[string]any
	if err := yaml.Unmarshal(voiceRecordsData, &voiceRecords); err != nil {
		t.Fatalf("parse voice-records OpenAPI spec: %v", err)
	}
	voiceTurnProperties := mapValue(t, mapValue(t, mapValue(t, voiceRecords, "components"), "schemas"), "VoiceTurn")
	properties := mapValue(t, voiceTurnProperties, "properties")
	for _, field := range []string{"asr_profile_id", "tts_profile_id"} {
		if _, exists := properties[field]; exists {
			t.Fatalf("public VoiceTurn must not expose %q", field)
		}
	}

	rootData, err := os.ReadFile(filepath.Join("..", "..", "openapi.yaml"))
	if err != nil {
		t.Fatalf("read root OpenAPI spec: %v", err)
	}
	var root map[string]any
	if err := yaml.Unmarshal(rootData, &root); err != nil {
		t.Fatalf("parse root OpenAPI spec: %v", err)
	}
	finalTurnSnapshot := mapValue(t, mapValue(t, mapValue(t, root, "components"), "schemas"), "FinalTurnSnapshot")
	for _, field := range []string{"asr_profile_id", "tts_profile_id"} {
		if _, exists := mapValue(t, finalTurnSnapshot, "properties")[field]; exists {
			t.Fatalf("public FinalTurnSnapshot must not expose %q", field)
		}
	}
}
