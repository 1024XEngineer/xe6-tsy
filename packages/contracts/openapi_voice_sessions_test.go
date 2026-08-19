package contracts

import (
	"os"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestVoiceSessionLifecycleOpenAPI(t *testing.T) {
	spec := loadRootOpenAPI(t)
	paths := spec["paths"].(map[string]any)
	for _, path := range []string{
		"/voice-sessions",
		"/voice-sessions/{id}",
		"/voice-sessions/{id}/start",
		"/voice-sessions/{id}/end",
		"/voice-sessions/{id}/state",
		"/voice-sessions/{id}/mode",
		"/voice-sessions/{id}/realtime-ticket",
		"/languages",
		"/account/automatic-delivery-readiness",
		"/voice-sessions/{id}/language-config",
		"/voice-sessions/{id}/language-configs",
	} {
		if _, ok := paths[path]; !ok {
			t.Fatalf("missing voice-session path %s", path)
		}
	}
	for _, path := range []string{"/account/device-pairing-codes", "/account/devices", "/account/devices/{device_id}", "/devices/pair", "/device-auth/challenges", "/device-auth/tokens", "/device/voice-sessions", "/device/voice-sessions/{id}/start", "/device/voice-sessions/{id}/end", "/device/voice-sessions/{id}/realtime-ticket"} {
		if _, ok := paths[path]; !ok {
			t.Fatalf("missing device path %s", path)
		}
	}

	schemas := spec["components"].(map[string]any)["schemas"].(map[string]any)
	deviceToken := schemas["DeviceAccessToken"].(map[string]any)["properties"].(map[string]any)["access_token"].(map[string]any)
	if _, writeOnly := deviceToken["writeOnly"]; writeOnly {
		t.Fatal("DeviceAccessToken.access_token is returned by the token exchange and must not be writeOnly")
	}
	status := schemas["VoiceSessionStatus"].(map[string]any)
	wantStatuses := []any{"created", "active", "ended", "failed"}
	if got := status["enum"]; !sameStringSlice(got.([]any), wantStatuses) {
		t.Fatalf("VoiceSessionStatus enum = %v, want %v", got, wantStatuses)
	}

	listItem := schemas["VoiceSessionListItem"].(map[string]any)
	listProperties := listItem["properties"].(map[string]any)
	for _, forbidden := range []string{
		"runtime_state",
		"current_turn_id",
		"current_playback_id",
		"last_error_code",
		"retryable",
		"runtime_updated_at",
	} {
		if _, ok := listProperties[forbidden]; ok {
			t.Fatalf("VoiceSessionListItem contains runtime field %q", forbidden)
		}
	}

	detail := schemas["VoiceSessionDetail"].(map[string]any)
	detailProperties := detail["properties"].(map[string]any)
	for _, requiredProperty := range []string{
		"id",
		"account_id",
		"status",
		"runtime_state",
		"audio_config",
		"capabilities",
		"runtime_updated_at",
	} {
		if _, ok := detailProperties[requiredProperty]; !ok {
			t.Fatalf("VoiceSessionDetail properties missing %q", requiredProperty)
		}
	}

	state := schemas["VoiceSessionStateSnapshot"].(map[string]any)
	stateRequired := state["required"].([]any)
	for _, required := range []any{"session_id", "status", "runtime_state", "runtime_updated_at"} {
		if !containsString(stateRequired, required) {
			t.Fatalf("VoiceSessionStateSnapshot required = %v, missing %s", stateRequired, required)
		}
	}
	startPath := paths["/voice-sessions/{id}/start"].(map[string]any)
	startPost := startPath["post"].(map[string]any)
	startBody := startPost["requestBody"].(map[string]any)
	startContent := startBody["content"].(map[string]any)
	startMedia := startContent["application/json"].(map[string]any)
	startSchema := startMedia["schema"].(map[string]any)
	if got := startSchema["$ref"]; got != "#/components/schemas/StartVoiceSessionRequest" {
		t.Fatalf("start request schema = %v", got)
	}
	startRequest := schemas["StartVoiceSessionRequest"].(map[string]any)
	if required, ok := startRequest["required"]; ok && containsString(required.([]any), "initial_mode") {
		t.Fatal("StartVoiceSessionRequest.initial_mode must remain optional for legacy clients")
	}
	startProperties := startRequest["properties"].(map[string]any)
	initialMode := startProperties["initial_mode"].(map[string]any)
	if initialMode["$ref"] != "#/components/schemas/RealtimeMode" || initialMode["default"] != "interpretation" {
		t.Fatalf("initial_mode schema = %#v", initialMode)
	}

	modeRequest := schemas["SwitchVoiceSessionModeRequest"].(map[string]any)
	if got := modeRequest["required"].([]any); !sameStringSlice(got, []any{"runtime_instance_id", "expected_generation", "target_mode"}) {
		t.Fatalf("SwitchVoiceSessionModeRequest required = %v", got)
	}
	modePath := paths["/voice-sessions/{id}/mode"].(map[string]any)
	if got := modePath["get"].(map[string]any)["operationId"]; got != "getVoiceSessionMode" {
		t.Fatalf("GET mode operationId = %v", got)
	}
	if got := modePath["post"].(map[string]any)["operationId"]; got != "switchVoiceSessionMode" {
		t.Fatalf("POST mode operationId = %v", got)
	}

	outputMode := schemas["InterpretationOutputMode"].(map[string]any)
	if got := outputMode["enum"].([]any); !sameStringSlice(got, []any{"bidirectional", "single"}) {
		t.Fatalf("InterpretationOutputMode enum = %v", got)
	}
	request := schemas["CreateLanguageConfigRequest"].(map[string]any)
	if !containsString(request["required"].([]any), "languages") {
		t.Fatalf("CreateLanguageConfigRequest must require languages")
	}
	readiness := schemas["AutomaticDeliveryReadiness"].(map[string]any)
	if !containsString(readiness["required"].([]any), "ready") {
		t.Fatalf("AutomaticDeliveryReadiness must require ready")
	}
	createPath := paths["/voice-sessions/{id}/language-configs"].(map[string]any)
	post := createPath["post"].(map[string]any)
	responses := post["responses"].(map[string]any)
	unprocessable := responses["422"].(map[string]any)
	content := unprocessable["content"].(map[string]any)
	media := content["application/json"].(map[string]any)
	examples := media["examples"].(map[string]any)
	if _, ok := examples["deliveryTargetRequired"]; !ok {
		t.Fatalf("language config 422 response missing delivery target example")
	}
}

func loadRootOpenAPI(t *testing.T) map[string]any {
	t.Helper()
	raw, err := os.ReadFile("openapi.yaml")
	if err != nil {
		t.Fatalf("read openapi.yaml: %v", err)
	}
	var spec map[string]any
	if err := yaml.Unmarshal(raw, &spec); err != nil {
		t.Fatalf("parse openapi.yaml: %v", err)
	}
	return spec
}

func sameStringSlice(got []any, want []any) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func containsString(values []any, want any) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
