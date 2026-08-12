package session

import (
	"context"
	"testing"
)

func TestRuntimeStateValues(t *testing.T) {
	tests := []struct {
		name  string
		state RuntimeState
		want  string
	}{
		{name: "stopped", state: RuntimeStopped, want: "stopped"},
		{name: "starting", state: RuntimeStarting, want: "starting"},
		{name: "listening", state: RuntimeListening, want: "listening"},
		{name: "asr processing", state: RuntimeASRProcessing, want: "asr_processing"},
		{name: "translating", state: RuntimeTranslating, want: "translating"},
		{name: "thinking compatibility", state: RuntimeThinking, want: "thinking"},
		{name: "assistant processing", state: RuntimeAssistantProcessing, want: "assistant_processing"},
		{name: "tts processing", state: RuntimeTTSProcessing, want: "tts_processing"},
		{name: "playing", state: RuntimePlaying, want: "playing"},
		{name: "stopping", state: RuntimeStopping, want: "stopping"},
		{name: "failed", state: RuntimeFailed, want: "failed"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := string(test.state); got != test.want {
				t.Fatalf("runtime state = %q, want %q", got, test.want)
			}
		})
	}
}

func TestLanguageConfigReaderUsesSessionBoundary(t *testing.T) {
	reader := languageConfigReaderStub{}
	snapshot, err := reader.GetCurrentConfig(context.Background(), "session-1")
	if err != nil {
		t.Fatalf("GetCurrentConfig() error = %v", err)
	}
	if snapshot.SessionID != "session-1" {
		t.Fatalf("SessionID = %q, want session-1", snapshot.SessionID)
	}
}

type languageConfigReaderStub struct{}

func (languageConfigReaderStub) GetCurrentConfig(_ context.Context, sessionID string) (LanguageConfigSnapshot, error) {
	return LanguageConfigSnapshot{SessionID: sessionID}, nil
}

var _ LanguageConfigReader = languageConfigReaderStub{}
