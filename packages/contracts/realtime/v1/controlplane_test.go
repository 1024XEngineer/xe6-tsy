package realtimev1

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestControlPlaneErrorCodes(t *testing.T) {
	var code ControlPlaneErrorCode = ErrorRuntimeOperationConflict
	if got := string(code); got != "runtime_operation_conflict" {
		t.Fatalf("ErrorRuntimeOperationConflict = %q", got)
	}
}

func TestStartRequestCarriesDurableOperationID(t *testing.T) {
	encoded, err := json.Marshal(StartRequest{
		OperationID: "operation-1",
		TraceID:     "trace-1",
		StartedBy:   "account-1",
	})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	for _, field := range []string{
		`"operation_id":"operation-1"`,
		`"trace_id":"trace-1"`,
		`"started_by":"account-1"`,
	} {
		if !strings.Contains(string(encoded), field) {
			t.Fatalf("StartRequest JSON = %s, missing %s", encoded, field)
		}
	}
}

func TestStopRequestCarriesEndIntentFields(t *testing.T) {
	endedAt := time.Unix(1700000060, 0).UTC()
	encoded, err := json.Marshal(StopRequest{
		TraceID: "trace-1",
		Reason:  "user_requested",
		EndedAt: endedAt,
	})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	for _, field := range []string{
		`"trace_id":"trace-1"`,
		`"reason":"user_requested"`,
		`"ended_at":"2023-11-14T22:14:20Z"`,
	} {
		if !strings.Contains(string(encoded), field) {
			t.Fatalf("StopRequest JSON = %s, missing %s", encoded, field)
		}
	}
}

func TestFallbackPlaybackRequestCarriesImmutableTurnSnapshot(t *testing.T) {
	encoded, err := json.Marshal(FallbackPlaybackRequest{
		OperationID:           "fallback-1",
		SessionID:             "session-1",
		TurnID:                "turn-1",
		TargetLanguage:        "zh-CN",
		TranslatedText:        "translated text",
		LanguageConfigVersion: 3,
		TraceID:               "trace-1",
	})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	for _, field := range []string{
		`"operation_id":"fallback-1"`,
		`"session_id":"session-1"`,
		`"turn_id":"turn-1"`,
		`"target_language":"zh-CN"`,
		`"translated_text":"translated text"`,
		`"language_config_version":3`,
		`"trace_id":"trace-1"`,
	} {
		if !strings.Contains(string(encoded), field) {
			t.Fatalf("FallbackPlaybackRequest JSON = %s, missing %s", encoded, field)
		}
	}

	receipt := FallbackPlaybackReceipt{OperationID: "fallback-1", Status: FallbackPlaybackAlreadyAccepted}
	if receipt.Status != "already_accepted" {
		t.Fatalf("receipt status = %q", receipt.Status)
	}
}

