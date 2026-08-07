package silero

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/audio"
)

// TestRuntimeLiveSpeech covers NewRuntime / inferLocked / Close against real
// ONNX Runtime when assets are present (local third_party or CI preinstall).
// Skips offline when the shared library is missing so default unit tests stay offline.
func TestRuntimeLiveSpeech(t *testing.T) {
	library, model, ok := liveORTAssets()
	if !ok {
		t.Skip("onnxruntime shared library or silero model unavailable")
	}

	rt, err := NewRuntime(RuntimeConfig{
		LibraryPath:    library,
		ModelPath:      model,
		Threshold:      0.5,
		NegThreshold:   0.35,
		IntraOpThreads: 1,
	})
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	t.Cleanup(func() {
		if err := rt.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	classifier, err := rt.NewClassifier()
	if err != nil {
		t.Fatalf("NewClassifier() error = %v", err)
	}

	// Silence windows should stay below speech threshold.
	silence := make([]byte, WindowSamples*2)
	silFrame, err := audio.NewFrame(silence, audio.SupportedSampleRate, time.Unix(1, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if classifier.Speech(silFrame) {
		t.Fatal("silence window should not report speech")
	}
	if err := classifier.Err(); err != nil {
		t.Fatalf("silence infer error: %v", err)
	}
	if classifier.WindowRuns() < 1 {
		t.Fatal("expected at least one scored silence window")
	}

	speechPCM, err := os.ReadFile(filepath.Join("testdata", "speech_16k_s16le.pcm"))
	if err != nil {
		t.Fatalf("read speech fixture: %v", err)
	}
	frameBytes := WindowSamples * 2
	base := time.Unix(2, 0).UTC()
	var heard bool
	for off, i := 0, 0; off+frameBytes <= len(speechPCM); off, i = off+frameBytes, i+1 {
		frame, err := audio.NewFrame(speechPCM[off:off+frameBytes], audio.SupportedSampleRate, base.Add(time.Duration(i)*32*time.Millisecond))
		if err != nil {
			t.Fatal(err)
		}
		if classifier.Speech(frame) {
			heard = true
			break
		}
		if err := classifier.Err(); err != nil {
			t.Fatalf("speech infer error: %v", err)
		}
	}
	if !heard {
		t.Fatalf("expected speech on fixture (lastProb=%.4f runs=%d)", classifier.LastProbability(), classifier.WindowRuns())
	}

	// Destroy path must tolerate a second Close from cleanup after explicit close.
	if err := rt.Close(); err != nil {
		t.Fatalf("explicit Close() error = %v", err)
	}
}

func TestNewRuntimeRejectsCorruptModel(t *testing.T) {
	library, _, ok := liveORTAssets()
	if !ok {
		t.Skip("onnxruntime shared library unavailable")
	}
	model := filepath.Join(t.TempDir(), "bad.onnx")
	if err := os.WriteFile(model, []byte("not-an-onnx-model"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewRuntime(RuntimeConfig{LibraryPath: library, ModelPath: model}); err == nil {
		t.Fatal("expected corrupt model error")
	}
}

func liveORTAssets() (library, model string, ok bool) {
	modelCandidates := []string{
		"silero_vad.onnx",
		filepath.Join("vad", "silero", "silero_vad.onnx"),
	}
	for _, candidate := range modelCandidates {
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			model = candidate
			break
		}
	}
	if model == "" {
		return "", "", false
	}

	libraryCandidates := []string{
		filepath.Join("..", "..", "third_party", "onnxruntime", "lib", libraryFileName()),
		filepath.Join("third_party", "onnxruntime", "lib", libraryFileName()),
		os.Getenv(envLibraryPath),
	}
	if runtimeGOOSIsLinux() {
		libraryCandidates = append(libraryCandidates,
			filepath.Join("..", "..", "third_party", "onnxruntime", "lib", "libonnxruntime.so."+ortVersion),
			filepath.Join("third_party", "onnxruntime", "lib", "libonnxruntime.so."+ortVersion),
		)
	}
	for _, candidate := range libraryCandidates {
		if candidate == "" {
			continue
		}
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			return candidate, model, true
		}
	}
	return "", "", false
}

func runtimeGOOSIsLinux() bool {
	return libraryFileName() == "libonnxruntime.so"
}
