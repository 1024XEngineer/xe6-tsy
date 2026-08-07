//go:build integration

package silero_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/audio"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/vad"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/vad/silero"
)

func TestSileroClassifierOnSpeechPCM(t *testing.T) {
	pcmPath := filepath.Join("testdata", "speech_16k_s16le.pcm")
	raw, err := os.ReadFile(pcmPath)
	if err != nil {
		t.Skipf("speech fixture missing: %v", err)
	}
	library := filepath.Join("..", "..", "third_party", "onnxruntime", "lib", libraryName())
	rt, err := silero.NewRuntime(silero.RuntimeConfig{
		LibraryPath: library,
		ModelPath:   "silero_vad.onnx",
		Threshold:   0.5,
	})
	if err != nil {
		t.Skipf("silero runtime unavailable: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })

	classifier, err := rt.NewClassifier()
	if err != nil {
		t.Fatal(err)
	}
	segmenter, err := vad.NewSegmenter(classifier, vad.Options{
		SilenceAfter:  300 * time.Millisecond,
		MaxDuration:   8 * time.Second,
		PrefixPadding: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}

	base := time.Unix(1, 0).UTC()
	frameBytes := silero.WindowSamples * 2
	opened, final := false, false
	var maxProb float32
	for off, i := 0, 0; off+frameBytes <= len(raw); off, i = off+frameBytes, i+1 {
		frame, err := audio.NewFrame(raw[off:off+frameBytes], audio.SupportedSampleRate, base.Add(time.Duration(i)*32*time.Millisecond))
		if err != nil {
			t.Fatal(err)
		}
		events, err := segmenter.Push(context.Background(), frame)
		if err != nil {
			t.Fatal(err)
		}
		if classifier.LastProbability() > maxProb {
			maxProb = classifier.LastProbability()
		}
		for _, event := range events {
			switch event.Type {
			case vad.EventOpened:
				opened = true
			case vad.EventFinal:
				final = true
			}
		}
	}
	// Trailing silence to close the utterance if the fixture ends while speaking.
	for i := 0; i < 20; i++ {
		silence := make([]byte, frameBytes)
		frame, err := audio.NewFrame(silence, audio.SupportedSampleRate, base.Add(time.Duration(1000+i)*32*time.Millisecond))
		if err != nil {
			t.Fatal(err)
		}
		events, err := segmenter.Push(context.Background(), frame)
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range events {
			if event.Type == vad.EventFinal {
				final = true
			}
		}
	}
	t.Logf("maxProb=%.4f opened=%v final=%v runs=%d", maxProb, opened, final, classifier.WindowRuns())
	if err := classifier.Err(); err != nil {
		t.Fatal(err)
	}
	if maxProb < 0.5 {
		t.Fatalf("expected speech probability >= 0.5, got %.4f", maxProb)
	}
	if !opened {
		t.Fatal("expected utterance open on speech fixture")
	}
	if !final {
		t.Fatal("expected utterance final after trailing silence")
	}
}

func libraryName() string {
	switch runtime.GOOS {
	case "windows":
		return "onnxruntime.dll"
	case "darwin":
		return "libonnxruntime.dylib"
	default:
		return "libonnxruntime.so"
	}
}
