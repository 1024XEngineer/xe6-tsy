// Package rawlog persists provider request/response JSON for one realtime session.
package rawlog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Logger writes append-only JSONL files grouped by session and provider kind.
// The payload is kept as raw JSON so provider-specific fields are not lost.
type Logger struct {
	dir string
	mu  sync.Mutex
}

type entry struct {
	LoggedAt  string          `json:"logged_at"`
	Direction string          `json:"direction"`
	Event     string          `json:"event"`
	Payload   json.RawMessage `json:"payload"`
}

// New constructs a logger. The directory is created lazily on first write.
func New(dir string) *Logger {
	if strings.TrimSpace(dir) == "" {
		dir = DefaultDir()
	}
	return &Logger{dir: dir}
}

// Default returns a logger rooted at REALTIME_RAW_LOG_DIR, or the repository
// log directory when the realtime service is started from either the repo root
// or services/realtime-audio.
func Default() *Logger { return New(DefaultDir()) }

// DefaultDir resolves the requested local log directory without depending on
// the temporary executable path used by go run.
func DefaultDir() string {
	if dir := strings.TrimSpace(os.Getenv("REALTIME_RAW_LOG_DIR")); dir != "" {
		return dir
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "log"
	}
	candidates := []string{
		filepath.Join(cwd, "log"),
		filepath.Join(cwd, "..", "log"),
		filepath.Join(cwd, "..", "..", "log"),
	}
	for _, candidate := range candidates {
		if info, statErr := os.Stat(candidate); statErr == nil && info.IsDir() {
			return candidate
		}
	}
	return candidates[0]
}

// WriteJSON appends one provider event. kind must be asr, llm, or tts.
func (l *Logger) WriteJSON(sessionID, kind, direction, event string, payload []byte) error {
	if l == nil || strings.TrimSpace(sessionID) == "" || strings.TrimSpace(kind) == "" {
		return nil
	}
	if !json.Valid(payload) {
		return fmt.Errorf("raw provider payload is not valid JSON")
	}
	sessionID = safeComponent(sessionID)
	kind = safeComponent(kind)

	l.mu.Lock()
	defer l.mu.Unlock()
	if err := os.MkdirAll(filepath.Join(l.dir, sessionID), 0o755); err != nil {
		return err
	}
	path := filepath.Join(l.dir, sessionID, kind+".jsonl")
	line, err := json.Marshal(entry{
		LoggedAt: time.Now().UTC().Format(time.RFC3339Nano), Direction: direction,
		Event: event, Payload: json.RawMessage(append([]byte(nil), payload...)),
	})
	if err != nil {
		return err
	}
	line = append(line, '\n')
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(line)
	_ = file.Close()
	return writeErr
}

// EnsureSession creates the three stable per-session files up front, even if
// a session ends before one provider reaches its first request.
func (l *Logger) EnsureSession(sessionID string) error {
	if l == nil || strings.TrimSpace(sessionID) == "" {
		return nil
	}
	sessionID = safeComponent(sessionID)
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := os.MkdirAll(filepath.Join(l.dir, sessionID), 0o755); err != nil {
		return err
	}
	for _, kind := range []string{"asr", "llm", "tts"} {
		file, err := os.OpenFile(filepath.Join(l.dir, sessionID, kind+".jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		_ = file.Close()
	}
	return nil
}

// Close is retained for callers that own a logger. Writes close their file
// handle immediately, so process-wide loggers do not accumulate descriptors.
func (l *Logger) Close() error {
	return nil
}

func safeComponent(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	var builder strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			builder.WriteRune(r)
		}
	}
	if builder.Len() == 0 {
		return "unknown"
	}
	return builder.String()
}
