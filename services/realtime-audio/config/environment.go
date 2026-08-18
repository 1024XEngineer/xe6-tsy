// Package config loads realtime-audio settings without coupling core packages to environment variables.
package config

import (
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"time"
)

const defaultTranslationModel = "qwen3.6-flash"

type ProviderName string

type CommandInterpreterName string

const (
	ProviderMock             ProviderName           = "mock"
	ProviderAliyun           ProviderName           = "aliyun"
	CommandInterpreterLegacy CommandInterpreterName = "legacy"
	CommandInterpreterQwen   CommandInterpreterName = "qwen"
)

var (
	ErrEnvironmentLookupRequired = errors.New("environment lookup is required")
	ErrUnsupportedProvider       = errors.New("unsupported realtime provider")
	ErrUnsupportedModel          = errors.New("unsupported realtime model")
	ErrInvalidEnvironmentValue   = errors.New("invalid realtime environment value")
)

// ProviderConfig contains the vendor-neutral selection and vendor transport settings.
type ProviderConfig struct {
	ASR         ASRConfig
	Translation TranslationConfig
	TTS         TTSConfig
	Command     CommandConfig
}

type ASRConfig struct {
	Provider        ProviderName
	APIKey          string
	BaseURL         string
	WebSocketURL    string
	Model           string
	SampleRate      int
	VADThreshold    float64
	SilenceDuration time.Duration
	// ServerVAD enables Qwen turn_detection.server_vad. Default true when unset.
	ServerVAD bool
}

type TranslationConfig struct {
	Provider       ProviderName
	APIKey         string
	BaseURL        string
	Model          string
	EnableThinking bool
	Timeout        time.Duration
}

type TTSConfig struct {
	Provider   ProviderName
	APIKey     string
	BaseURL    string
	Model      string
	Voice      string
	SampleRate int
	Timeout    time.Duration
}

// CommandConfig selects the semantic command boundary independently from ordinary assistant
// replies. Qwen may reuse transport credentials while retaining its own timeout and model.
type CommandConfig struct {
	Interpreter CommandInterpreterName
	APIKey      string
	BaseURL     string
	Model       string
	Timeout     time.Duration
}

type LookupEnv func(key string) (string, bool)

// LoadProviderConfig reads settings through an injected lookup for deterministic tests.
func LoadProviderConfig(lookup LookupEnv) (ProviderConfig, error) {
	if lookup == nil {
		return ProviderConfig{}, ErrEnvironmentLookupRequired
	}

	asrProvider, err := readProvider(lookup, "ASR_PROVIDER")
	if err != nil {
		return ProviderConfig{}, err
	}
	translationProvider, err := readProvider(lookup, "LLM_PROVIDER")
	if err != nil {
		return ProviderConfig{}, err
	}
	ttsProvider, err := readProvider(lookup, "TTS_PROVIDER")
	if err != nil {
		return ProviderConfig{}, err
	}
	commandInterpreter, err := readCommandInterpreter(lookup)
	if err != nil {
		return ProviderConfig{}, err
	}

	sampleRate, err := readInt(lookup, "ASR_SAMPLE_RATE")
	if err != nil {
		return ProviderConfig{}, err
	}
	vadThreshold, err := readFloat(lookup, "ASR_VAD_THRESHOLD")
	if err != nil {
		return ProviderConfig{}, err
	}
	silenceDuration, err := readMilliseconds(lookup, "ASR_SILENCE_DURATION_MS")
	if err != nil {
		return ProviderConfig{}, err
	}
	serverVAD, err := readBoolDefault(lookup, "ASR_SERVER_VAD", true)
	if err != nil {
		return ProviderConfig{}, err
	}
	if vadThreshold < -1 || vadThreshold > 1 {
		return ProviderConfig{}, invalidValue("ASR_VAD_THRESHOLD", value(lookup, "ASR_VAD_THRESHOLD"))
	}
	if silenceDuration != 0 && (silenceDuration < 200*time.Millisecond || silenceDuration > 6000*time.Millisecond) {
		return ProviderConfig{}, invalidValue("ASR_SILENCE_DURATION_MS", value(lookup, "ASR_SILENCE_DURATION_MS"))
	}
	if sampleRate != 0 && sampleRate != 8000 && sampleRate != 16000 {
		return ProviderConfig{}, invalidValue("ASR_SAMPLE_RATE", value(lookup, "ASR_SAMPLE_RATE"))
	}
	translationModel, err := readTranslationModel(lookup)
	if err != nil {
		return ProviderConfig{}, err
	}
	enableThinking, err := readBool(lookup, "LLM_ENABLE_THINKING")
	if err != nil {
		return ProviderConfig{}, err
	}
	translationTimeout, err := readMilliseconds(lookup, "LLM_TIMEOUT_MS")
	if err != nil {
		return ProviderConfig{}, err
	}
	ttsSampleRate, err := readInt(lookup, "TTS_SAMPLE_RATE")
	if err != nil {
		return ProviderConfig{}, err
	}
	ttsTimeout, err := readMilliseconds(lookup, "TTS_TIMEOUT_MS")
	if err != nil {
		return ProviderConfig{}, err
	}
	commandTimeout, err := readMilliseconds(lookup, "COMMAND_LLM_TIMEOUT_MS")
	if err != nil {
		return ProviderConfig{}, err
	}
	commandModel := value(lookup, "COMMAND_LLM_MODEL")
	if commandModel == "" {
		commandModel = translationModel
	}
	commandAPIKey := value(lookup, "COMMAND_LLM_API_KEY")
	commandBaseURL := value(lookup, "COMMAND_LLM_BASE_URL")
	if (commandAPIKey == "") != (commandBaseURL == "") {
		return ProviderConfig{}, fmt.Errorf(
			"%w: COMMAND_LLM_API_KEY and COMMAND_LLM_BASE_URL must be configured together",
			ErrInvalidEnvironmentValue,
		)
	}
	if commandAPIKey == "" {
		commandAPIKey = value(lookup, "LLM_API_KEY")
		commandBaseURL = value(lookup, "LLM_BASE_URL")
	}

	return ProviderConfig{
		ASR: ASRConfig{
			Provider: asrProvider, APIKey: value(lookup, "ASR_API_KEY"),
			BaseURL: value(lookup, "ASR_BASE_URL"), WebSocketURL: value(lookup, "ASR_WEBSOCKET_URL"),
			Model: value(lookup, "ASR_MODEL"), SampleRate: sampleRate,
			VADThreshold: vadThreshold, SilenceDuration: silenceDuration, ServerVAD: serverVAD,
		},
		Translation: TranslationConfig{
			Provider: translationProvider, APIKey: value(lookup, "LLM_API_KEY"),
			BaseURL: value(lookup, "LLM_BASE_URL"), Model: translationModel,
			EnableThinking: enableThinking, Timeout: translationTimeout,
		},
		TTS: TTSConfig{
			Provider: ttsProvider, APIKey: value(lookup, "TTS_API_KEY"),
			BaseURL: value(lookup, "TTS_BASE_URL"), Model: value(lookup, "TTS_MODEL"),
			Voice: value(lookup, "TTS_VOICE"), SampleRate: ttsSampleRate, Timeout: ttsTimeout,
		},
		Command: CommandConfig{
			Interpreter: commandInterpreter, APIKey: commandAPIKey, BaseURL: commandBaseURL,
			Model: commandModel, Timeout: commandTimeout,
		},
	}, nil
}

