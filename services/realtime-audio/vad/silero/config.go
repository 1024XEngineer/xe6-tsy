package silero

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

const (
	ProviderSilero = "silero"
	ProviderEnergy = "energy"

	envProvider     = "LOCAL_VAD_PROVIDER"
	envLibraryPath  = "ONNXRUNTIME_SHARED_LIBRARY_PATH"
	envModelPath    = "LOCAL_VAD_MODEL_PATH"
	envThreshold    = "LOCAL_VAD_THRESHOLD"
	envNegThreshold = "LOCAL_VAD_NEG_THRESHOLD"
)

// LocalConfig selects and configures the process-local utterance VAD.
type LocalConfig struct {
	Provider     string
	LibraryPath  string
	ModelPath    string
	Threshold    float64
	NegThreshold float64
}

// LoadLocalConfigFromEnv reads LOCAL_VAD_* / ONNXRUNTIME_* settings.
// Default provider is silero so the realtime entrypoint uses the production classifier.
func LoadLocalConfigFromEnv(getenv func(string) string) LocalConfig {
	if getenv == nil {
		getenv = os.Getenv
	}
	provider := strings.ToLower(strings.TrimSpace(getenv(envProvider)))
	if provider == "" {
		provider = ProviderSilero
	}
	cfg := LocalConfig{
		Provider:    provider,
		LibraryPath: strings.TrimSpace(getenv(envLibraryPath)),
		ModelPath:   strings.TrimSpace(getenv(envModelPath)),
	}
	if cfg.LibraryPath == "" {
		cfg.LibraryPath = defaultLibraryPath()
	}
	if cfg.ModelPath == "" {
		cfg.ModelPath = defaultModelPath()
	}
	if v := strings.TrimSpace(getenv(envThreshold)); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.Threshold = f
		}
	}
	if v := strings.TrimSpace(getenv(envNegThreshold)); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.NegThreshold = f
		}
	}
	return cfg
}

func defaultLibraryPath() string {
	candidates := []string{
		filepath.Join("third_party", "onnxruntime", "lib", libraryFileName()),
		filepath.Join("lib", libraryFileName()),
	}
	for _, candidate := range candidates {
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			return candidate
		}
	}
	return candidates[0]
}

func defaultModelPath() string {
	candidates := []string{
		filepath.Join("vad", "silero", "silero_vad.onnx"),
		"silero_vad.onnx",
	}
	for _, candidate := range candidates {
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			return candidate
		}
	}
	return candidates[0]
}

func libraryFileName() string {
	switch runtime.GOOS {
	case "windows":
		return "onnxruntime.dll"
	case "darwin":
		return "libonnxruntime.dylib"
	default:
		return "libonnxruntime.so"
	}
}
