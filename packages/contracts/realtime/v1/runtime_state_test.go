package realtimev1

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"
)

var contractRuntimeStates = []RuntimeState{
	RuntimeStopped,
	RuntimeStarting,
	RuntimeListening,
	RuntimeASRProcessing,
	RuntimeTranslating,
	RuntimeThinking,
	RuntimeAssistantProcessing,
	RuntimeTTSProcessing,
	RuntimePlaying,
	RuntimeStopping,
	RuntimeFailed,
}

var contractRuntimeErrorCodes = []RuntimeErrorCode{
	RuntimeErrorStartFailed,
	RuntimeErrorStopFailed,
	RuntimeErrorPipelineFailed,
	RuntimeErrorTranslationRejected,
}

func TestRuntimeStateContract(t *testing.T) {
	for _, state := range contractRuntimeStates {
		if !state.Valid() {
			t.Fatalf("RuntimeState(%q).Valid() = false", state)
		}
	}
	if RuntimeState("unknown").Valid() {
		t.Fatal("unknown runtime state must be invalid")
	}
	for _, code := range contractRuntimeErrorCodes {
		if !code.Valid() {
			t.Fatalf("RuntimeErrorCode(%q).Valid() = false", code)
		}
	}
	if RuntimeErrorCode("unknown").Valid() {
		t.Fatal("unknown runtime error code must be invalid")
	}
}

func TestOpenAPIRuntimeContractMatchesGoTypes(t *testing.T) {
	specPath := filepath.Join("..", "..", "openapi.yaml")
	specData, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("read OpenAPI spec: %v", err)
	}

	var spec openAPISpec
	if err := yaml.Unmarshal(specData, &spec); err != nil {
		t.Fatalf("parse OpenAPI spec: %v", err)
	}

	stateSchema := spec.Components.Schemas["RealtimeRuntimeState"]
	if !reflect.DeepEqual(stateSchema.Enum, stringValues(contractRuntimeStates)) {
		t.Fatalf("RealtimeRuntimeState enum = %v, want %v", stateSchema.Enum, contractRuntimeStates)
	}
	errorSchema := spec.Components.Schemas["RealtimeRuntimeErrorCode"]
	if !reflect.DeepEqual(errorSchema.Enum, stringValues(contractRuntimeErrorCodes)) {
		t.Fatalf("RealtimeRuntimeErrorCode enum = %v, want %v", errorSchema.Enum, contractRuntimeErrorCodes)
	}

	snapshot := spec.Components.Schemas["RealtimeRuntimeSnapshot"]
	wantFields := []string{"session_id", "start_operation_id", "runtime_state", "current_turn_id", "current_playback_id", "last_error_code", "updated_at"}
	if !reflect.DeepEqual(snapshot.Required, wantFields) {
		t.Fatalf("RealtimeRuntimeSnapshot required = %v, want %v", snapshot.Required, wantFields)
	}
	if got := snapshot.Properties["runtime_state"].Ref; got != "#/components/schemas/RealtimeRuntimeState" {
		t.Fatalf("RealtimeRuntimeSnapshot.runtime_state ref = %q", got)
	}
}
