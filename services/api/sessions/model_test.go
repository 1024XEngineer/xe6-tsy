package sessions

import (
	"encoding/json"
	"testing"
	"time"
)

func TestVoiceSessionJSONKeepsStateBoundaries(t *testing.T) {
	session := VoiceSession{
		ID:           "vs_01TEST",
		AccountID:    "acct_01TEST",
		Status:       StatusCreated,
		AudioConfig:  marshalJSON(t, DefaultAudioConfig()),
		Capabilities: marshalJSON(t, Capabilities{WebRTC: true, DataChannel: true, Microphone: true, Speaker: true}),
		CreatedAt:    time.Date(2026, 7, 27, 8, 0, 0, 0, time.UTC),
	}

	body, err := json.Marshal(session)
	if err != nil {
		t.Fatalf("marshal voice session: %v", err)
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatalf("unmarshal voice session: %v", err)
	}

	for _, field := range []string{"runtime_state", "connection_state"} {
		if _, exists := fields[field]; exists {
			t.Fatalf("persistent voice session unexpectedly contains %q", field)
		}
	}
	for _, field := range []string{"started_at", "ended_at"} {
		value, exists := fields[field]
		if !exists {
			t.Fatalf("persistent voice session omits nullable field %q", field)
		}
		if string(value) != "null" {
			t.Fatalf("%s = %s, want null", field, value)
		}
	}
}

func TestVoiceSessionListItemJSONIsPersistentSummary(t *testing.T) {
	item := VoiceSessionListItem{
		ID:        "vs_01TEST",
		AccountID: "acct_01TEST",
		Status:    StatusEnded,
		CreatedAt: time.Date(2026, 7, 27, 8, 0, 0, 0, time.UTC),
	}

	body, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("marshal list item: %v", err)
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatalf("unmarshal list item: %v", err)
	}

	for _, field := range []string{"id", "account_id", "status", "started_at", "ended_at", "created_at"} {
		if _, exists := fields[field]; !exists {
			t.Errorf("list item omits %q", field)
		}
	}
	for _, field := range []string{
		"audio_config",
		"capabilities",
		"runtime_state",
		"connection_state",
		"current_turn_id",
		"current_playback_id",
		"last_error_code",
		"retryable",
		"runtime_updated_at",
	} {
		if _, exists := fields[field]; exists {
			t.Errorf("list item unexpectedly contains %q", field)
		}
	}
}

func TestDefaultAudioConfigMatchesP0BrowserProfile(t *testing.T) {
	got := DefaultAudioConfig()

	if got.Codec != "opus" || got.SampleRateHz != 48000 || got.Channels != 1 {
		t.Fatalf("default audio transport = %+v, want opus/48000/mono", got)
	}
	if !got.EchoCancellation || !got.NoiseSuppression || !got.AutoGainControl {
		t.Fatalf("default audio processing = %+v, want all browser processing enabled", got)
	}
}

func TestStatusValidation(t *testing.T) {
	tests := []struct {
		name   string
		status Status
		want   bool
	}{
		{name: string(StatusCreated), status: StatusCreated, want: true},
		{name: string(StatusActive), status: StatusActive, want: true},
		{name: string(StatusEnded), status: StatusEnded, want: true},
		{name: string(StatusFailed), status: StatusFailed, want: true},
		{name: "unknown", status: Status("unknown"), want: false},
		{name: "empty", status: Status(""), want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.status.Valid(); got != test.want {
				t.Fatalf("Valid() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestRuntimeStateValidation(t *testing.T) {
	validStates := []RuntimeState{
		RuntimeStopped,
		RuntimeStarting,
		RuntimeListening,
		RuntimeASRProcessing,
		RuntimeTranslating,
		RuntimeTTSProcessing,
		RuntimePlaying,
		RuntimeStopping,
		RuntimeFailed,
	}

	for _, state := range validStates {
		t.Run(string(state), func(t *testing.T) {
			if !state.Valid() {
				t.Fatalf("%q should be a valid runtime state", state)
			}
		})
	}
	if RuntimeState("connected").Valid() {
		t.Fatal("WebRTC connection state must not be accepted as a runtime state")
	}
}

func TestConnectionStateValidationAndReadiness(t *testing.T) {
	validStates := []ConnectionState{
		ConnectionNew,
		ConnectionConnecting,
		ConnectionConnected,
		ConnectionDisconnected,
		ConnectionFailed,
		ConnectionClosed,
	}

	for _, state := range validStates {
		t.Run(string(state), func(t *testing.T) {
			if !state.Valid() {
				t.Fatalf("%q should be a valid connection state", state)
			}
			if got, want := state.Ready(), state == ConnectionConnected; got != want {
				t.Fatalf("Ready() = %t, want %t", got, want)
			}
		})
	}

	for _, state := range []ConnectionState{"", "unknown", ConnectionState(RuntimeListening)} {
		t.Run("invalid_"+string(state), func(t *testing.T) {
			if state.Valid() {
				t.Fatalf("%q must not be accepted as a connection state", state)
			}
			if state.Ready() {
				t.Fatalf("%q must not satisfy WebRTC readiness", state)
			}
		})
	}
}

func TestRetryableOnlyAfterCreatedSessionStartFailure(t *testing.T) {
	tests := []struct {
		name    string
		status  Status
		runtime RuntimeState
		want    bool
	}{
		{name: "start failed", status: StatusCreated, runtime: RuntimeFailed, want: true},
		{name: "active runtime failure", status: StatusActive, runtime: RuntimeFailed, want: false},
		{name: "not started", status: StatusCreated, runtime: RuntimeStopped, want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := Retryable(test.status, test.runtime); got != test.want {
				t.Fatalf("Retryable(%q, %q) = %t, want %t", test.status, test.runtime, got, test.want)
			}
		})
	}
}

func TestEndReasonValidation(t *testing.T) {
	for _, reason := range []EndReason{
		EndReasonUserRequested,
		EndReasonOperatorCancelled,
		EndReasonClientDisconnected,
	} {
		if !reason.Valid() {
			t.Fatalf("%q should be a valid end reason", reason)
		}
	}
	if EndReason("timeout").Valid() {
		t.Fatal("unsupported end reason must be rejected")
	}
}

func marshalJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()

	body, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal test fixture: %v", err)
	}
	return body
}
