package config

import (
	"errors"
	"testing"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/asr"
	asrqwen "github.com/1024XEngineer/xe6-tsy/services/realtime-audio/asr/qwen"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/assistant"
	assistantqwen "github.com/1024XEngineer/xe6-tsy/services/realtime-audio/assistant/qwen"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/translate"
	translateqwen "github.com/1024XEngineer/xe6-tsy/services/realtime-audio/translate/qwen"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/tts"
	ttsqwen "github.com/1024XEngineer/xe6-tsy/services/realtime-audio/tts/qwen"
)

func TestBuildProvidersUsesExplicitOfflineProviders(t *testing.T) {
	offline := Providers{
		ASR:         asr.NewFakeProvider(asr.FakeProviderConfig{}),
		Assistant:   assistant.NewFakeProvider(assistant.FakeProviderConfig{}),
		Translation: &translate.FakeProvider{},
		TTS:         tts.NewFakeProvider(tts.FakeProviderConfig{}),
	}
	providers, err := BuildProviders(ProviderConfig{}, offline)
	if err != nil {
		t.Fatalf("BuildProviders() error = %v", err)
	}
	if providers.ASR != offline.ASR || providers.Assistant != offline.Assistant || providers.Translation != offline.Translation || providers.TTS != offline.TTS {
		t.Fatal("BuildProviders() did not preserve offline provider instances")
	}
}

func TestBuildProvidersConstructsQwenAdapters(t *testing.T) {
	config := ProviderConfig{
		ASR:         ASRConfig{Provider: ProviderAliyun, APIKey: "asr-key", BaseURL: "https://example.com/compatible-mode/v1"},
		Translation: TranslationConfig{Provider: ProviderAliyun, APIKey: "llm-key", BaseURL: "https://example.com/compatible-mode/v1"},
		TTS:         TTSConfig{Provider: ProviderAliyun, APIKey: "tts-key", BaseURL: "https://example.com/api/v1"},
	}
	providers, err := BuildProviders(config, Providers{})
	if err != nil {
		t.Fatalf("BuildProviders() error = %v", err)
	}
	if _, ok := providers.ASR.(*asrqwen.Provider); !ok {
		t.Fatalf("ASR provider type = %T", providers.ASR)
	}
	if _, ok := providers.Translation.(*translateqwen.Provider); !ok {
		t.Fatalf("translation provider type = %T", providers.Translation)
	}
	if _, ok := providers.Assistant.(*assistantqwen.Provider); !ok {
		t.Fatalf("assistant provider type = %T", providers.Assistant)
	}
	if _, ok := providers.TTS.(*ttsqwen.Provider); !ok {
		t.Fatalf("TTS provider type = %T", providers.TTS)
	}
}

func TestBuildProvidersValidatesSelections(t *testing.T) {
	tests := []struct {
		name    string
		config  ProviderConfig
		offline Providers
		want    error
	}{
		{name: "missing mock", config: ProviderConfig{}, want: ErrMockProviderRequired},
		{name: "unsupported", config: ProviderConfig{ASR: ASRConfig{Provider: "other"}}, want: ErrUnsupportedProvider},
		{name: "Qwen key", config: ProviderConfig{ASR: ASRConfig{Provider: ProviderAliyun, BaseURL: "https://example.com"}}, want: asrqwen.ErrAPIKeyRequired},
		{
			name: "Qwen translation model",
			config: ProviderConfig{
				ASR:         ASRConfig{Provider: ProviderMock},
				Translation: TranslationConfig{Provider: ProviderAliyun, APIKey: "llm-key", BaseURL: "https://example.com", Model: "deepseek-chat"},
				TTS:         TTSConfig{Provider: ProviderMock},
			},
			offline: Providers{ASR: asr.NewFakeProvider(asr.FakeProviderConfig{}), TTS: tts.NewFakeProvider(tts.FakeProviderConfig{})},
			want:    ErrUnsupportedModel,
		},
		{
			name: "Qwen ASR sample rate",
			config: ProviderConfig{
				ASR:         ASRConfig{Provider: ProviderAliyun, APIKey: "asr-key", BaseURL: "https://example.com", SampleRate: 44100},
				Translation: TranslationConfig{Provider: ProviderMock},
				TTS:         TTSConfig{Provider: ProviderMock},
			},
			offline: Providers{Translation: &translate.FakeProvider{}, TTS: tts.NewFakeProvider(tts.FakeProviderConfig{})},
			want:    ErrInvalidEnvironmentValue,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := BuildProviders(test.config, test.offline)
			if !errors.Is(err, test.want) {
				t.Fatalf("BuildProviders() error = %v, want %v", err, test.want)
			}
		})
	}
}
