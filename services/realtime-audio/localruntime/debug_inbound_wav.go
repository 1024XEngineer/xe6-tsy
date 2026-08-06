// TEMPORARY DEBUG FEATURE — delete this entire file for production builds.
// Also remove the single wrapDebugInboundWAV call in frames.go.
//
// Inbound decoded PCM (16 kHz mono s16le, the same frames VAD/ASR consume) is
// accumulated and flushed to WAV files every chunk. Local mic/pipeline debugging only.

package localruntime

import (
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/audio"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/segment"
)

// Hardcoded temporary dump knobs — flip enabled to false or delete this file to retire.
const (
	debugInboundWAVEnabled       = true
	debugInboundWAVDir           = "debug-inbound-wav"
	debugInboundWAVChunkSeconds  = 10
	debugInboundWAVChannels      = 1
	debugInboundWAVBitsPerSample = 16
)

// wrapDebugInboundWAV tees inbound PCM to WAV files when the hardcoded dump flag is on.
// Call sites should stay as a single wrap so removal is one line.
func wrapDebugInboundWAV(source segment.FrameSource, sessionID string) segment.FrameSource {
	if source == nil || !debugInboundWAVEnabled {
		return source
	}
	dumper, err := newDebugInboundWAVSource(
		source,
		sessionID,
		debugInboundWAVDir,
		debugInboundWAVChunkSeconds,
		time.Now,
	)
	if err != nil {
		slog.Warn("debug inbound wav dump disabled", "error", err)
		return source
	}
	slog.Info("debug inbound wav dump enabled",
		"session_id", sessionID,
		"dir", debugInboundWAVDir,
		"chunk_seconds", debugInboundWAVChunkSeconds,
	)
	return dumper
}

type debugInboundWAVSource struct {
	inner      segment.FrameSource
	sessionID  string
	dir        string
	chunkBytes int
	now        func() time.Time

	mu      sync.Mutex
	buf     []byte
	part    int
	closed  bool
	writeFn func(path string, data []byte) error
}

func newDebugInboundWAVSource(
	inner segment.FrameSource,
	sessionID string,
	dir string,
	chunkSeconds int,
	now func() time.Time,
) (*debugInboundWAVSource, error) {
	if inner == nil {
		return nil, fmt.Errorf("debug inbound wav: inner source is required")
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("debug inbound wav: session id is required")
	}
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, fmt.Errorf("debug inbound wav: dir is required")
	}
	if chunkSeconds <= 0 {
		return nil, fmt.Errorf("debug inbound wav: chunk seconds must be positive")
	}
	if now == nil {
		now = time.Now
	}
	// Create the output dir lazily on first flush so Open() wrapping stays side-effect free.
	bytesPerSecond := audio.SupportedSampleRate * debugInboundWAVChannels * (debugInboundWAVBitsPerSample / 8)
	return &debugInboundWAVSource{
		inner:      inner,
		sessionID:  sanitizeDebugInboundWAVSessionID(sessionID),
		dir:        dir,
		chunkBytes: chunkSeconds * bytesPerSecond,
		now:        now,
		writeFn:    writeDebugInboundWAVFile,
	}, nil
}

func (s *debugInboundWAVSource) ReadFrame(ctx context.Context) (audio.Frame, error) {
	frame, err := s.inner.ReadFrame(ctx)
	if err != nil {
		return frame, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return frame, nil
	}
	s.buf = append(s.buf, frame.PCM...)
	for len(s.buf) >= s.chunkBytes {
		chunk := append([]byte(nil), s.buf[:s.chunkBytes]...)
		s.buf = s.buf[s.chunkBytes:]
		s.flushLocked(chunk)
	}
	return frame, nil
}

func (s *debugInboundWAVSource) Close() error {
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		if len(s.buf) > 0 {
			chunk := append([]byte(nil), s.buf...)
			s.buf = nil
			s.flushLocked(chunk)
		}
	}
	s.mu.Unlock()
	return s.inner.Close()
}

func (s *debugInboundWAVSource) flushLocked(pcm []byte) {
	if len(pcm) == 0 {
		return
	}
	s.part++
	name := fmt.Sprintf("%s_%s_part%03d.wav",
		s.sessionID,
		s.now().UTC().Format("20060102T150405"),
		s.part,
	)
	path := filepath.Join(s.dir, name)
	data := encodePCM16MonoWAV(pcm, audio.SupportedSampleRate)
	writeFn := s.writeFn
	if writeFn == nil {
		writeFn = writeDebugInboundWAVFile
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		slog.Warn("debug inbound wav mkdir failed", "dir", s.dir, "error", err)
		return
	}
	if err := writeFn(path, data); err != nil {
		slog.Warn("debug inbound wav write failed", "path", path, "error", err)
		return
	}
	slog.Info("debug inbound wav written",
		"path", path,
		"pcm_bytes", len(pcm),
		"duration_ms", pcmDurationMillis(pcm, audio.SupportedSampleRate),
	)
}

func encodePCM16MonoWAV(pcm []byte, sampleRate int) []byte {
	const headerSize = 44
	dataSize := len(pcm)
	byteRate := sampleRate * debugInboundWAVChannels * (debugInboundWAVBitsPerSample / 8)
	blockAlign := debugInboundWAVChannels * (debugInboundWAVBitsPerSample / 8)

	out := make([]byte, headerSize+dataSize)
	copy(out[0:4], "RIFF")
	binary.LittleEndian.PutUint32(out[4:8], uint32(36+dataSize))
	copy(out[8:12], "WAVE")
	copy(out[12:16], "fmt ")
	binary.LittleEndian.PutUint32(out[16:20], 16) // PCM fmt chunk size
	binary.LittleEndian.PutUint16(out[20:22], 1)  // PCM
	binary.LittleEndian.PutUint16(out[22:24], debugInboundWAVChannels)
	binary.LittleEndian.PutUint32(out[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(out[28:32], uint32(byteRate))
	binary.LittleEndian.PutUint16(out[32:34], uint16(blockAlign))
	binary.LittleEndian.PutUint16(out[34:36], debugInboundWAVBitsPerSample)
	copy(out[36:40], "data")
	binary.LittleEndian.PutUint32(out[40:44], uint32(dataSize))
	copy(out[44:], pcm)
	return out
}

func pcmDurationMillis(pcm []byte, sampleRate int) int {
	if sampleRate <= 0 {
		return 0
	}
	samples := len(pcm) / 2
	return samples * 1000 / sampleRate
}

func writeDebugInboundWAVFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0o644)
}

func sanitizeDebugInboundWAVSessionID(sessionID string) string {
	var b strings.Builder
	b.Grow(len(sessionID))
	for _, r := range sessionID {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r), r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := b.String()
	if out == "" {
		return "session"
	}
	return out
}
