package realtimev1

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

var contractModes = []Mode{ModeAssistant, ModeInterpretation}
var contractModePhases = []ModePhase{ModePhaseActive, ModePhaseSwitching}
var contractModeSwitchStatuses = []ModeSwitchStatus{ModeSwitchApplied, ModeSwitchUnchanged}

func TestModeChangedEventValidation(t *testing.T) {
	event := validModeChangedEvent()
	if err := event.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*ModeChangedEvent)
	}{
		{name: "event version", mutate: func(event *ModeChangedEvent) { event.EventVersion = 2 }},
		{name: "event id", mutate: func(event *ModeChangedEvent) { event.EventID = "" }},
		{name: "trace id", mutate: func(event *ModeChangedEvent) { event.TraceID = "" }},
		{name: "session id", mutate: func(event *ModeChangedEvent) { event.SessionID = "" }},
		{name: "runtime instance id", mutate: func(event *ModeChangedEvent) { event.RuntimeInstanceID = "" }},
		{name: "operation id", mutate: func(event *ModeChangedEvent) { event.OperationID = "" }},
		{name: "from mode", mutate: func(event *ModeChangedEvent) { event.FromMode = "unknown" }},
		{name: "to mode", mutate: func(event *ModeChangedEvent) { event.ToMode = "unknown" }},
		{name: "unchanged mode", mutate: func(event *ModeChangedEvent) { event.ToMode = event.FromMode }},
		{name: "generation", mutate: func(event *ModeChangedEvent) { event.ResultingGeneration = 0 }},
		{name: "occurred at", mutate: func(event *ModeChangedEvent) { event.OccurredAt = time.Time{} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			invalid := validModeChangedEvent()
			test.mutate(&invalid)
			if err := invalid.Validate(); !errors.Is(err, ErrInvalidModeChangedEvent) {
				t.Fatalf("Validate() error = %v, want ErrInvalidModeChangedEvent", err)
			}
		})
	}
}

func validModeChangedEvent() ModeChangedEvent {
	return ModeChangedEvent{
		EventVersion: ModeChangedEventVersion, EventID: "mode-event-1", TraceID: "trace-1",
		SessionID: "session-1", RuntimeInstanceID: "runtime-1", OperationID: "operation-1",
		FromMode: ModeInterpretation, ToMode: ModeAssistant, ResultingGeneration: 2,
		OccurredAt: time.Unix(1700000000, 0).UTC(),
	}
}

func TestModeValuesAndLegacyDefault(t *testing.T) {
	for _, mode := range contractModes {
		if !mode.Valid() {
			t.Fatalf("Mode(%q).Valid() = false", mode)
		}
	}
	if Mode("").Valid() || Mode("english_practice").Valid() {
		t.Fatal("empty and future placeholder modes must not be valid")
	}
	if got := Mode("").OrLegacyDefault(); got != ModeInterpretation {
		t.Fatalf("empty mode default = %q, want %q", got, ModeInterpretation)
	}
	if got := ModeAssistant.OrLegacyDefault(); got != ModeAssistant {
		t.Fatalf("explicit mode default = %q, want %q", got, ModeAssistant)
	}
}

func TestModePhaseAndSwitchStatusValues(t *testing.T) {
	for _, phase := range contractModePhases {
		if !phase.Valid() {
			t.Fatalf("ModePhase(%q).Valid() = false", phase)
		}
	}
	if ModePhase("failed").Valid() {
		t.Fatal("unknown mode phase must be invalid")
	}
	for _, status := range contractModeSwitchStatuses {
		if !status.Valid() {
			t.Fatalf("ModeSwitchStatus(%q).Valid() = false", status)
		}
	}
	if ModeSwitchStatus("replayed").Valid() {
		t.Fatal("replay must return the first result instead of adding a replayed status")
	}
}

