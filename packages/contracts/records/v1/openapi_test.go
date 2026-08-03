package recordsv1

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
