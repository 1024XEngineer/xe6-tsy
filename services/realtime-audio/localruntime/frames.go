package localruntime

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"

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
}

func (f WebRTCFrameSources) Open(
	_ context.Context,
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
	if language == "" {
		language = "zh-CN"
	}
	return runtime.AudioInput{
		Source: &lazyWebRTCSource{
			media:     f.Media,
			sessionID: sessionID,
		},
		SourceLanguage: language,
	}, nil
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