func TestStartRequestInitialModeCompatibility(t *testing.T) {
	legacy, err := json.Marshal(StartRequest{OperationID: "operation-1"})
	if err != nil {
		t.Fatalf("Marshal() legacy request error = %v", err)
	}
	if strings.Contains(string(legacy), "initial_mode") {
		t.Fatalf("legacy StartRequest JSON = %s, want initial_mode omitted", legacy)
	}

	explicit, err := json.Marshal(StartRequest{OperationID: "operation-2", InitialMode: ModeAssistant})
	if err != nil {
		t.Fatalf("Marshal() explicit request error = %v", err)
	}
	if !strings.Contains(string(explicit), `"initial_mode":"assistant"`) {
		t.Fatalf("explicit StartRequest JSON = %s, want assistant initial_mode", explicit)
	}
}

func TestModeContractsCarryRuntimeIdentityAndGeneration(t *testing.T) {
	lastOperationID := "operation-1"
	now := time.Unix(1700000000, 0).UTC()
	result := SwitchModeResult{
		OperationID: "operation-1",
		Status:      ModeSwitchApplied,
		State: ModeStateSnapshot{
			SessionID:         "session-1",
			RuntimeInstanceID: "runtime-1",
			ActiveMode:        ModeAssistant,
			Generation:        2,
			Phase:             ModePhaseActive,
			LastOperationID:   &lastOperationID,
			UpdatedAt:         now,
		},
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Marshal() result error = %v", err)
	}
	for _, field := range []string{
		`"runtime_instance_id":"runtime-1"`,
		`"active_mode":"assistant"`,
		`"generation":2`,
		`"last_operation_id":"operation-1"`,
	} {
		if !strings.Contains(string(encoded), field) {
			t.Fatalf("SwitchModeResult JSON = %s, missing %s", encoded, field)
		}
	}

	command := SwitchModeCommand{
		SessionID:          "session-1",
		RuntimeInstanceID:  "runtime-1",
		OperationID:        "operation-2",
		TraceID:            "trace-1",
		ExpectedGeneration: 2,
		TargetMode:         ModeInterpretation,
	}
	encoded, err = json.Marshal(command)
	if err != nil {
		t.Fatalf("Marshal() command error = %v", err)
	}
	for _, field := range []string{
		`"operation_id":"operation-2"`,
		`"expected_generation":2`,
		`"target_mode":"interpretation"`,
	} {
		if !strings.Contains(string(encoded), field) {
			t.Fatalf("SwitchModeCommand JSON = %s, missing %s", encoded, field)
		}
	}
}

func TestModeEventIdentity(t *testing.T) {
	if ModeChangedTopic != "realtime.mode.changed" || ModeChangedEventVersion != 1 {
		t.Fatalf("mode changed identity = (%q, %d)", ModeChangedTopic, ModeChangedEventVersion)
	}
	if AssistantReplyTopic != "assistant.reply" || AssistantReplyEventVersion != 1 {
		t.Fatalf("assistant reply identity = (%q, %d)", AssistantReplyTopic, AssistantReplyEventVersion)
	}
}

