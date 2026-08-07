package silero

import (
	"testing"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/audio"
)

func TestLoadLocalConfigDefaultsToSilero(t *testing.T) {
	cfg := LoadLocalConfigFromEnv(func(string) string { return "" })
	if cfg.Provider != ProviderSilero {
		t.Fatalf("Provider = %q, want %q", cfg.Provider, ProviderSilero)
	}
	if cfg.LibraryPath == "" || cfg.ModelPath == "" {
		t.Fatal("expected default library and model paths")
	}
}

func TestLoadLocalConfigReadsOverrides(t *testing.T) {
	cfg := LoadLocalConfigFromEnv(func(key string) string {
		switch key {
		case envProvider:
			return "energy"
		case envLibraryPath:
			return `C:\ort\onnxruntime.dll`
		case envModelPath:
			return `C:\models\silero_vad.onnx`
		case envThreshold:
			return "0.7"
		case envNegThreshold:
			return "0.4"
		default:
			return ""
		}
	})
	if cfg.Provider != ProviderEnergy || cfg.Threshold != 0.7 || cfg.NegThreshold != 0.4 {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	if cfg.LibraryPath != `C:\ort\onnxruntime.dll` || cfg.ModelPath != `C:\models\silero_vad.onnx` {
		t.Fatalf("paths = %q %q", cfg.LibraryPath, cfg.ModelPath)
	}
}

func TestClassifierHysteresisWithStub(t *testing.T) {
	rt := &Runtime{threshold: 0.5, negThresh: 0.35}
	c := &Classifier{
		runtime:   rt,
		threshold: 0.5,
		negThresh: 0.35,
		state:     make([]float32, stateSize),
	}
	// Bypass ONNX by feeding applyHysteresis directly through a test helper path:
	// build one full window of PCM and stub infer via lastErr-free empty runtime is hard.
	// Instead unit-test hysteresis helper behavior.
	if !c.applyHysteresis(0.8) || !c.triggered {
		t.Fatal("prob above threshold should start speech")
	}
	if !c.applyHysteresis(0.4) || !c.triggered {
		t.Fatal("prob between thresholds should keep speech")
	}
	if c.applyHysteresis(0.2) || c.triggered {
		t.Fatal("prob below neg threshold should end speech")
	}
}

func TestAppendPCM16AndSpeechBufferingWithoutInfer(t *testing.T) {
	// Partial window must not flip speech until 512 samples are available.
	c := &Classifier{
		runtime:   &Runtime{closed: true}, // infer will fail if reached
		threshold: 0.5,
		negThresh: 0.35,
		state:     make([]float32, stateSize),
		triggered: true, // sticky until a completed window says otherwise
	}
	pcm := make([]byte, 100*2) // 100 samples < 512
	frame, err := audio.NewFrame(pcm, audio.SupportedSampleRate, time.Unix(1, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if !c.Speech(frame) {
		t.Fatal("incomplete window should keep previous triggered state")
	}
	if len(c.pending) != 100 {
		t.Fatalf("pending samples = %d, want 100", len(c.pending))
	}
	if c.Err() != nil {
		t.Fatalf("unexpected infer attempt: %v", c.Err())
	}
}
