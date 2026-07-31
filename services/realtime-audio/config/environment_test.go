package config

import (
	"errors"
	"testing"
	"time"
)

func TestLoadProviderConfigDefaultsToOfflineProviders(t *testing.T) {
	config, err := LoadProviderConfig(mapLookup(nil))
	if err != nil {
		t.Fatalf("LoadProviderConfig() error = %v", err)
	}
	if config.ASR.Provider != ProviderMock || config.Translation.Provider != ProviderMock || config.TTS.Provider != ProviderMock {
		t.Fatalf("providers = %q/%q/%q, want all mock", config.ASR.Provider, config.Translation.Provider, config.TTS.Provider)
	}
	if !config.ASR.ServerVAD {
		t.Fatal("ASR.ServerVAD default = false, want true")
	}
}

func TestLoadProviderConfigReadsQwenSettings(t *testing.T) {
	values := map[string]string{
		"ASR_PROVIDER": "aliyun", "ASR_API_KEY": "asr-key", "ASR_SAMPLE_RATE": "16000",
		"ASR_VAD_THRESHOLD": "0.3", "ASR_SILENCE_DURATION_MS": "700",
		"LLM_PROVIDER": "aliyun", "LLM_API_KEY": "llm-key", "LLM_MODEL": "qwen3.6-flash", "LLM_ENABLE_THINKING": "true", "LLM_TIMEOUT_MS": "12000",
		"TTS_PROVIDER": "aliyun", "TTS_API_KEY": "tts-key", "TTS_SAMPLE_RATE": "24000", "TTS_TIMEOUT_MS": "25000",
	}
	config, err := LoadProviderConfig(mapLookup(values))
	if err != nil {
		t.Fatalf("LoadProviderConfig() error = %v", err)
	}
	if config.ASR.SampleRate != 16000 || config.ASR.VADThreshold != 0.3 || config.ASR.SilenceDuration != 700*time.Millisecond {
		t.Fatalf("ASR config = %+v", config.ASR)
	}
	if !config.ASR.ServerVAD {
		t.Fatal("ASR.ServerVAD = false when ASR_SERVER_VAD unset, want true")
	}
	if config.Translation.Model != defaultTranslationModel || !config.Translation.EnableThinking || config.Translation.Timeout != 12*time.Second {
		t.Fatalf("translation config = %+v", config.Translation)
	}
	if config.TTS.SampleRate != 24000 || config.TTS.Timeout != 25*time.Second {
		t.Fatalf("TTS config = %+v", config.TTS)
	}
}

func TestLoadProviderConfigRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name   string
		values map[string]string
		want   error
	}{
		{name: "provider", values: map[string]string{"LLM_PROVIDER": "deepseek"}, want: ErrUnsupportedProvider},
		{name: "model", values: map[string]string{"LLM_MODEL": "deepseek-chat"}, want: ErrUnsupportedModel},
		{name: "integer", values: map[string]string{"ASR_SAMPLE_RATE": "fast"}, want: ErrInvalidEnvironmentValue},
		{name: "negative", values: map[string]string{"TTS_TIMEOUT_MS": "-1"}, want: ErrInvalidEnvironmentValue},
		{name: "VAD range", values: map[string]string{"ASR_VAD_THRESHOLD": "1.1"}, want: ErrInvalidEnvironmentValue},
		{name: "VAD non-finite", values: map[string]string{"ASR_VAD_THRESHOLD": "NaN"}, want: ErrInvalidEnvironmentValue},
		{name: "silence range", values: map[string]string{"ASR_SILENCE_DURATION_MS": "100"}, want: ErrInvalidEnvironmentValue},
		{name: "sample rate", values: map[string]string{"ASR_SAMPLE_RATE": "44100"}, want: ErrInvalidEnvironmentValue},
		{name: "boolean", values: map[string]string{"LLM_ENABLE_THINKING": "sometimes"}, want: ErrInvalidEnvironmentValue},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := LoadProviderConfig(mapLookup(test.values))
			if !errors.Is(err, test.want) {
				t.Fatalf("LoadProviderConfig() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestLoadProviderConfigAllowsNegativeQwenVADThreshold(t *testing.T) {
	config, err := LoadProviderConfig(mapLookup(map[string]string{"ASR_VAD_THRESHOLD": "-0.5"}))
	if err != nil {
		t.Fatalf("LoadProviderConfig() error = %v", err)
	}
	if config.ASR.VADThreshold != -0.5 {
		t.Fatalf("VAD threshold = %v, want -0.5", config.ASR.VADThreshold)
	}
}

func TestLoadProviderConfigRequiresLookup(t *testing.T) {
	_, err := LoadProviderConfig(nil)
	if !errors.Is(err, ErrEnvironmentLookupRequired) {
		t.Fatalf("LoadProviderConfig(nil) error = %v", err)
	}
}

func mapLookup(values map[string]string) LookupEnv {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}
