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
				Responses map[string]struct {
					Content map[string]struct {
						Schema openAPIProperty `yaml:"schema"`
					} `yaml:"content"`
				} `yaml:"responses"`
			} `yaml:"post"`
			Get struct {
				Responses map[string]struct {
					Content map[string]struct {
						Schema openAPIProperty `yaml:"schema"`
					} `yaml:"content"`
				} `yaml:"responses"`
			} `yaml:"get"`
		} `yaml:"paths"`
		Components struct {
			Schemas map[string]openAPISchema `yaml:"schemas"`
		} `yaml:"components"`
	}
	if err := yaml.Unmarshal(specData, &spec); err != nil {
		t.Fatalf("parse OpenAPI spec: %v", err)
	}

	controlPlaneCodes := spec.Components.Schemas["ControlPlaneErrorCode"].Enum
	if want := []string{"runtime_operation_conflict"}; !reflect.DeepEqual(controlPlaneCodes, want) {
		t.Fatalf("ControlPlaneErrorCode enum = %v, want %v", controlPlaneCodes, want)
	}
	for _, code := range spec.Components.Schemas["WebRTCErrorCode"].Enum {
		if code == string(ErrorRuntimeOperationConflict) {
			t.Fatal("WebRTCErrorCode must not contain runtime_operation_conflict")
		}
	}

	start := spec.Paths["/realtime/v1/sessions/{session_id}/start"]
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
	if got := bodySchema.Properties["code"].Ref; got != "#/components/schemas/ControlPlaneErrorCode" {
		t.Fatalf("RuntimeOperationConflictErrorBody.code ref = %q", got)
	}

	stop := spec.Paths["/realtime/v1/sessions/{session_id}/stop"]
	if got := stop.Post.RequestBody.Content["application/json"].Schema.Ref; got != "#/components/schemas/RealtimeStopRequest" {
		t.Fatalf("Stop request schema ref = %q", got)
	}
	if got := stop.Post.Responses["200"].Content["application/json"].Schema.Ref; got != "#/components/schemas/RealtimeRuntimeSnapshot" {
		t.Fatalf("Stop 200 schema ref = %q", got)
	}
	runtime := spec.Paths["/realtime/v1/sessions/{session_id}/runtime"]
	if got := runtime.Get.Responses["200"].Content["application/json"].Schema.Ref; got != "#/components/schemas/RealtimeRuntimeSnapshot" {
		t.Fatalf("Runtime 200 schema ref = %q", got)
	}
	stopSchema := spec.Components.Schemas["RealtimeStopRequest"]
	wantFields := []string{"reason", "ended_at"}
	if !reflect.DeepEqual(stopSchema.Required, wantFields) {
		t.Fatalf("RealtimeStopRequest required = %v, want %v", stopSchema.Required, wantFields)
	}
}
