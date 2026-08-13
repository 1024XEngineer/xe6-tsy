package languages

import (
	"context"
	"errors"
	"testing"
)

func TestPostgresStoreSpeechRouteValidationWithoutPool(t *testing.T) {
	store := &PostgresStore{}
	pairs := []LanguagePair{
		{Source: "en-US", Target: "zh-CN"},
		{Source: "zh-CN", Target: "en-US"},
	}

	if _, err := store.ResolveSpeechRoute(context.Background(), "en-US", "zh-CN"); !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("ResolveSpeechRoute() error = %v, want not_implemented", err)
	}
	if err := store.ValidateSpeechRoute(context.Background(), pairs, nil); !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("ValidateSpeechRoute() error = %v, want not_implemented", err)
	}
	if err := store.ValidateSpeechRoute(context.Background(), pairs[:1], nil); !errors.Is(err, ErrSpeechRouteInvalid) {
		t.Fatalf("ValidateSpeechRoute() invalid pair error = %v, want speech_route_invalid", err)
	}
}

func TestValidateLoadedSpeechRoute(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*loadedSpeechRoute)
		want   error
	}{
		{name: "valid metadata"},
		{
			name: "profile ID mismatch",
			mutate: func(loaded *loadedSpeechRoute) {
				loaded.asr.ID = "asr-other"
			},
			want: ErrSpeechRouteInvalid,
		},
		{
			name: "ASR misses routed language",
			mutate: func(loaded *loadedSpeechRoute) {
				loaded.asr.SupportedLanguages = []string{"en-US"}
			},
			want: ErrSpeechRouteInvalid,
		},
		{
			name: "TTS has malformed supported language",
			mutate: func(loaded *loadedSpeechRoute) {
				loaded.tts.SupportedLanguages = []string{"en-US", "not-a-language@@"}
			},
			want: ErrSpeechRouteInvalid,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			route, asr, tts := validSpeechRouteData()
			loaded := loadedSpeechRoute{route: route, asr: asr, tts: tts}
			if tt.mutate != nil {
				tt.mutate(&loaded)
			}

			err := validateLoadedSpeechRoute(loaded)
			if tt.want == nil && err != nil {
				t.Fatalf("validateLoadedSpeechRoute() error = %v", err)
			}
			if tt.want != nil && !errors.Is(err, tt.want) {
				t.Fatalf("validateLoadedSpeechRoute() error = %v, want %v", err, tt.want)
			}
		})
	}
}
