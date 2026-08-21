package realtimev1

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var controlMessageTypes = []ControlMessageType{
	ControlMessageModeSwitch,
	ControlMessageModeSwitchResult,
	ControlMessageError,
}

var controlPlaneErrorCodes = []ControlPlaneErrorCode{
	ErrorRuntimeNotFound,
	ErrorRuntimeOperationConflict,
	ErrorModeNotAvailable,
	ErrorModeGenerationConflict,
	ErrorModeRuntimeInstanceMismatch,
	ErrorModeOperationConflict,
	ErrorControlInvalidMessage,
	ErrorControlUnsupportedVersion,
	ErrorControlUnsupportedType,
	ErrorControlUnauthorizedSession,
	ErrorControlConnectionClosed,
	ErrorControlUnavailable,
}

func TestControlProtocolIdentityAndEnums(t *testing.T) {
	if ControlProtocolVersion != 1 || ControlDataChannelLabel != "lingow-control-v1" {
		t.Fatalf("control protocol identity = (%d, %q)", ControlProtocolVersion, ControlDataChannelLabel)
	}
	for _, messageType := range controlMessageTypes {
		if !messageType.Valid() {
			t.Fatalf("ControlMessageType(%q).Valid() = false", messageType)
		}
	}
	if ControlMessageType("mode.stop").Valid() {
		t.Fatal("unknown control message type must be invalid")
	}
	for _, code := range controlPlaneErrorCodes {
		if !code.Valid() {
			t.Fatalf("ControlPlaneErrorCode(%q).Valid() = false", code)
		}
	}
	if ControlPlaneErrorCode("unknown").Valid() {
		t.Fatal("unknown control error code must be invalid")
	}
}

func TestControlModeSwitchRequestValidation(t *testing.T) {
	request := validControlModeSwitchRequest()
	if err := request.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*ControlModeSwitchRequest)
	}{
		{name: "protocol version", mutate: func(request *ControlModeSwitchRequest) { request.ProtocolVersion = 2 }},
		{name: "message type", mutate: func(request *ControlModeSwitchRequest) { request.Type = ControlMessageError }},
		{name: "empty request id", mutate: func(request *ControlModeSwitchRequest) { request.RequestID = " " }},
		{name: "invalid UTF-8 request id", mutate: func(request *ControlModeSwitchRequest) { request.RequestID = string([]byte{0xff}) }},
		{name: "long request id", mutate: func(request *ControlModeSwitchRequest) {
			request.RequestID = strings.Repeat("r", maxControlRequestIDLength+1)
		}},
		{name: "runtime id", mutate: func(request *ControlModeSwitchRequest) { request.Command.RuntimeInstanceID = "" }},
		{name: "operation id", mutate: func(request *ControlModeSwitchRequest) { request.Command.OperationID = "operation\n2" }},
		{name: "generation", mutate: func(request *ControlModeSwitchRequest) { request.Command.ExpectedGeneration = 0 }},
		{name: "target mode", mutate: func(request *ControlModeSwitchRequest) { request.Command.TargetMode = "english_practice" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			invalid := validControlModeSwitchRequest()
			test.mutate(&invalid)
			if err := invalid.Validate(); !errors.Is(err, ErrInvalidControlMessage) {
				t.Fatalf("Validate() error = %v, want ErrInvalidControlMessage", err)
			}
		})
	}
	unicodeID := validControlModeSwitchRequest()
	unicodeID.RequestID = strings.Repeat("请", maxControlRequestIDLength)
	if err := unicodeID.Validate(); err != nil {
		t.Fatalf("Unicode request ID at schema maxLength rejected: %v", err)
	}
}