func TestOpenAPIControlPlaneErrorContract(t *testing.T) {
	specData, err := os.ReadFile(filepath.Join("..", "..", "openapi.yaml"))
	if err != nil {
		t.Fatalf("read OpenAPI spec: %v", err)
	}

	var spec struct {
		Paths map[string]struct {
			Post struct {
				RequestBody struct {
					Content map[string]struct {
						Schema openAPIProperty `yaml:"schema"`
					} `yaml:"content"`
				} `yaml:"requestBody"`
				Security  []map[string][]string `yaml:"security"`
				Responses map[string]struct {
					Ref     string `yaml:"$ref"`
					Content map[string]struct {
						Schema openAPIProperty `yaml:"schema"`
					} `yaml:"content"`
				} `yaml:"responses"`
			} `yaml:"post"`
			Get struct {
				Responses map[string]struct {
					Ref     string `yaml:"$ref"`
					Content map[string]struct {
						Schema openAPIProperty `yaml:"schema"`
					} `yaml:"content"`
				} `yaml:"responses"`
				Security []map[string][]string `yaml:"security"`
			} `yaml:"get"`
		} `yaml:"paths"`
		Components struct {
			SecuritySchemes map[string]openAPISchema `yaml:"securitySchemes"`
			Schemas         map[string]openAPISchema `yaml:"schemas"`
			Responses       map[string]struct {
				Content map[string]struct {
					Schema openAPIProperty `yaml:"schema"`
				} `yaml:"content"`
			} `yaml:"responses"`
		} `yaml:"components"`
	}
	if err := yaml.Unmarshal(specData, &spec); err != nil {
		t.Fatalf("parse OpenAPI spec: %v", err)
	}

	controlPlaneCodes := spec.Components.Schemas["ControlPlaneErrorCode"].Enum
	if want := []string{
		"runtime_not_found",
		"runtime_operation_conflict",
		"mode_not_available",
		"mode_generation_conflict",
		"mode_runtime_instance_mismatch",
		"mode_operation_conflict",
		"control_invalid_message",
		"control_unsupported_version",
		"control_unsupported_type",
		"control_unauthorized_session",
		"control_connection_closed",
		"control_unavailable",
	}; !reflect.DeepEqual(controlPlaneCodes, want) {
		t.Fatalf("ControlPlaneErrorCode enum = %v, want %v", controlPlaneCodes, want)
	}
	for _, code := range spec.Components.Schemas["WebRTCErrorCode"].Enum {
		if code == string(ErrorRuntimeOperationConflict) {
			t.Fatal("WebRTCErrorCode must not contain runtime_operation_conflict")
		}
	}
	ticket := spec.Components.SecuritySchemes["realtimeTicket"]
	if ticket.Type != "http" || ticket.Scheme != "bearer" || ticket.BearerFormat != "" {
		t.Fatalf("realtimeTicket = %#v, want non-JWT HTTP bearer scheme", ticket)
	}

	start := spec.Paths["/realtime/v1/sessions/{session_id}/start"]
	assertRealtimeSecurity(t, "start", start.Post.Security)
	if _, ok := start.Post.Responses["401"]; !ok {
		t.Fatal("Start must declare 401 Unauthorized")
	}
	conflict := start.Post.Responses["409"]
	schema := conflict.Content["application/json"].Schema
	if want := "#/components/schemas/RuntimeOperationConflictError"; schema.Ref != want {
		t.Fatalf("Start 409 schema ref = %q, want %q", schema.Ref, want)
	}
	errorSchema := spec.Components.Schemas["RuntimeOperationConflictError"]
	if got := errorSchema.Properties["error"].Ref; got != "#/components/schemas/RuntimeOperationConflictErrorBody" {
		t.Fatalf("RuntimeOperationConflictError.error ref = %q", got)
	}
	bodySchema := spec.Components.Schemas["RuntimeOperationConflictErrorBody"]
	if got := bodySchema.Properties["code"].Enum; !reflect.DeepEqual(got, []string{"runtime_operation_conflict"}) {
		t.Fatalf("RuntimeOperationConflictErrorBody.code enum = %v", got)
	}

	stop := spec.Paths["/realtime/v1/sessions/{session_id}/stop"]
	assertRealtimeSecurity(t, "stop", stop.Post.Security)
	if _, ok := stop.Post.Responses["401"]; !ok {
		t.Fatal("Stop must declare 401 Unauthorized")
	}
	if got := stop.Post.RequestBody.Content["application/json"].Schema.Ref; got != "#/components/schemas/RealtimeStopRequest" {
		t.Fatalf("Stop request schema ref = %q", got)
	}
	if got := stop.Post.Responses["200"].Content["application/json"].Schema.Ref; got != "#/components/schemas/RealtimeRuntimeSnapshot" {
		t.Fatalf("Stop 200 schema ref = %q", got)
	}
	runtime := spec.Paths["/realtime/v1/sessions/{session_id}/runtime"]
	assertRealtimeSecurity(t, "runtime", runtime.Get.Security)
	if _, ok := runtime.Get.Responses["401"]; !ok {
		t.Fatal("Runtime must declare 401 Unauthorized")
	}
	if got := runtime.Get.Responses["200"].Content["application/json"].Schema.Ref; got != "#/components/schemas/RealtimeRuntimeSnapshot" {
		t.Fatalf("Runtime 200 schema ref = %q", got)
	}
	mode := spec.Paths["/realtime/v1/sessions/{session_id}/mode"]
	for method, response := range map[string]struct {
		Ref string
	}{
		"GET":  {Ref: mode.Get.Responses["404"].Ref},
		"POST": {Ref: mode.Post.Responses["404"].Ref},
	} {
		if want := "#/components/responses/RuntimeNotFound"; response.Ref != want {
			t.Fatalf("Mode %s 404 response ref = %q, want %q", method, response.Ref, want)
		}
	}
	if got := spec.Components.Responses["RuntimeNotFound"].Content["application/json"].Schema.Ref; got != "#/components/schemas/RuntimeNotFoundError" {
		t.Fatalf("RuntimeNotFound response schema ref = %q", got)
	}
	runtimeNotFound := spec.Components.Schemas["RuntimeNotFoundError"]
	if got := runtimeNotFound.Properties["error"].Ref; got != "#/components/schemas/RuntimeNotFoundErrorBody" {
		t.Fatalf("RuntimeNotFoundError.error ref = %q", got)
	}
	if got := spec.Components.Schemas["RuntimeNotFoundErrorBody"].Properties["code"].Enum; !reflect.DeepEqual(got, []string{"runtime_not_found"}) {
		t.Fatalf("RuntimeNotFoundErrorBody.code enum = %v", got)
	}
	connection := spec.Paths["/realtime/v1/sessions/{session_id}/connection"]
	assertRealtimeSecurity(t, "connection", connection.Get.Security)
	if _, ok := connection.Get.Responses["401"]; !ok {
		t.Fatal("Connection must declare 401 Unauthorized")
	}
	stopSchema := spec.Components.Schemas["RealtimeStopRequest"]
	wantFields := []string{"reason", "ended_at"}
	if !reflect.DeepEqual(stopSchema.Required, wantFields) {
		t.Fatalf("RealtimeStopRequest required = %v, want %v", stopSchema.Required, wantFields)
	}

	fallback := spec.Paths["/realtime/v1/sessions/{session_id}/fallback-playback"]
	assertRealtimeSecurity(t, "fallback playback", fallback.Post.Security)
	if got := fallback.Post.RequestBody.Content["application/json"].Schema.Ref; got != "#/components/schemas/FallbackPlaybackRequest" {
		t.Fatalf("Fallback playback request schema ref = %q", got)
	}
	if got := fallback.Post.Responses["202"].Content["application/json"].Schema.Ref; got != "#/components/schemas/FallbackPlaybackReceipt" {
		t.Fatalf("Fallback playback 202 schema ref = %q", got)
	}
	fallbackSchema := spec.Components.Schemas["FallbackPlaybackRequest"]
	wantFallbackFields := []string{"operation_id", "session_id", "turn_id", "target_language", "translated_text", "language_config_version", "trace_id"}
	if !reflect.DeepEqual(fallbackSchema.Required, wantFallbackFields) {
		t.Fatalf("FallbackPlaybackRequest required = %v, want %v", fallbackSchema.Required, wantFallbackFields)
	}
	if got := spec.Components.Schemas["FallbackPlaybackReceiptStatus"].Enum; !reflect.DeepEqual(got, []string{"accepted", "already_accepted"}) {
		t.Fatalf("FallbackPlaybackReceiptStatus enum = %v", got)
	}
}

func assertRealtimeSecurity(t *testing.T, operation string, security []map[string][]string) {
	t.Helper()
	if len(security) != 1 {
		t.Fatalf("%s security = %#v, want exactly realtimeTicket", operation, security)
	}
	if scopes, ok := security[0]["realtimeTicket"]; !ok || len(scopes) != 0 {
		t.Fatalf("%s security = %#v, want realtimeTicket with no scopes", operation, security)
	}
}
