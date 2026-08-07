package silero

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/audio"
	ort "github.com/getcharzp/onnxruntime_purego"
)

func TestClassifierResetAndDiagnostics(t *testing.T) {
	c := &Classifier{
		runtime:    &Runtime{closed: true},
		threshold:  0.5,
		negThresh:  0.35,
		state:      make([]float32, stateSize),
		context:    make([]float32, contextSamples),
		pending:    []float32{0.1, 0.2},
		triggered:  true,
		lastProb:   0.9,
		windowRuns: 3,
		lastErr:    ErrRuntimeClosed,
	}
	c.Reset()
	if c.triggered || c.lastProb != 0 || c.windowRuns != 0 || c.lastErr != nil || len(c.pending) != 0 {
		t.Fatalf("Reset() left state: %+v", c)
	}
	if c.LastProbability() != 0 || c.WindowRuns() != 0 || c.Err() != nil {
		t.Fatal("diagnostics should be clear after Reset")
	}
	var nilClassifier *Classifier
	if nilClassifier.LastProbability() != 0 || nilClassifier.WindowRuns() != 0 || nilClassifier.Err() != nil {
		t.Fatal("nil classifier diagnostics should be zero values")
	}
	nilClassifier.Reset()
}

func TestClassifierSpeechInferErrorPath(t *testing.T) {
	c := &Classifier{
		runtime:   &Runtime{closed: true},
		threshold: 0.5,
		negThresh: 0.35,
		state:     make([]float32, stateSize),
		context:   make([]float32, contextSamples),
	}
	pcm := make([]byte, WindowSamples*2)
	frame, err := audio.NewFrame(pcm, audio.SupportedSampleRate, time.Unix(1, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if c.Speech(frame) {
		t.Fatal("closed runtime should not report speech")
	}
	if c.Err() == nil {
		t.Fatal("expected infer error after closed runtime window")
	}
	// Once failed, later frames stay false.
	if c.Speech(frame) {
		t.Fatal("classifier with lastErr should stay silent")
	}
}

func TestClassifierNilSpeech(t *testing.T) {
	var c *Classifier
	frame, err := audio.NewFrame(make([]byte, 4), audio.SupportedSampleRate, time.Unix(1, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if c.Speech(frame) {
		t.Fatal("nil classifier Speech should be false")
	}
}

func TestNewRuntimeValidation(t *testing.T) {
	if _, err := NewRuntime(RuntimeConfig{}); err == nil {
		t.Fatal("expected missing library/model error")
	}
	dir := t.TempDir()
	library := filepath.Join(dir, "missing.dll")
	model := filepath.Join(dir, "model.onnx")
	if err := os.WriteFile(model, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewRuntime(RuntimeConfig{LibraryPath: library, ModelPath: model}); err == nil {
		t.Fatal("expected missing library error")
	}
	if err := os.WriteFile(library, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewRuntime(RuntimeConfig{
		LibraryPath:  library,
		ModelPath:    model,
		Threshold:    0.2,
		NegThreshold: 0.5,
	}); err == nil {
		t.Fatal("expected NegThreshold validation error")
	}
}

func TestInferLockedValidation(t *testing.T) {
	rt := &Runtime{session: &ort.Session{}}
	window := make([]float32, WindowSamples)
	state := make([]float32, stateSize)
	context := make([]float32, contextSamples)

	if _, _, _, err := rt.inferLocked([]float32{1}, state, context); err == nil {
		t.Fatal("expected window size error")
	}
	if _, _, _, err := rt.inferLocked(window, []float32{1}, context); err == nil {
		t.Fatal("expected state size error")
	}
	if _, _, _, err := rt.inferLocked(window, state, []float32{1}); err == nil {
		t.Fatal("expected context size error")
	}

	closed := &Runtime{closed: true, session: &ort.Session{}}
	if _, _, _, err := closed.inferLocked(window, state, context); err == nil {
		t.Fatal("expected closed runtime error")
	}
}

func TestRuntimeCloseAndNewClassifierGuards(t *testing.T) {
	var rt *Runtime
	if err := rt.Close(); err != nil {
		t.Fatalf("nil Close() error = %v", err)
	}
	if _, err := rt.NewClassifier(); err == nil {
		t.Fatal("nil runtime NewClassifier should fail")
	}
	rt = &Runtime{closed: true}
	if _, err := rt.NewClassifier(); err == nil {
		t.Fatal("closed runtime NewClassifier should fail")
	}
	if err := rt.Close(); err != nil {
		t.Fatalf("idempotent Close() error = %v", err)
	}
}

func TestEnsureAssetsSkipsNonSilero(t *testing.T) {
	cfg := LocalConfig{Provider: ProviderEnergy}
	if err := EnsureAssets(&cfg); err != nil {
		t.Fatalf("EnsureAssets(energy) error = %v", err)
	}
}

func TestEnsureAssetsNilConfig(t *testing.T) {
	if err := EnsureAssets(nil); err == nil {
		t.Fatal("expected nil config error")
	}
}

func TestEnsureAssetsDownloadsMissingLibrary(t *testing.T) {
	asset, libraryRel, err := ortReleaseAsset()
	if err != nil {
		t.Skipf("unsupported platform: %v", err)
	}
	payload := map[string][]byte{
		"onnxruntime-pkg/" + filepath.ToSlash(libraryRel): []byte("shared-lib"),
		"onnxruntime-pkg/README.md":                       []byte("readme"),
	}
	var archiveBytes []byte
	if strings.HasSuffix(asset, ".zip") {
		archiveBytes = mustZipBytes(t, payload)
	} else {
		archiveBytes = mustTarGzBytes(t, payload)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/"+asset) {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(archiveBytes)
	}))
	t.Cleanup(server.Close)

	prevURL := ortReleaseBaseURL
	ortReleaseBaseURL = server.URL + "/"
	t.Cleanup(func() { ortReleaseBaseURL = prevURL })

	ortRoot := t.TempDir()
	prevRoot := defaultORTRoot
	defaultORTRoot = ortRoot
	t.Cleanup(func() { defaultORTRoot = prevRoot })

	model := filepath.Join(t.TempDir(), "silero_vad.onnx")
	if err := os.WriteFile(model, []byte("onnx"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := LocalConfig{
		Provider:    ProviderSilero,
		LibraryPath: filepath.Join(ortRoot, "missing", libraryFileName()),
		ModelPath:   model,
	}
	if err := EnsureAssets(&cfg); err != nil {
		t.Fatalf("EnsureAssets() error = %v", err)
	}
	if _, err := os.Stat(cfg.LibraryPath); err != nil {
		t.Fatalf("EnsureAssets did not install library: %v", err)
	}
}

func TestDownloadONNXRuntimeUsesExistingLibrary(t *testing.T) {
	dir := t.TempDir()
	_, libraryRel, err := ortReleaseAsset()
	if err != nil {
		t.Skipf("ort asset unavailable on this platform: %v", err)
	}
	path := filepath.Join(dir, libraryRel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("lib"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := downloadONNXRuntime(dir)
	if err != nil {
		t.Fatalf("downloadONNXRuntime() error = %v", err)
	}
	if got != path {
		t.Fatalf("library path = %q, want %q", got, path)
	}
}

func TestResolveInstalledLibraryFallback(t *testing.T) {
	dir := t.TempDir()
	_, libraryRel, err := ortReleaseAsset()
	if err != nil {
		t.Skipf("ort asset unavailable on this platform: %v", err)
	}
	primary := filepath.Join(dir, libraryRel)
	if err := os.MkdirAll(filepath.Dir(primary), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(primary, []byte("lib"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := resolveInstalledLibrary(dir, libraryRel)
	if err != nil {
		t.Fatalf("resolveInstalledLibrary() error = %v", err)
	}
	if got != primary {
		t.Fatalf("resolved = %q, want %q", got, primary)
	}
}

func TestDownloadONNXRuntimeFromLocalServer(t *testing.T) {
	asset, libraryRel, err := ortReleaseAsset()
	if err != nil {
		t.Skipf("unsupported platform: %v", err)
	}
	payload := map[string][]byte{
		"onnxruntime-pkg/" + filepath.ToSlash(libraryRel): []byte("shared-lib"),
		"onnxruntime-pkg/README.md":                       []byte("readme"),
	}
	var archiveBytes []byte
	if strings.HasSuffix(asset, ".zip") {
		archiveBytes = mustZipBytes(t, payload)
	} else {
		archiveBytes = mustTarGzBytes(t, payload)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/"+asset) {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(archiveBytes)
	}))
	t.Cleanup(server.Close)

	prev := ortReleaseBaseURL
	ortReleaseBaseURL = server.URL + "/"
	t.Cleanup(func() { ortReleaseBaseURL = prev })

	dest := t.TempDir()
	got, err := downloadONNXRuntime(dest)
	if err != nil {
		t.Fatalf("downloadONNXRuntime() error = %v", err)
	}
	if _, err := os.Stat(got); err != nil {
		t.Fatalf("installed library missing: %v", err)
	}
}

func TestDownloadFileAndHTTPErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ok" {
			_, _ = w.Write([]byte("payload"))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(server.Close)

	dest := filepath.Join(t.TempDir(), "file.bin")
	if err := downloadFile(server.URL+"/ok", dest); err != nil {
		t.Fatalf("downloadFile() error = %v", err)
	}
	body, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "payload" {
		t.Fatalf("body = %q", body)
	}
	if err := downloadFile(server.URL+"/missing", filepath.Join(t.TempDir(), "x.bin")); err == nil {
		t.Fatal("expected HTTP error")
	}
}

func TestOrtReleaseAssetCurrentPlatform(t *testing.T) {
	asset, libraryRel, err := ortReleaseAsset()
	if err != nil {
		t.Skipf("unsupported platform: %v", err)
	}
	if asset == "" || libraryRel == "" {
		t.Fatal("expected non-empty asset metadata")
	}
}

func TestUnzipAndCopyHelpers(t *testing.T) {
	srcDir := t.TempDir()
	zipPath := filepath.Join(t.TempDir(), "sample.zip")
	inner := filepath.Join(srcDir, "onnxruntime-win-x64")
	if err := os.MkdirAll(filepath.Join(inner, "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	payload := filepath.Join(inner, "lib", "onnxruntime.dll")
	if err := os.WriteFile(payload, []byte("dll"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeZip(zipPath, map[string][]byte{
		"onnxruntime-win-x64/lib/onnxruntime.dll": []byte("dll"),
		"onnxruntime-win-x64/README.md":           []byte("readme"),
	}); err != nil {
		t.Fatal(err)
	}

	extractDir := filepath.Join(t.TempDir(), "extract")
	if err := unzipArchive(zipPath, extractDir); err != nil {
		t.Fatalf("unzipArchive() error = %v", err)
	}
	subdir, err := firstSubdir(extractDir)
	if err != nil {
		t.Fatalf("firstSubdir() error = %v", err)
	}
	dest := t.TempDir()
	if err := copyDirContents(subdir, dest); err != nil {
		t.Fatalf("copyDirContents() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "lib", "onnxruntime.dll")); err != nil {
		t.Fatalf("copied dll missing: %v", err)
	}
}

func TestUntarGzipHelpers(t *testing.T) {
	tgzPath := filepath.Join(t.TempDir(), "sample.tgz")
	if err := writeTarGz(tgzPath, map[string][]byte{
		"onnxruntime-linux-x64/lib/libonnxruntime.so.1.24.1": []byte("so"),
		"onnxruntime-linux-x64/README.md":                    []byte("readme"),
	}); err != nil {
		t.Fatal(err)
	}
	extractDir := filepath.Join(t.TempDir(), "extract")
	if err := untarGzipArchive(tgzPath, extractDir); err != nil {
		t.Fatalf("untarGzipArchive() error = %v", err)
	}
	subdir, err := firstSubdir(extractDir)
	if err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	if err := copyDirContents(subdir, dest); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "lib", "libonnxruntime.so.1.24.1")); err != nil {
		t.Fatalf("copied so missing: %v", err)
	}
}

func TestArchiveRejectsUnsafePaths(t *testing.T) {
	zipPath := filepath.Join(t.TempDir(), "bad.zip")
	if err := writeZip(zipPath, map[string][]byte{"../escape.txt": []byte("nope")}); err != nil {
		t.Fatal(err)
	}
	if err := unzipArchive(zipPath, t.TempDir()); err == nil {
		t.Fatal("expected unsafe zip path error")
	}
}

func writeZip(path string, files map[string][]byte) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			return err
		}
		if _, err := w.Write(body); err != nil {
			return err
		}
	}
	return zw.Close()
}

func writeTarGz(path string, files map[string][]byte) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		hdr := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(body))}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if _, err := tw.Write(body); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

func mustZipBytes(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func mustTarGzBytes(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		hdr := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(body))}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