func TestOpenAPIModeContractMatchesGoTypes(t *testing.T) {
	spec := readYAMLMap(t, filepath.Join("..", "..", "openapi.yaml"))
	schemas := nestedMap(t, nestedMap(t, spec, "components"), "schemas")

	assertStringList(t, nestedMap(t, schemas, "RealtimeMode")["enum"], stringValues(contractModes))
	assertStringList(t, nestedMap(t, schemas, "ModePhase")["enum"], stringValues(contractModePhases))
	assertStringList(t, nestedMap(t, schemas, "ModeSwitchStatus")["enum"], stringValues(contractModeSwitchStatuses))

	start := nestedMap(t, schemas, "RealtimeStartRequest")
	assertStringList(t, start["required"], []string{"operation_id"})
	startProperties := nestedMap(t, start, "properties")
	initialMode := nestedMap(t, startProperties, "initial_mode")
	if got := initialMode["$ref"]; got != "#/components/schemas/RealtimeMode" {
		t.Fatalf("RealtimeStartRequest.initial_mode ref = %v", got)
	}
	if got := initialMode["default"]; got != string(ModeInterpretation) {
		t.Fatalf("RealtimeStartRequest.initial_mode default = %v, want %q", got, ModeInterpretation)
	}

	modeState := nestedMap(t, schemas, "ModeStateSnapshot")
	assertStringList(t, modeState["required"], []string{
		"session_id", "runtime_instance_id", "active_mode", "generation", "phase", "last_operation_id", "updated_at",
	})
	switchCommand := nestedMap(t, schemas, "SwitchModeCommand")
	assertStringList(t, switchCommand["required"], []string{
		"session_id", "runtime_instance_id", "operation_id", "trace_id", "expected_generation", "target_mode",
	})
	assertStringList(t, nestedMap(t, schemas, "SwitchModeResult")["required"], []string{"operation_id", "status", "state"})
	assertStringList(t, nestedMap(t, schemas, "ModeChangedEvent")["required"], []string{
		"event_version", "event_id", "trace_id", "session_id", "runtime_instance_id", "operation_id",
		"from_mode", "to_mode", "resulting_generation", "occurred_at",
	})
	assertStringList(t, nestedMap(t, schemas, "AssistantReplyEvent")["required"], []string{
		"event_version", "event_id", "trace_id", "session_id", "turn_id", "runtime_instance_id",
		"generation", "text", "language", "occurred_at",
	})

	paths := nestedMap(t, spec, "paths")
	modePath := nestedMap(t, paths, "/realtime/v1/sessions/{session_id}/mode")
	get := nestedMap(t, modePath, "get")
	post := nestedMap(t, modePath, "post")
	assertRealtimeTicketMap(t, get)
	assertRealtimeTicketMap(t, post)
	assertParameterRef(t, post, "#/components/parameters/IdempotencyKey")
	assertResponseCodes(t, get, []string{"200", "401", "404", "503"})
	assertResponseCodes(t, post, []string{"200", "400", "401", "404", "409", "422", "503"})
	if got := responseSchemaRef(t, get, "200"); got != "#/components/schemas/ModeStateSnapshot" {
		t.Fatalf("mode GET 200 schema ref = %q", got)
	}
	if got := responseSchemaRef(t, post, "200"); got != "#/components/schemas/SwitchModeResult" {
		t.Fatalf("mode POST 200 schema ref = %q", got)
	}
	requestBody := nestedMap(t, post, "requestBody")
	content := nestedMap(t, requestBody, "content")
	requestSchema := nestedMap(t, nestedMap(t, content, "application/json"), "schema")
	if got := requestSchema["$ref"]; got != "#/components/schemas/SwitchModeCommand" {
		t.Fatalf("mode POST request schema ref = %v", got)
	}
}

