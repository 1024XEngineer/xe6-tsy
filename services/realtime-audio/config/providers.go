package config

import (
	"errors"
	"fmt"
	"strings"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/asr"
	asrqwen "github.com/1024XEngineer/xe6-tsy/services/realtime-audio/asr/qwen"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/translate"
	translateqwen "github.com/1024XEngineer/xe6-tsy/services/realtime-audio/translate/qwen"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/tts"
	ttsqwen "github.com/1024XEngineer/xe6-tsy/services/realtime-audio/tts/qwen"
)

var ErrMockProviderRequired = errors.New("selected mock provider is required")

// Providers is the vendor-neutral provider set injected into the Turn processor and pipeline.
type Providers struct {
	ASR         asr.Provider
	Translation translate.Provider
	TTS         tts.Provider
}

// BuildProviders constructs selected vendor adapters and reuses explicit offline providers.
func BuildProviders(config ProviderConfig, offline Providers) (Providers, error) {
	recognizer, err := buildASR(config.ASR, offline.ASR)
	if err != nil {
		return Providers{}, fmt.Errorf("build ASR provider: %w", err)
	}
	translator, err := buildTranslation(config.Translation, offline.Translation)
	if err != nil {
		return Providers{}, fmt.Errorf("build translation provider: %w", err)
	}
	synthesizer, err := buildTTS(config.TTS, offline.TTS)
	if err != nil {
		return Providers{}, fmt.Errorf("build TTS provider: %w", err)
	}
	return Providers{ASR: recognizer, Translation: translator, TTS: synthesizer}, nil
}

// BuildProvidersFromEnvironment is the startup boundary for provider selection.
func BuildProvidersFromEnvironment(offline Providers) (Providers, error) {
	config, err := LoadProviderConfigFromEnvironment()
	if err != nil {
		return Providers{}, err
	}
	return BuildProviders(config, offline)
}

func buildASR(config ASRConfig, offline asr.Provider) (asr.Provider, error) {
	switch normalizedProvider(config.Provider) {
	case ProviderMock:
		if offline == nil {
			return nil, fmt.Errorf("%w: ASR", ErrMockProviderRequired)
		}
		return offline, nil
	case ProviderAliyun:
		return asrqwen.NewProvider(asrqwen.Config{
			APIKey: config.APIKey, BaseURL: config.BaseURL, WebSocketURL: config.WebSocketURL,
			Model: config.Model, Provider: string(ProviderAliyun), SampleRate: config.SampleRate,
			VADThreshold: config.VADThreshold, SilenceDuration: config.SilenceDuration,
		})
	default:
		return nil, unsupportedProvider(config.Provider)
	}
}

func buildTranslation(config TranslationConfig, offline translate.Provider) (translate.Provider, error) {
	switch normalizedProvider(config.Provider) {
	case ProviderMock:
		if offline == nil {
			return nil, fmt.Errorf("%w: translation", ErrMockProviderRequired)
		}
		return offline, nil
	case ProviderAliyun:
		model := strings.TrimSpace(config.Model)
		if model == "" {
			model = defaultTranslationModel
		}
		if !strings.EqualFold(model, defaultTranslationModel) {
			return nil, fmt.Errorf("%w: LLM_MODEL=%q (want %s)", ErrUnsupportedModel, model, defaultTranslationModel)
		}
		return translateqwen.NewProvider(translateqwen.Config{
			APIKey: config.APIKey, BaseURL: config.BaseURL, Model: defaultTranslationModel,
			Provider: string(ProviderAliyun), EnableThinking: config.EnableThinking, Timeout: config.Timeout,
		})
	default:
		return nil, unsupportedProvider(config.Provider)
	}
}

func buildTTS(config TTSConfig, offline tts.Provider) (tts.Provider, error) {
	switch normalizedProvider(config.Provider) {
	case ProviderMock:
		if offline == nil {
			return nil, fmt.Errorf("%w: TTS", ErrMockProviderRequired)
		}
		return offline, nil
	case ProviderAliyun:
		return ttsqwen.NewProvider(ttsqwen.Config{
			APIKey: config.APIKey, BaseURL: config.BaseURL, Model: config.Model,
			Provider: string(ProviderAliyun), Voice: config.Voice,
			SampleRate: config.SampleRate, Timeout: config.Timeout,
		})
	default:
		return nil, unsupportedProvider(config.Provider)
	}
}

func normalizedProvider(provider ProviderName) ProviderName {
	provider = ProviderName(strings.ToLower(strings.TrimSpace(string(provider))))
	if provider == "" {
		return ProviderMock
	}
	return provider
}

func unsupportedProvider(provider ProviderName) error {
	return fmt.Errorf("%w: %q", ErrUnsupportedProvider, provider)
}
