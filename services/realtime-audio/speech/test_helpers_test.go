package speech

import (
	"testing"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/asr"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/tts"
)

func mustRegistry(t *testing.T, asrAdapter asr.Provider, ttsAdapter tts.Provider, capabilities []string) *ProviderRegistry {
	t.Helper()
	registry, err := NewProviderRegistry(
		[]ASRProfile{{
			Profile: Profile{ID: "asr-primary", Provider: "fake", Model: "asr-v1", Capabilities: capabilities},
			Adapter: asrAdapter,
		}},
		[]TTSProfile{{
			Profile: Profile{ID: "tts-primary", Provider: "fake", Model: "tts-v1", Voice: "voice-a", Capabilities: capabilities},
			Adapter: ttsAdapter,
		}},
	)
	if err != nil {
		t.Fatalf("NewProviderRegistry() error = %v", err)
	}
	return registry
}
