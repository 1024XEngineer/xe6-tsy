package localruntime

import (
	"context"
	"testing"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/session"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/webrtc"
)

type stubLanguages struct {
	pairs []session.LanguagePair
}

func (s stubLanguages) GetCurrentConfig(_ context.Context, sessionID string) (session.LanguageConfigSnapshot, error) {
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

func TestResolveASRSourceLanguageBilingualIsAuto(t *testing.T) {
	got := resolveASRSourceLanguage(session.LanguageConfigSnapshot{
		LanguagePairs: []session.LanguagePair{
			{Source: "zh-CN", Target: "en-US"},
			{Source: "en-US", Target: "zh-CN"},
		},
	})
	if got != "" {
		t.Fatalf("bilingual ASR language = %q, want empty auto-detect", got)
	}
}

func TestResolveASRSourceLanguageSinglePairForced(t *testing.T) {
	got := resolveASRSourceLanguage(session.LanguageConfigSnapshot{
		LanguagePairs: []session.LanguagePair{
			{Source: "en-US", Target: "zh-CN"},
		},
	})
	if got != "en-US" {
		t.Fatalf("single-pair ASR language = %q, want en-US", got)
	}
}

func TestWebRTCFrameSourcesOpenLeavesBilingualAuto(t *testing.T) {
	sources := WebRTCFrameSources{
		Media:          stubMediaLookup{},
		SourceLanguage: "zh-CN",
		Languages: stubLanguages{pairs: []session.LanguagePair{
			{Source: "zh-CN", Target: "en-US"},
			{Source: "en-US", Target: "zh-CN"},
		}},
	}
	input, err := sources.Open(context.Background(), session.SessionSnapshot{SessionID: "session-1"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if input.SourceLanguage != "" {
		t.Fatalf("SourceLanguage = %q, want empty auto-detect", input.SourceLanguage)
	}
}
