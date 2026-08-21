package localruntime

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/audio"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/runtime"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/segment"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/session"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/webrtc"
)

// WebRTCFrameSources opens a lazy inbound source that resolves the transport
// on the first ReadFrame. Lifecycle Start runs before Activate, and Activate
// runs after the control plane has already required WebRTC connected — but
// unit tests may call Start without an offer, so Open must not fail early.
type WebRTCFrameSources struct {
	Media          MediaLookup
	SourceLanguage string
	// Languages optionally supplies the active pair source language per session.
	Languages session.LanguageConfigReader
}

func (f WebRTCFrameSources) Open(
	ctx context.Context,
	snapshot session.SessionSnapshot,
) (runtime.AudioInput, error) {
	if f.Media == nil {
		return runtime.AudioInput{}, webrtc.ErrMediaUnavailable
	}
	sessionID := strings.TrimSpace(snapshot.SessionID)
	if sessionID == "" {
		return runtime.AudioInput{}, session.ErrSessionIDRequired
	}
	language := strings.TrimSpace(f.SourceLanguage)
	if f.Languages != nil {
		if cfg, err := f.Languages.GetCurrentConfig(ctx, sessionID); err == nil {
			language = resolveASRSourceLanguage(cfg)
		}
	}
	// Empty SourceLanguage means "auto-detect" for bilingual sessions.
	// Do not force zh-CN here — that locks Qwen ASR to Chinese and drops English.
	// TEMPORARY: wrapDebugInboundWAV dumps inbound PCM to WAV (hardcoded in debug_inbound_wav.go).
	// Delete debug_inbound_wav.go and this wrap when the debug dump is retired.
	return runtime.AudioInput{
		Source: wrapDebugInboundWAV(&lazyWebRTCSource{
			media:     f.Media,
			sessionID: sessionID,
		}, sessionID),
		SourceLanguage: language,
		WakeWords:      &lazyWakeWordSource{media: f.Media, sessionID: sessionID},
	}, nil
}

// resolveASRSourceLanguage returns a forced ASR language only when the config
// has a single unique source. Bilingual pairs leave the language empty so the
// provider can auto-detect zh/en turn by turn.
func resolveASRSourceLanguage(cfg session.LanguageConfigSnapshot) string {
	unique := make([]string, 0, 2)
	seen := map[string]struct{}{}
	for _, pair := range cfg.LanguagePairs {
		source := strings.TrimSpace(pair.Source)
		if source == "" {
			continue
		}
		if _, ok := seen[source]; ok {
			continue
		}
		seen[source] = struct{}{}
		unique = append(unique, source)
	}
	if len(unique) == 1 {
		return unique[0]
	}
	return ""
}

type lazyWebRTCSource struct {
	media     MediaLookup
	sessionID string

	once   sync.Once
	source segment.FrameSource
	err    error
	closed bool
	mu     sync.Mutex
}

// lazyWakeWordSource keeps Start compatible with callers that prepare a
// runtime before WebRTC negotiation completes. The transport is resolved only
// when the segment loop starts listening for a local wake signal.
type lazyWakeWordSource struct {
	media     MediaLookup
	sessionID string

	once   sync.Once
	source segment.WakeWordSource
	err    error
}

func (s *lazyWakeWordSource) resolve(ctx context.Context) {
	s.once.Do(func() {
		media, err := s.media.CurrentMedia(ctx, s.sessionID)
		if err != nil {
			s.err = fmt.Errorf("resolve wake-word media transport: %w", err)
			return
		}
		transport, ok := media.(webrtc.WakeWordTransport)
		if !ok {
			s.err = webrtc.ErrMediaUnavailable
			return
		}
		s.source = transport.WakeWordSource()
		if s.source == nil {
			s.err = webrtc.ErrMediaUnavailable
		}
	})
}

func (s *lazyWakeWordSource) Receive(ctx context.Context) (realtimev1.WakeWordDetectedSignal, error) {
	if s == nil || s.media == nil {
		return realtimev1.WakeWordDetectedSignal{}, webrtc.ErrMediaUnavailable
	}
	if err := ctx.Err(); err != nil {
		return realtimev1.WakeWordDetectedSignal{}, err
	}
	s.resolve(ctx)
	if s.err != nil {
		return realtimev1.WakeWordDetectedSignal{}, s.err
	}
	return s.source.Receive(ctx)
}

func (s *lazyWebRTCSource) resolve(ctx context.Context) error {
	s.once.Do(func() {
		media, err := s.media.CurrentMedia(ctx, s.sessionID)
		if err != nil {
			s.err = fmt.Errorf("resolve media transport: %w", err)
			return
		}
		source := media.AudioSource()
		if source == nil {
			s.err = webrtc.ErrMediaUnavailable
			return
		}
		s.source = source
	})
	return s.err
}

func (s *lazyWebRTCSource) ReadFrame(ctx context.Context) (audio.Frame, error) {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return audio.Frame{}, io.EOF
	}
	if err := s.resolve(ctx); err != nil {
		return audio.Frame{}, err
	}
	return s.source.ReadFrame(ctx)
}

func (s *lazyWebRTCSource) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	if s.source != nil {
		return s.source.Close()
	}
	return nil
}
