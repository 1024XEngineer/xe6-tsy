package silero

import (
	"archive/zip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	ortVersion     = "1.24.1"
	ortDownloadUA  = "xe6-tsy-realtime-audio-silero"
	defaultORTRoot = "third_party/onnxruntime"
)

// EnsureAssets makes sure the ONNX Runtime shared library exists for Silero.
// When the configured library path is missing, it downloads the matching
// official ONNX Runtime release into third_party/onnxruntime and updates
// cfg.LibraryPath. The Silero model file must already be vendored in-repo.
func EnsureAssets(cfg *LocalConfig) error {
	if cfg == nil {
		return fmt.Errorf("silero local config is required")
	}
	if cfg.Provider != "" && cfg.Provider != ProviderSilero {
		return nil
	}
	if cfg.ModelPath == "" {
		cfg.ModelPath = defaultModelPath()
	}
	if _, err := os.Stat(cfg.ModelPath); err != nil {
		return fmt.Errorf("silero model %q: %w", cfg.ModelPath, err)
	}
	if cfg.LibraryPath == "" {
		cfg.LibraryPath = defaultLibraryPath()
	}
	if st, err := os.Stat(cfg.LibraryPath); err == nil && !st.IsDir() {
		return nil
	}

	libraryPath, err := downloadONNXRuntime(defaultORTRoot)
	if err != nil {
		return err
	}
	cfg.LibraryPath = libraryPath
	return nil
}

func downloadONNXRuntime(destRoot string) (string, error) {
	asset, libraryRel, err := ortReleaseAsset()
	if err != nil {
		return "", err
	}
	libraryPath := filepath.Join(destRoot, libraryRel)
	if st, err := os.Stat(libraryPath); err == nil && !st.IsDir() {
		return libraryPath, nil
	}
	if err := os.MkdirAll(destRoot, 0o755); err != nil {
		return "", fmt.Errorf("create onnxruntime dir: %w", err)
	}

	url := "https://github.com/microsoft/onnxruntime/releases/download/v" + ortVersion + "/" + asset
	zipPath := filepath.Join(destRoot, "ort-download.zip")
	if err := downloadFile(url, zipPath); err != nil {
		return "", err
	}
	defer os.Remove(zipPath)

	extractRoot := filepath.Join(destRoot, "extract")
	_ = os.RemoveAll(extractRoot)
	if err := unzipArchive(zipPath, extractRoot); err != nil {
		return "", err
	}
	defer os.RemoveAll(extractRoot)

	inner, err := firstSubdir(extractRoot)
	if err != nil {
		return "", err
	}
	if err := copyDirContents(inner, destRoot); err != nil {
		return "", err
	}
	if st, err := os.Stat(libraryPath); err != nil || st.IsDir() {
		return "", fmt.Errorf("onnxruntime download finished but %q is missing", libraryPath)
	}
	return libraryPath, nil
}

func ortReleaseAsset() (asset string, libraryRel string, err error) {
	// Auto-download currently covers the Windows local-dev path used by start-local.
	// Other platforms can still point ONNXRUNTIME_SHARED_LIBRARY_PATH at a preinstalled library.
	if runtime.GOOS == "windows" && runtime.GOARCH == "amd64" {
		return "onnxruntime-win-x64-" + ortVersion + ".zip", filepath.Join("lib", "onnxruntime.dll"), nil
	}
	return "", "", fmt.Errorf("automatic onnxruntime download is not supported on %s/%s; install the shared library and set ONNXRUNTIME_SHARED_LIBRARY_PATH", runtime.GOOS, runtime.GOARCH)
}

func downloadFile(url, dest string) error {
	client := &http.Client{Timeout: 5 * time.Minute}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", ortDownloadUA)
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: HTTP %s", url, resp.Status)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := io.Copy(f, resp.Body); err != nil {
		return fmt.Errorf("write %s: %w", dest, err)
	}
	return nil
}

func unzipArchive(zipPath, destDir string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("open onnxruntime zip: %w", err)
	}
	defer r.Close()
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return err
	}
	for _, file := range r.File {
		if err := extractZipFile(file, destDir); err != nil {
			return err
		}
	}
	return nil
}

func extractZipFile(file *zip.File, destDir string) error {
	name := filepath.Clean(file.Name)
	if name == "." || strings.HasPrefix(name, "..") {
		return fmt.Errorf("refusing unsafe zip path %q", file.Name)
	}
	target := filepath.Join(destDir, name)
	if !strings.HasPrefix(target, filepath.Clean(destDir)+string(os.PathSeparator)) && target != filepath.Clean(destDir) {
		return fmt.Errorf("refusing zip path outside destination: %q", file.Name)
	}
	if file.FileInfo().IsDir() {
		return os.MkdirAll(target, 0o755)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	src, err := file.Open()
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, file.Mode())
	if err != nil {
		return err
	}
	defer dst.Close()
	_, err = io.Copy(dst, src)
	return err
}

func firstSubdir(root string) (string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			return filepath.Join(root, entry.Name()), nil
		}
	}
	return "", fmt.Errorf("onnxruntime archive layout unexpected under %s", root)
}

func copyDirContents(srcDir, destDir string) error {
	return filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		target := filepath.Join(destDir, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return copyFile(path, target)
	})
}

func copyFile(src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
