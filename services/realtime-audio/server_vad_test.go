package main

import (
	"os"
	"testing"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/audio"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/localruntime"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/vad/silero"
)

func TestNewLocalVADSegmenterFactoryEnergy(t *testing.T) {
	segmenters, classifiers, err := newLocalVADFactories(func(key string) string {
		if key == "LOCAL_VAD_PROVIDER" {
			return silero.ProviderEnergy
		}
		return ""
	})
	if err != nil {
		t.Fatalf("factory error = %v", err)
	}
	segmenter, err := segmenters()
	if err != nil {
		t.Fatalf("NewSegmenter error = %v", err)
	}
	if segmenter == nil {
		t.Fatal("segmenter is nil")
	}
	classifier, err := classifiers()
	if err != nil || classifier == nil {
		t.Fatalf("command classifier = %#v, %v", classifier, err)
	}
	energy, ok := classifier.(localruntime.EnergySpeechClassifier)
	if !ok {
		t.Fatalf("classifier type = %T, want EnergySpeechClassifier", classifier)
	}
	if energy.Threshold != 0.01 {
		t.Fatalf("energy threshold = %v, want 0.01", energy.Threshold)
	}
	if !energy.Speech(audio.Frame{PCM: []byte{0xff, 0x7f}}) {
		t.Fatal("maximum-amplitude frame was not classified as speech")
	}
}

func TestNewLocalVADSegmenterFactoryRejectsUnknown(t *testing.T) {
	_, _, err := newLocalVADFactories(func(key string) string {
		if key == "LOCAL_VAD_PROVIDER" {
			return "other"
		}
		return ""
	})
	if err == nil {
		t.Fatal("expected unsupported provider error")
	}
}

func TestNewLocalVADSegmenterFactorySileroLoadsWhenAssetsPresent(t *testing.T) {
	library := `third_party\onnxruntime\lib\onnxruntime.dll`
	model := `vad\silero\silero_vad.onnx`
	if _, err := os.Stat(library); err != nil {
		t.Skipf("onnxruntime missing: %v", err)
	}
	if _, err := os.Stat(model); err != nil {
		t.Skipf("silero model missing: %v", err)
	}
	segmenters, classifiers, err := newLocalVADFactories(func(key string) string {
		switch key {
		case "LOCAL_VAD_PROVIDER":
			return silero.ProviderSilero
		case "ONNXRUNTIME_SHARED_LIBRARY_PATH":
			return library
		case "LOCAL_VAD_MODEL_PATH":
			return model
		default:
			return ""
		}
	})
	if err != nil {
		t.Fatalf("silero factory error = %v", err)
	}
	segmenter, err := segmenters()
	if err != nil {
		t.Fatalf("NewSegmenter error = %v", err)
	}
	if segmenter == nil {
		t.Fatal("segmenter is nil")
	}
	classifier, err := classifiers()
	if err != nil || classifier == nil {
		t.Fatalf("command classifier = %#v, %v", classifier, err)
	}
}