func TestControlResponseValidation(t *testing.T) {
	result := validControlModeSwitchResult()
	success := ControlResponse{
		ProtocolVersion: ControlProtocolVersion,
		Type:            ControlMessageModeSwitchResult,
		RequestID:       "request-1",
		Result:          &result,
	}
	if err := success.Validate(); err != nil {
		t.Fatalf("success Validate() error = %v", err)
	}
	failure := ControlResponse{
		ProtocolVersion: ControlProtocolVersion,
		Type:            ControlMessageError,
		RequestID:       "request-1",
		Error: &ControlError{
			Code: ErrorModeGenerationConflict, Message: "mode generation changed",
		},
	}
	if err := failure.Validate(); err != nil {
		t.Fatalf("failure Validate() error = %v", err)
	}
	uncorrelated := failure
	uncorrelated.RequestID = ""
	if err := uncorrelated.Validate(); err != nil {
		t.Fatalf("uncorrelated error Validate() error = %v", err)
	}

	tests := []struct {
		name     string
		response ControlResponse
	}{
		{name: "wrong version", response: func() ControlResponse { value := success; value.ProtocolVersion = 2; return value }()},
		{name: "request as response", response: func() ControlResponse { value := success; value.Type = ControlMessageModeSwitch; return value }()},
		{name: "missing request id", response: func() ControlResponse { value := success; value.RequestID = ""; return value }()},
		{name: "missing result", response: ControlResponse{ProtocolVersion: 1, Type: ControlMessageModeSwitchResult, RequestID: "request-1"}},
		{name: "result and error", response: func() ControlResponse { value := success; value.Error = failure.Error; return value }()},
		{name: "error type with result", response: func() ControlResponse { value := success; value.Type = ControlMessageError; return value }()},
		{name: "unknown error", response: ControlResponse{ProtocolVersion: 1, Type: ControlMessageError, Error: &ControlError{Code: "unknown", Message: "failed"}}},
		{name: "empty error message", response: ControlResponse{ProtocolVersion: 1, Type: ControlMessageError, Error: &ControlError{Code: ErrorControlInvalidMessage}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.response.Validate(); !errors.Is(err, ErrInvalidControlMessage) {
				t.Fatalf("Validate() error = %v, want ErrInvalidControlMessage", err)
			}
		})
	}
}

