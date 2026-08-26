package rawlog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestLoggerCreatesThreeSessionFilesAndPreservesPayload(t *testing.T) {
	dir := t.TempDir()
	logger := New(dir)
	defer logger.Close()
	if err := logger.EnsureSession("vs_session-1"); err != nil {
		t.Fatalf("EnsureSession() error = %v", err)
	}
	info, err := os.Stat(filepath.Join(dir, "vs_session-1"))
	if err != nil {
		t.Fatalf("stat session directory: %v", err)
	}
	if runtime.GOOS != "windows" {
		if mode := info.Mode().Perm(); mode != 0o700 {
			t.Fatalf("session directory mode = %o, want 700", mode)
		}
	}
	payload := []byte(`{"type":"response.audio.delta","delta":"abc"}`)
	if err := logger.WriteJSON("vs_session-1", "tts", "response", "response.audio.delta", payload); err != nil {
		t.Fatalf("WriteJSON() error = %v", err)
	}
	for _, kind := range []string{"asr", "llm", "tts"} {
		path := filepath.Join(dir, "vs_session-1", kind+".jsonl")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", kind, err)
		}
		if kind != "tts" && len(data) != 0 {
			t.Fatalf("%s unexpectedly has data: %s", kind, data)
		}
		if kind == "tts" {
			info, err := os.Stat(path)
			if err != nil {
				t.Fatalf("stat %s: %v", kind, err)
			}
			if runtime.GOOS != "windows" {
				if mode := info.Mode().Perm(); mode != 0o600 {
					t.Fatalf("%s mode = %o, want 600", kind, mode)
				}
			}
			var line struct {
				Payload json.RawMessage `json:"payload"`
			}
			if err := json.Unmarshal(data, &line); err != nil {
				t.Fatalf("decode line: %v", err)
			}
			if string(line.Payload) != string(payload) {
				t.Fatalf("payload = %s, want %s", line.Payload, payload)
			}
		}
	}
}
