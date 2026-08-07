package silero

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureAssetsNoopsWhenLibraryPresent(t *testing.T) {
	dir := t.TempDir()
	library := filepath.Join(dir, "onnxruntime.dll")
	model := filepath.Join(dir, "silero_vad.onnx")
	if err := os.WriteFile(library, []byte("dll"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(model, []byte("onnx"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := LocalConfig{
		Provider:    ProviderSilero,
		LibraryPath: library,
		ModelPath:   model,
	}
	if err := EnsureAssets(&cfg); err != nil {
		t.Fatalf("EnsureAssets() error = %v", err)
	}
	if cfg.LibraryPath != library {
		t.Fatalf("LibraryPath changed to %q", cfg.LibraryPath)
	}
}

func TestEnsureAssetsRequiresModel(t *testing.T) {
	cfg := LocalConfig{
		Provider:    ProviderSilero,
		LibraryPath: filepath.Join(t.TempDir(), "missing.dll"),
		ModelPath:   filepath.Join(t.TempDir(), "missing.onnx"),
	}
	if err := EnsureAssets(&cfg); err == nil {
		t.Fatal("expected missing model error")
	}
}
