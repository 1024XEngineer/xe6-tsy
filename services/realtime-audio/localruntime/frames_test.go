package localruntime

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/audio"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/session"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/webrtc"
)

type stubLanguages struct {
	pairs []session.LanguagePair
	err   error
}

func (s stubLanguages) GetCurrentConfig(_ context.Context, sessionID string) (session.LanguageConfigSnapshot, error) {
	if s.err != nil {
		return session.LanguageConfigSnapshot{}, s.err
	}
	return session.LanguageConfigSnapshot{
		SessionID:     sessionID,
		Version:       1,
		Status:        "active",
		LanguagePairs: s.pairs,
	}, nil
}

type stubMediaLookup struct{}

func (stubMediaLookup) CurrentMedia(context.Context, string) (webrtc.MediaTransport, error) {
	return nil, webrtc.ErrMediaUnavailable
}

func TestResolveASRSourceLanguage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		pairs []session.LanguagePair
		want  string
	}{
		{
			name: "bilingual is auto",
			pairs: []session.LanguagePair{
				{Source: "zh-CN", Target: "en-US"},
				{Source: "en-US", Target: "zh-CN"},
			},
			want: "",
		},
		{
			name:  "single pair forced",
			pairs: []session.LanguagePair{{Source: "en-US", Target: "zh-CN"}},
			want:  "en-US",
		},
		{
			name: "duplicate sources still forced",
			pairs: []session.LanguagePair{
				{Source: "zh-CN", Target: "en-US"},
				{Source: " zh-CN ", Target: "ja-JP"},
			},
			want: "zh-CN",
		},
		{
			name:  "empty sources ignored",
			pairs: []session.LanguagePair{{Source: "", Target: "en-US"}, {Source: "  ", Target: "zh-CN"}},
			want:  "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := resolveASRSourceLanguage(session.LanguageConfigSnapshot{LanguagePairs: tt.pairs})
			if got != tt.want {
				t.Fatalf("resolveASRSourceLanguage() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestWebRTCFrameSourcesOpen(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		sources  WebRTCFrameSources
		snapshot session.SessionSnapshot
		wantErr  error
		wantLang string
	}{
		{
			name:     "nil media",
			sources:  WebRTCFrameSources{},
			snapshot: session.SessionSnapshot{SessionID: "session-1"},
			wantErr:  webrtc.ErrMediaUnavailable,
		},
		{
			name:     "missing session id",
			sources:  WebRTCFrameSources{Media: stubMediaLookup{}},
			snapshot: session.SessionSnapshot{SessionID: "  "},
			wantErr:  session.ErrSessionIDRequired,
		},
		{
			name: "bilingual config clears forced source language",
			sources: WebRTCFrameSources{
				Media:          stubMediaLookup{},
				SourceLanguage: "zh-CN",
				Languages: stubLanguages{pairs: []session.LanguagePair{
					{Source: "zh-CN", Target: "en-US"},
					{Source: "en-US", Target: "zh-CN"},
				}},
			},
			snapshot: session.SessionSnapshot{SessionID: "session-1"},
			wantLang: "",
		},
		{
			name: "single-pair config forces source",
			sources: WebRTCFrameSources{
				Media:          stubMediaLookup{},
				SourceLanguage: "",
				Languages:      stubLanguages{pairs: []session.LanguagePair{{Source: "en-US", Target: "zh-CN"}}},
			},
			snapshot: session.SessionSnapshot{SessionID: "session-1"},
			wantLang: "en-US",
		},
		{
			name: "language reader error keeps configured source",
			sources: WebRTCFrameSources{
				Media:          stubMediaLookup{},
				SourceLanguage: "ja-JP",
				Languages:      stubLanguages{err: errors.New("config unavailable")},
			},
			snapshot: session.SessionSnapshot{SessionID: "session-1"},
			wantLang: "ja-JP",
		},
		{
			name: "no language reader keeps source language",
			sources: WebRTCFrameSources{
				Media:          stubMediaLookup{},
				SourceLanguage: "  en-US  ",
			},
			snapshot: session.SessionSnapshot{SessionID: "session-1"},
			wantLang: "en-US",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			input, err := tt.sources.Open(context.Background(), tt.snapshot)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("Open error = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			if input.SourceLanguage != tt.wantLang {
				t.Fatalf("SourceLanguage = %q, want %q", input.SourceLanguage, tt.wantLang)
			}
			if input.Source == nil {
				t.Fatal("Source = nil")
			}
		})
	}
}

func TestLazyWebRTCSourceReadAndClose(t *testing.T) {
	t.Parallel()

	t.Run("media resolve error", func(t *testing.T) {
		t.Parallel()
		sources := WebRTCFrameSources{Media: stubMediaLookup{}}
		input, err := sources.Open(context.Background(), session.SessionSnapshot{SessionID: "session-1"})
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		_, err = input.Source.ReadFrame(context.Background())
		if !errors.Is(err, webrtc.ErrMediaUnavailable) {
			t.Fatalf("ReadFrame error = %v", err)
		}
	})

	t.Run("nil audio source", func(t *testing.T) {
		t.Parallel()
		sources := WebRTCFrameSources{
			Media: mediaLookupFunc(func(context.Context, string) (webrtc.MediaTransport, error) {
				return &fakeMediaTransport{source: nil}, nil
			}),
		}
		input, err := sources.Open(context.Background(), session.SessionSnapshot{SessionID: "session-1"})
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		_, err = input.Source.ReadFrame(context.Background())
		if !errors.Is(err, webrtc.ErrMediaUnavailable) {
			t.Fatalf("ReadFrame error = %v, want ErrMediaUnavailable", err)
		}
	})

	t.Run("reads then eof after close", func(t *testing.T) {
		t.Parallel()
		frame, err := audio.NewFrame(make([]byte, 320), audio.SupportedSampleRate, time.Unix(1, 0).UTC())
		if err != nil {
			t.Fatal(err)
		}
		src := &stubFrameSource{frame: frame}
		sources := WebRTCFrameSources{
			Media: mediaLookupFunc(func(context.Context, string) (webrtc.MediaTransport, error) {
				return &fakeMediaTransport{source: src}, nil
			}),
		}
		input, err := sources.Open(context.Background(), session.SessionSnapshot{SessionID: "session-1"})
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		got, err := input.Source.ReadFrame(context.Background())
		if err != nil {
			t.Fatalf("ReadFrame: %v", err)
		}
		if !got.CapturedAt.Equal(frame.CapturedAt) {
			t.Fatalf("frame CapturedAt = %v, want %v", got.CapturedAt, frame.CapturedAt)
		}
		if err := input.Source.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		if src.closed != 1 {
			t.Fatalf("underlying Close calls = %d, want 1", src.closed)
		}
		_, err = input.Source.ReadFrame(context.Background())
		if !errors.Is(err, io.EOF) {
			t.Fatalf("ReadFrame after close = %v, want EOF", err)
		}
		if err := input.Source.Close(); err != nil {
			t.Fatalf("second Close: %v", err)
		}
	})

	t.Run("close before resolve succeeds", func(t *testing.T) {
		t.Parallel()
		sources := WebRTCFrameSources{Media: stubMediaLookup{}}
		input, err := sources.Open(context.Background(), session.SessionSnapshot{SessionID: "session-1"})
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		if err := input.Source.Close(); err != nil {
			t.Fatalf("Close before resolve: %v", err)
		}
		_, err = input.Source.ReadFrame(context.Background())
		if !errors.Is(err, io.EOF) {
			t.Fatalf("ReadFrame after early close = %v, want EOF", err)
		}
	})
}

type stubFrameSource struct {
	frame  audio.Frame
	closed int
	err    error
}

func (s *stubFrameSource) ReadFrame(context.Context) (audio.Frame, error) {
	if s.err != nil {
		return audio.Frame{}, s.err
	}
	return s.frame, nil
}

func (s *stubFrameSource) Close() error {
	s.closed++
	return nil
}
