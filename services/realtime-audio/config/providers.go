package config

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/asr"
	asrqwen "github.com/1024XEngineer/xe6-tsy/services/realtime-audio/asr/qwen"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/assistant"
	assistantqwen "github.com/1024XEngineer/xe6-tsy/services/realtime-audio/assistant/qwen"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/rawlog"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/translate"
	translateqwen "github.com/1024XEngineer/xe6-tsy/services/realtime-audio/translate/qwen"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/tts"
	ttsqwen "github.com/1024XEngineer/xe6-tsy/services/realtime-audio/tts/qwen"
)

var ErrMockProviderRequired = errors.New("selected mock provider is required")

// Providers is the vendor-neutral provider set injected into the Turn processor and pipeline.
type Providers struct {
	ASR         asr.Provider
	Assistant   assistant.Provider
	Translation translate.Provider
	TTS         tts.Provider
}

// BuildProviders constructs selected vendor adapters and reuses explicit offline providers.
func BuildProviders(config ProviderConfig, offline Providers) (Providers, error) {
	rawLogger := rawlog.Default()
	recognizer, err := buildASR(config.ASR, offline.ASR, rawLogger)
	if err != nil {
		return Providers{}, fmt.Errorf("build ASR provider: %w", err)
	}
	translator, err := buildTranslation(config.Translation, offline.Translation, rawLogger)
	if err != nil {
		return Providers{}, fmt.Errorf("build translation provider: %w", err)
	}
	conversation, err := buildAssistant(config.Translation, offline.Assistant, rawLogger)
	if err != nil {
		return Providers{}, fmt.Errorf("build assistant provider: %w", err)
	}
	synthesizer, err := buildTTS(config.TTS, offline.TTS, rawLogger)
	if err != nil {
		return Providers{}, fmt.Errorf("build TTS provider: %w", err)
	}
	return Providers{ASR: recognizer, Assistant: conversation, Translation: translator, TTS: synthesizer}, nil
}

func buildAssistant(config TranslationConfig, offline assistant.Provider, rawLogger *rawlog.Logger) (assistant.Provider, error) {
	switch normalizedProvider(config.Provider) {
	case ProviderMock:
		// Assistant remains optional for offline callers. When it is absent, runtime registers
		// only the interpretation Handler and the command registry reflects that capability set.
		return offline, nil
	case ProviderAliyun:
		model := strings.TrimSpace(config.Model)
		if model == "" {
			model = defaultTranslationModel
		}
		if !strings.EqualFold(model, defaultTranslationModel) {
			return nil, fmt.Errorf("%w: LLM_MODEL=%q (want %s)", ErrUnsupportedModel, model, defaultTranslationModel)
		}
		return assistantqwen.NewProvider(assistantqwen.Config{
			APIKey: config.APIKey, BaseURL: config.BaseURL, Model: defaultTranslationModel,
			Provider: string(ProviderAliyun), EnableThinking: config.EnableThinking, Timeout: config.Timeout, RawLogger: rawLogger,
		})
	default:
		return nil, unsupportedProvider(config.Provider)
	}
}

// BuildProvidersFromEnvironment is the startup boundary for provider selection.
func BuildProvidersFromEnvironment(offline Providers) (Providers, error) {
	config, err := LoadProviderConfigFromEnvironment()
	if err != nil {
		return Providers{}, err
	}
	return BuildProviders(config, offline)
}

func buildASR(config ASRConfig, offline asr.Provider, rawLogger *rawlog.Logger) (asr.Provider, error) {
	switch normalizedProvider(config.Provider) {
	case ProviderMock:
		if offline == nil {
			return nil, fmt.Errorf("%w: ASR", ErrMockProviderRequired)
		}
		return offline, nil
	case ProviderAliyun:
		if err := validateASRConfig(config); err != nil {
			return nil, err
		}
		return asrqwen.NewProvider(asrqwen.Config{
			APIKey: config.APIKey, BaseURL: config.BaseURL, WebSocketURL: config.WebSocketURL,
			Model: config.Model, Provider: string(ProviderAliyun), SampleRate: config.SampleRate,
			VADThreshold: config.VADThreshold, SilenceDuration: config.SilenceDuration,
			DisableServerVAD: !config.ServerVAD, RawLogger: rawLogger,
		})
	default:
		return nil, unsupportedProvider(config.Provider)
	}
}

func validateASRConfig(config ASRConfig) error {
	if config.SampleRate != 0 && config.SampleRate != 8000 && config.SampleRate != 16000 {
		return fmt.Errorf("%w: ASR_SAMPLE_RATE=%d", ErrInvalidEnvironmentValue, config.SampleRate)
	}
	if math.IsNaN(config.VADThreshold) || math.IsInf(config.VADThreshold, 0) || config.VADThreshold < -1 || config.VADThreshold > 1 {
		return fmt.Errorf("%w: ASR_VAD_THRESHOLD=%v", ErrInvalidEnvironmentValue, config.VADThreshold)
	}
	if config.SilenceDuration != 0 && (config.SilenceDuration < 200*time.Millisecond || config.SilenceDuration > 6000*time.Millisecond) {
		return fmt.Errorf("%w: ASR_SILENCE_DURATION_MS=%d", ErrInvalidEnvironmentValue, config.SilenceDuration.Milliseconds())
	}
	return nil
}

func buildTranslation(config TranslationConfig, offline translate.Provider, rawLogger *rawlog.Logger) (translate.Provider, error) {
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
			Provider: string(ProviderAliyun), EnableThinking: config.EnableThinking, Timeout: config.Timeout, RawLogger: rawLogger,
		})
	default:
		return nil, unsupportedProvider(config.Provider)
	}
}

func buildTTS(config TTSConfig, offline tts.Provider, rawLogger *rawlog.Logger) (tts.Provider, error) {
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
			SampleRate: config.SampleRate, Timeout: config.Timeout, RawLogger: rawLogger,
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
