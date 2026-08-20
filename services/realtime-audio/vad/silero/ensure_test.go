package silero

import (
	"archive/tar"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
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

func TestEnsureAssetsEmptyProviderUsesSileroPath(t *testing.T) {
	cfg := LocalConfig{
		ModelPath:   filepath.Join(t.TempDir(), "missing.onnx"),
		LibraryPath: filepath.Join(t.TempDir(), "missing.dll"),
	}
	if err := EnsureAssets(&cfg); err == nil {
		t.Fatal("empty provider should use Silero asset validation")
	}
}

func TestDownloadONNXRuntimeRejectsCorruptArchive(t *testing.T) {
	asset, _, err := ortReleaseAsset()
	if err != nil {
		t.Skipf("unsupported platform: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/"+asset {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte("not an onnxruntime archive"))
	}))
	t.Cleanup(server.Close)

	previousURL := ortReleaseBaseURL
	ortReleaseBaseURL = server.URL + "/"
	t.Cleanup(func() { ortReleaseBaseURL = previousURL })

	if _, err := downloadONNXRuntime(t.TempDir()); err == nil {
		t.Fatal("expected corrupt archive error")
	}
}

func TestArchivesRejectCurrentDirectoryAndTarTraversal(t *testing.T) {
	zipPath := filepath.Join(t.TempDir(), "current-directory.zip")
	if err := writeZip(zipPath, map[string][]byte{".": []byte("nope")}); err != nil {
		t.Fatal(err)
	}
	if err := unzipArchive(zipPath, t.TempDir()); err == nil {
		t.Fatal("expected current directory zip path error")
	}

	tgzPath := filepath.Join(t.TempDir(), "traversal.tgz")
	if err := writeTarGz(tgzPath, map[string][]byte{"../escape.txt": []byte("nope")}); err != nil {
		t.Fatal(err)
	}
	if err := untarGzipArchive(tgzPath, t.TempDir()); err == nil {
		t.Fatal("expected unsafe tar path error")
	}
}

func TestResolveInstalledLibraryRejectsDirectoryAndUnexpectedLayout(t *testing.T) {
	root := t.TempDir()
	_, libraryRel, err := ortReleaseAsset()
	if err != nil {
		t.Skipf("unsupported platform: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, libraryRel), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveInstalledLibrary(root, libraryRel); err == nil {
		t.Fatal("expected directory to be rejected as library")
	}

	emptyRoot := t.TempDir()
	if _, err := firstSubdir(emptyRoot); err == nil {
		t.Fatal("expected unexpected archive layout error")
	}
}

func TestUntarGzipSkipsSymlinkAndContinues(t *testing.T) {
	tgzPath := filepath.Join(t.TempDir(), "symlink-then-library.tgz")
	f, err := os.Create(tgzPath)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "package/link", Typeflag: tar.TypeSymlink, Linkname: "outside"}); err != nil {
		t.Fatal(err)
	}
	body := []byte("library")
	if err := tw.WriteHeader(&tar.Header{Name: "package/lib/onnxruntime.dll", Mode: 0o644, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	destDir := filepath.Join(t.TempDir(), "extract")
	if err := untarGzipArchive(tgzPath, destDir); err != nil {
		t.Fatalf("untarGzipArchive() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(destDir, "package", "link")); !os.IsNotExist(err) {
		t.Fatalf("symlink path exists or returned unexpected error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(destDir, "package", "lib", "onnxruntime.dll")); err != nil {
		t.Fatalf("library after symlink was not extracted: %v", err)
	}
}