func readCommandInterpreter(lookup LookupEnv) (CommandInterpreterName, error) {
	raw := strings.ToLower(value(lookup, "COMMAND_INTERPRETER"))
	if raw == "" {
		return CommandInterpreterLegacy, nil
	}
	interpreter := CommandInterpreterName(raw)
	if interpreter != CommandInterpreterLegacy && interpreter != CommandInterpreterQwen {
		return "", fmt.Errorf("%w: COMMAND_INTERPRETER=%q", ErrUnsupportedProvider, raw)
	}
	return interpreter, nil
}

// LoadProviderConfigFromEnvironment reads the process environment without loading .env files.
func LoadProviderConfigFromEnvironment() (ProviderConfig, error) {
	return LoadProviderConfig(os.LookupEnv)
}

func readProvider(lookup LookupEnv, key string) (ProviderName, error) {
	raw := strings.ToLower(value(lookup, key))
	if raw == "" {
		return ProviderMock, nil
	}
	provider := ProviderName(raw)
	if provider != ProviderMock && provider != ProviderAliyun {
		return "", fmt.Errorf("%w: %s=%q", ErrUnsupportedProvider, key, raw)
	}
	return provider, nil
}

func readInt(lookup LookupEnv, key string) (int, error) {
	raw := value(lookup, key)
	if raw == "" {
		return 0, nil
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed < 0 {
		return 0, invalidValue(key, raw)
	}
	return parsed, nil
}

func readFloat(lookup LookupEnv, key string) (float64, error) {
	raw := value(lookup, key)
	if raw == "" {
		return 0, nil
	}
	parsed, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
		return 0, invalidValue(key, raw)
	}
	return parsed, nil
}

func readTranslationModel(lookup LookupEnv) (string, error) {
	raw := value(lookup, "LLM_MODEL")
	if raw == "" {
		return defaultTranslationModel, nil
	}
	if !strings.EqualFold(raw, defaultTranslationModel) {
		return "", fmt.Errorf("%w: LLM_MODEL=%q (want %s)", ErrUnsupportedModel, raw, defaultTranslationModel)
	}
	return defaultTranslationModel, nil
}

func readBool(lookup LookupEnv, key string) (bool, error) {
	raw := value(lookup, key)
	if raw == "" {
		return false, nil
	}
	parsed, err := strconv.ParseBool(raw)
	if err != nil {
		return false, invalidValue(key, raw)
	}
	return parsed, nil
}

func readBoolDefault(lookup LookupEnv, key string, defaultValue bool) (bool, error) {
	raw := value(lookup, key)
	if raw == "" {
		return defaultValue, nil
	}
	parsed, err := strconv.ParseBool(raw)
	if err != nil {
		return false, invalidValue(key, raw)
	}
	return parsed, nil
}

func readMilliseconds(lookup LookupEnv, key string) (time.Duration, error) {
	milliseconds, err := readInt(lookup, key)
	if err != nil {
		return 0, err
	}
	return time.Duration(milliseconds) * time.Millisecond, nil
}

func value(lookup LookupEnv, key string) string {
	raw, _ := lookup(key)
	return strings.TrimSpace(raw)
}

func invalidValue(key, raw string) error {
	return fmt.Errorf("%w: %s=%q", ErrInvalidEnvironmentValue, key, raw)
}
