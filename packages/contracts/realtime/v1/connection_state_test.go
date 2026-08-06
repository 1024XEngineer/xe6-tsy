package realtimev1

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"
)

var contractStates = []ConnectionState{
	ConnectionNew,
	ConnectionConnecting,
	ConnectionConnected,
	ConnectionDisconnected,
	ConnectionFailed,
	ConnectionClosed,
}

var contractErrorCodes = []ErrorCode{
	ErrorConnectionNotFound,
	ErrorConnectionNotReady,
	ErrorConnectionFailed,
}

func TestConnectionStateContract(t *testing.T) {
	for _, state := range contractStates {
		if !state.Valid() {
			t.Fatalf("ConnectionState(%q).Valid() = false", state)
		}
		if got := state.Ready(); got != (state == ConnectionConnected) {
			t.Fatalf("ConnectionState(%q).Ready() = %t", state, got)
		}
	}
	if ConnectionState("unknown").Valid() {
		t.Fatal("unknown connection state must be invalid")
	}
}

func TestOpenAPIWebRTCContractMatchesGoTypes(t *testing.T) {
	specPath := filepath.Join("..", "..", "openapi.yaml")
	specData, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("read OpenAPI spec: %v", err)
	}

	var spec openAPISpec
	if err := yaml.Unmarshal(specData, &spec); err != nil {
		t.Fatalf("parse OpenAPI spec: %v", err)
	}

	stateSchema := spec.Components.Schemas["WebRTCConnectionState"]
	if !reflect.DeepEqual(stateSchema.Enum, stringValues(contractStates)) {
		t.Fatalf("WebRTCConnectionState enum = %v, want %v", stateSchema.Enum, contractStates)
	}
	errorSchema := spec.Components.Schemas["WebRTCErrorCode"]
	if !reflect.DeepEqual(errorSchema.Enum, stringValues(contractErrorCodes)) {
		t.Fatalf("WebRTCErrorCode enum = %v, want %v", errorSchema.Enum, contractErrorCodes)
	}

	snapshot := spec.Components.Schemas["WebRTCConnectionSnapshot"]
	wantFields := []string{"session_id", "connection_id", "state", "version", "updated_at"}
	if !reflect.DeepEqual(snapshot.Required, wantFields) {
		t.Fatalf("WebRTCConnectionSnapshot required = %v, want %v", snapshot.Required, wantFields)
	}
	if got := snapshot.Properties["state"].Ref; got != "#/components/schemas/WebRTCConnectionState" {
		t.Fatalf("WebRTCConnectionSnapshot.state ref = %q", got)
	}

	connectionPath := spec.Paths["/realtime/v1/sessions/{session_id}/connection"]
	okResponse := connectionPath.Get.Responses["200"]
	if got := okResponse.Content["application/json"].Schema.Ref; got != "#/components/schemas/WebRTCConnectionSnapshot" {
		t.Fatalf("connection 200 schema ref = %q", got)
	}
	notFoundResponse := connectionPath.Get.Responses["404"]
	if got := notFoundResponse.Content["application/json"].Schema.Ref; got != "#/components/schemas/WebRTCConnectionError" {
		t.Fatalf("connection 404 schema ref = %q", got)
	}
	errorBody := spec.Components.Schemas["WebRTCConnectionErrorBody"]
	if got := errorBody.Properties["code"].Ref; got != "#/components/schemas/WebRTCErrorCode" {
		t.Fatalf("WebRTCConnectionErrorBody.code ref = %q", got)
	}
}

func stringValues[T ~string](values []T) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = string(value)
	}
	return result
}

type openAPISpec struct {
	Paths map[string]struct {
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

type openAPISchema struct {
	Type         string                     `yaml:"type"`
	Scheme       string                     `yaml:"scheme"`
	BearerFormat string                     `yaml:"bearerFormat"`
	Enum         []string                   `yaml:"enum"`
	Required     []string                   `yaml:"required"`
	Properties   map[string]openAPIProperty `yaml:"properties"`
}

type openAPIProperty struct {
	Ref string `yaml:"$ref"`
}