func TestAsyncAPIModeEventsMatchGoContracts(t *testing.T) {
	spec := readYAMLMap(t, filepath.Join("..", "..", "events.yaml"))
	channels := nestedMap(t, spec, "channels")
	if got := nestedMap(t, channels, "modeChanged")["address"]; got != ModeChangedTopic {
		t.Fatalf("mode changed topic = %v, want %q", got, ModeChangedTopic)
	}
	if got := nestedMap(t, channels, "assistantReply")["address"]; got != AssistantReplyTopic {
		t.Fatalf("assistant reply topic = %v, want %q", got, AssistantReplyTopic)
	}

	operations := nestedMap(t, spec, "operations")
	for name, action := range map[string]string{
		"sendModeChanged":    "send",
		"consumeModeChanged": "receive",
		"sendAssistantReply": "send",
	} {
		if got := nestedMap(t, operations, name)["action"]; got != action {
			t.Fatalf("operation %s action = %v, want %q", name, got, action)
		}
	}

	schemas := nestedMap(t, nestedMap(t, spec, "components"), "schemas")
	assertStringList(t, nestedMap(t, schemas, "RealtimeMode")["enum"], stringValues(contractModes))
	modeChanged := nestedMap(t, schemas, "ModeChangedEvent")
	assertStringList(t, modeChanged["required"], []string{
		"event_version", "event_id", "trace_id", "session_id", "runtime_instance_id", "operation_id",
		"from_mode", "to_mode", "resulting_generation", "occurred_at",
	})
	if got := nestedMap(t, nestedMap(t, modeChanged, "properties"), "event_version")["const"]; got != ModeChangedEventVersion {
		t.Fatalf("ModeChangedEvent event_version = %v, want %d", got, ModeChangedEventVersion)
	}
	assistantReply := nestedMap(t, schemas, "AssistantReplyEvent")
	assertStringList(t, assistantReply["required"], []string{
		"event_version", "event_id", "trace_id", "session_id", "turn_id", "runtime_instance_id",
		"generation", "text", "language", "occurred_at",
	})
	if got := nestedMap(t, nestedMap(t, assistantReply, "properties"), "event_version")["const"]; got != AssistantReplyEventVersion {
		t.Fatalf("AssistantReplyEvent event_version = %v, want %d", got, AssistantReplyEventVersion)
	}
}

func readYAMLMap(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var result map[string]any
	if err := yaml.Unmarshal(data, &result); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return result
}

func nestedMap(t *testing.T, values map[string]any, key string) map[string]any {
	t.Helper()
	value, ok := values[key].(map[string]any)
	if !ok {
		t.Fatalf("%q is not an object", key)
	}
	return value
}

func assertStringList(t *testing.T, value any, want []string) {
	t.Helper()
	items, ok := value.([]any)
	if !ok {
		t.Fatalf("value = %#v, want string array", value)
	}
	got := make([]string, len(items))
	for index, item := range items {
		got[index], ok = item.(string)
		if !ok {
			t.Fatalf("array item = %#v, want string", item)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("list = %v, want %v", got, want)
	}
}

func assertRealtimeTicketMap(t *testing.T, operation map[string]any) {
	t.Helper()
	security, ok := operation["security"].([]any)
	if !ok || len(security) != 1 {
		t.Fatalf("security = %#v, want one realtimeTicket requirement", operation["security"])
	}
	requirement, ok := security[0].(map[string]any)
	if !ok {
		t.Fatalf("security requirement = %#v, want object", security[0])
	}
	if _, ok := requirement["realtimeTicket"]; !ok {
		t.Fatalf("security requirement = %#v, want realtimeTicket", requirement)
	}
}

func assertParameterRef(t *testing.T, operation map[string]any, want string) {
	t.Helper()
	parameters, ok := operation["parameters"].([]any)
	if !ok {
		t.Fatalf("parameters = %#v, want array", operation["parameters"])
	}
	for _, parameter := range parameters {
		value, ok := parameter.(map[string]any)
		if ok && value["$ref"] == want {
			return
		}
	}
	t.Fatalf("parameters = %#v, missing %q", parameters, want)
}

func assertResponseCodes(t *testing.T, operation map[string]any, want []string) {
	t.Helper()
	responses := nestedMap(t, operation, "responses")
	got := make([]string, 0, len(responses))
	for _, code := range want {
		if _, ok := responses[code]; !ok {
			t.Fatalf("responses missing %s", code)
		}
		got = append(got, code)
	}
	if len(responses) != len(got) {
		t.Fatalf("response count = %d, want %d", len(responses), len(got))
	}
}

func responseSchemaRef(t *testing.T, operation map[string]any, code string) string {
	t.Helper()
	response := nestedMap(t, nestedMap(t, operation, "responses"), code)
	content := nestedMap(t, response, "content")
	schema := nestedMap(t, nestedMap(t, content, "application/json"), "schema")
	ref, _ := schema["$ref"].(string)
	return ref
}