func TestControlSchemasMatchGoContract(t *testing.T) {
	for _, path := range []string{"openapi.yaml", "events.yaml"} {
		t.Run(path, func(t *testing.T) {
			spec := readYAMLMap(t, filepath.Join("..", "..", path))
			schemas := nestedMap(t, nestedMap(t, spec, "components"), "schemas")
			if path == "openapi.yaml" {
				configOperation := nestedMap(t, nestedMap(t, nestedMap(t, spec, "paths"), "/realtime/v1/sessions/{session_id}/webrtc/config"), "get")
				configResponse := nestedMap(t, nestedMap(t, nestedMap(t, nestedMap(t, configOperation, "responses"), "200"), "content"), "application/json")
				if nestedMap(t, configResponse, "schema")["$ref"] != "#/components/schemas/WebRTCConfig" {
					t.Fatal("WebRTC config response schema is missing")
				}
				assertStringList(t, nestedMap(t, schemas, "WebRTCConfig")["required"], []string{"session_id", "expires_at", "ice_servers", "ice_transport_policy", "data_channel", "control_data_channel", "audio"})
				controlSchema := nestedMap(t, schemas, "WebRTCControlDataChannelConfig")
				assertStringList(t, controlSchema["required"], []string{"label", "ordered", "protocol_version"})
				controlConfig := nestedMap(t, controlSchema, "properties")
				if nestedMap(t, controlConfig, "label")["const"] != ControlDataChannelLabel || nestedMap(t, controlConfig, "ordered")["const"] != true || nestedMap(t, controlConfig, "protocol_version")["const"] != ControlProtocolVersion {
					t.Fatal("control DataChannel config does not match Go constants")
				}
			}
			assertStringList(t, nestedMap(t, schemas, "ControlPlaneErrorCode")["enum"], stringValues(controlPlaneErrorCodes))
			assertStringList(t, nestedMap(t, schemas, "ControlModeSwitchCommand")["required"], []string{"runtime_instance_id", "operation_id", "expected_generation", "target_mode"})
			assertStringList(t, nestedMap(t, schemas, "ControlModeSwitchRequest")["required"], []string{"protocol_version", "type", "request_id", "command"})
			assertStringList(t, nestedMap(t, schemas, "ControlResponse")["required"], []string{"protocol_version", "type"})
			requestProperties := nestedMap(t, nestedMap(t, schemas, "ControlModeSwitchRequest"), "properties")
			if got := nestedMap(t, requestProperties, "protocol_version")["const"]; got != ControlProtocolVersion {
				t.Fatalf("protocol version = %v, want %d", got, ControlProtocolVersion)
			}
			requestID := nestedMap(t, requestProperties, "request_id")
			if requestID["maxLength"] != maxControlRequestIDLength || requestID["pattern"] == nil {
				t.Fatalf("request_id bounds = %#v", requestID)
			}
			for _, test := range []struct {
				schema string
				field  string
			}{
				{schema: "WakeWordDetectedSignal", field: "signal_id"},
				{schema: "CommandResultEvent", field: "command_id"},
				{schema: "CommandResultEvent", field: "session_id"},
			} {
				properties := nestedMap(t, nestedMap(t, schemas, test.schema), "properties")
				if nestedMap(t, properties, test.field)["pattern"] != `^(?:[^\r\n\t]*\S[^\r\n\t]*)$` {
					t.Fatalf("%s.%s pattern is incomplete", test.schema, test.field)
				}
			}
			if got := nestedMap(t, requestProperties, "type")["const"]; got != string(ControlMessageModeSwitch) {
				t.Fatalf("request type = %v, want %q", got, ControlMessageModeSwitch)
			}
			result := nestedMap(t, nestedMap(t, nestedMap(t, schemas, "SwitchModeResult"), "properties"), "operation_id")
			state := nestedMap(t, nestedMap(t, schemas, "ModeStateSnapshot"), "properties")
			if result["maxLength"] != maxControlOperationIDLength || result["pattern"] == nil || nestedMap(t, state, "runtime_instance_id")["maxLength"] != maxControlRuntimeIDLength || nestedMap(t, state, "last_operation_id")["description"] == nil {
				t.Fatalf("response identifier bounds are incomplete")
			}
			responseTypes := nestedMap(t, nestedMap(t, schemas, "ControlResponse"), "properties")
			assertStringList(t, nestedMap(t, responseTypes, "type")["enum"], []string{
				string(ControlMessageModeSwitchResult), string(ControlMessageError),
			})
		})
	}
}

func TestAsyncAPIControlDataChannelIdentity(t *testing.T) {
	spec := readYAMLMap(t, filepath.Join("..", "..", "events.yaml"))
	channels := nestedMap(t, spec, "channels")
	if got := nestedMap(t, channels, "controlDataChannel")["address"]; got != ControlDataChannelLabel {
		t.Fatalf("control DataChannel address = %v, want %q", got, ControlDataChannelLabel)
	}
	operations := nestedMap(t, spec, "operations")
	for name, action := range map[string]string{
		"receiveControlModeSwitch": "receive",
		"sendControlResponse":      "send",
	} {
		if got := nestedMap(t, operations, name)["action"]; got != action {
			t.Fatalf("operation %s action = %v, want %q", name, got, action)
		}
	}
}

func validControlModeSwitchRequest() ControlModeSwitchRequest {
	return ControlModeSwitchRequest{
		ProtocolVersion: ControlProtocolVersion,
		Type:            ControlMessageModeSwitch,
		RequestID:       "request-1",
		Command: ControlModeSwitchCommand{
			RuntimeInstanceID: "runtime-1", OperationID: "operation-1",
			ExpectedGeneration: 1, TargetMode: ModeAssistant,
		},
	}
}

func validControlModeSwitchResult() SwitchModeResult {
	operationID := "operation-1"
	return SwitchModeResult{
		OperationID: operationID,
		Status:      ModeSwitchApplied,
		State: ModeStateSnapshot{
			SessionID: "session-1", RuntimeInstanceID: "runtime-1", ActiveMode: ModeAssistant,
			Generation: 2, Phase: ModePhaseActive, LastOperationID: &operationID,
			UpdatedAt: time.Unix(1700000000, 0).UTC(),
		},
	}
}
