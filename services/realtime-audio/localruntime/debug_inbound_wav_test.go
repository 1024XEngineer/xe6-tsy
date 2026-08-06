package localruntime

import (
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/audio"
)

func TestWrapDebugInboundWAVNilPassthrough(t *testing.T) {
	t.Parallel()
	if wrapDebugInboundWAV(nil, "session-1") != nil {
		t.Fatal("nil source should stay nil")
	}
}

func TestDebugInboundWAVSourceFlushesChunksAndRemainder(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	fixedNow := time.Date(2026, 8, 3, 7, 0, 0, 0, time.UTC)
	inner := &stubFrameSource{}
	source, err := newDebugInboundWAVSource(inner, "sess/a b", dir, 1, func() time.Time { return fixedNow })
	if err != nil {
		t.Fatalf("newDebugInboundWAVSource: %v", err)
	}

	var wrote []string
	source.writeFn = func(path string, data []byte) error {
		wrote = append(wrote, path)
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return err
		}
		assertValidPCM16MonoWAV(t, data, audio.SupportedSampleRate)
		return nil
	}

	// 1 second of silence (16000 samples * 2 bytes).
	oneSecond := make([]byte, audio.SupportedSampleRate*2)
	remainder := make([]byte, 800) // 25 ms
	frames := [][]byte{oneSecond[:1000], oneSecond[1000:], remainder}
	for i, pcm := range frames {
		inner.frame = mustFrame(t, pcm, fixedNow.Add(time.Duration(i)*time.Millisecond))
		if _, err := source.ReadFrame(context.Background()); err != nil {
			t.Fatalf("ReadFrame[%d]: %v", i, err)
		}
	}
	if len(wrote) != 1 {
		t.Fatalf("chunk files = %d, want 1 before close", len(wrote))
	}
	if err := source.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if len(wrote) != 2 {
		t.Fatalf("files after close = %d, want 2", len(wrote))
	}
	if !strings.Contains(filepath.Base(wrote[0]), "sess_a_b_") {
		t.Fatalf("filename = %q, want sanitized session id", wrote[0])
	}
	if inner.closed == 0 {
		t.Fatal("inner Close was not called")
	}
}

func TestEncodePCM16MonoWAVHeader(t *testing.T) {
	t.Parallel()

	pcm := []byte{0x00, 0x01, 0x02, 0x03}
	raw := encodePCM16MonoWAV(pcm, audio.SupportedSampleRate)
	assertValidPCM16MonoWAV(t, raw, audio.SupportedSampleRate)
	if binary.LittleEndian.Uint32(raw[40:44]) != uint32(len(pcm)) {
		t.Fatalf("data chunk size = %d, want %d", binary.LittleEndian.Uint32(raw[40:44]), len(pcm))
	}
	if string(raw[44:]) != string(pcm) {
		t.Fatalf("pcm payload mismatch")
	}
}

func TestDebugInboundWAVWriteFailureDoesNotBreakRead(t *testing.T) {
	t.Parallel()

	inner := &stubFrameSource{frame: mustFrame(t, make([]byte, audio.SupportedSampleRate*2), time.Now())}
	source, err := newDebugInboundWAVSource(inner, "session-1", t.TempDir(), 1, time.Now)
	if err != nil {
		t.Fatalf("newDebugInboundWAVSource: %v", err)
	}
	source.writeFn = func(string, []byte) error {
		return errors.New("disk full")
	}
	if _, err := source.ReadFrame(context.Background()); err != nil {
		t.Fatalf("ReadFrame should ignore write errors, got %v", err)
	}
}

func mustFrame(t *testing.T, pcm []byte, capturedAt time.Time) audio.Frame {
	t.Helper()
	frame, err := audio.NewFrame(pcm, audio.SupportedSampleRate, capturedAt)
	if err != nil {
		t.Fatalf("NewFrame: %v", err)
	}
	return frame
}

func assertValidPCM16MonoWAV(t *testing.T, raw []byte, sampleRate int) {
	t.Helper()
	if len(raw) < 44 {
		t.Fatalf("wav too short: %d", len(raw))
	}
	if string(raw[0:4]) != "RIFF" || string(raw[8:12]) != "WAVE" {
		t.Fatalf("missing RIFF/WAVE header")
	}
	if string(raw[12:16]) != "fmt " || string(raw[36:40]) != "data" {
		t.Fatalf("missing fmt/data chunks")
	}
	if binary.LittleEndian.Uint16(raw[20:22]) != 1 {
		t.Fatalf("audio format = %d, want PCM", binary.LittleEndian.Uint16(raw[20:22]))
	}
	if binary.LittleEndian.Uint16(raw[22:24]) != 1 {
		t.Fatalf("channels = %d, want 1", binary.LittleEndian.Uint16(raw[22:24]))
	}
	if int(binary.LittleEndian.Uint32(raw[24:28])) != sampleRate {
		t.Fatalf("sample rate = %d, want %d", binary.LittleEndian.Uint32(raw[24:28]), sampleRate)
	}
	if binary.LittleEndian.Uint16(raw[34:36]) != 16 {
		t.Fatalf("bits = %d, want 16", binary.LittleEndian.Uint16(raw[34:36]))
	}
}
