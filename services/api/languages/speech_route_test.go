package languages

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestNormalizeSpeechPair(t *testing.T) {
	tests := []struct {
		name      string
		languageA string
		languageB string
		wantA     string
		wantB     string
		wantErr   bool
	}{
		{
			name:      "canonicalizes and orders reversed pair",
			languageA: " zh_cn ",
			languageB: "en_us",
			wantA:     "en-US",
			wantB:     "zh-CN",
		},
		{name: "empty language", languageA: "", languageB: "en-US", wantErr: true},
		{name: "same canonical language", languageA: "en-US", languageB: "en_us", wantErr: true},
		{name: "unparseable language", languageA: "en@@US", languageB: "zh-CN", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotA, gotB, err := normalizeSpeechPair(tt.languageA, tt.languageB)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("normalizeSpeechPair(%q, %q) succeeded", tt.languageA, tt.languageB)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizeSpeechPair(%q, %q): %v", tt.languageA, tt.languageB, err)
			}
			if gotA != tt.wantA || gotB != tt.wantB {
				t.Fatalf("normalizeSpeechPair(%q, %q) = (%q, %q), want (%q, %q)", tt.languageA, tt.languageB, gotA, gotB, tt.wantA, tt.wantB)
			}
		})
	}
}

func TestValidateSpeechRouteCapabilities(t *testing.T) {
	tests := []struct {
		name        string
		mutateRoute func(*SpeechRoute)
		mutateASR   func(*ASRProfile)
		mutateTTS   func(*TTSProfile)
		ttsTargets  []string
		wantErr     bool
	}{
		{name: "ASR supports both languages", ttsTargets: []string{"en-US", "zh-CN"}},
		{
			name: "ASR auto detect permits incomplete explicit coverage",
			mutateASR: func(profile *ASRProfile) {
				profile.SupportedLanguages = []string{"en-US"}
				profile.SupportsAutoDetect = true
			},
			ttsTargets: []string{"en-US", "zh-CN"},
		},
		{
			name: "ASR without coverage is rejected",
			mutateASR: func(profile *ASRProfile) {
				profile.SupportedLanguages = []string{"en-US"}
			},
			ttsTargets: []string{"en-US"},
			wantErr:    true,
		},
		{
			name: "inactive route is rejected",
			mutateRoute: func(route *SpeechRoute) {
				route.Enabled = false
			},
			ttsTargets: []string{"en-US"},
			wantErr:    true,
		},
		{
			name: "retired route is rejected",
			mutateRoute: func(route *SpeechRoute) {
				retiredAt := time.Date(2026, time.August, 12, 9, 0, 0, 0, time.UTC)
				route.Enabled = false
				route.RetiredAt = &retiredAt
			},
			ttsTargets: []string{"en-US"},
			wantErr:    true,
		},
		{
			name: "inactive ASR profile is rejected",
			mutateASR: func(profile *ASRProfile) {
				profile.Enabled = false
			},
			ttsTargets: []string{"en-US"},
			wantErr:    true,
		},
		{
			name: "retired TTS profile is rejected",
			mutateTTS: func(profile *TTSProfile) {
				retiredAt := time.Date(2026, time.August, 12, 9, 0, 0, 0, time.UTC)
				profile.Enabled = false
				profile.RetiredAt = &retiredAt
			},
			ttsTargets: []string{"en-US"},
			wantErr:    true,
		},
		{
			name: "ASR profile without input media is rejected",
			mutateASR: func(profile *ASRProfile) {
				profile.InputSampleRateHz = 0
			},
			ttsTargets: []string{"en-US"},
			wantErr:    true,
		},
		{
			name: "TTS profile without output media is rejected",
			mutateTTS: func(profile *TTSProfile) {
				profile.OutputEncoding = ""
			},
			ttsTargets: []string{"en-US"},
			wantErr:    true,
		},
		{
			name: "TTS only needs enabled target language",
			mutateTTS: func(profile *TTSProfile) {
				profile.SupportedLanguages = []string{"en-US"}
			},
			ttsTargets: []string{"en-US"},
		},
		{
			name: "bidirectional TTS needs both target languages",
			mutateTTS: func(profile *TTSProfile) {
				profile.SupportedLanguages = []string{"en-US"}
			},
			ttsTargets: []string{"en-US", "zh-CN"},
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			route, asrProfile, ttsProfile := validSpeechRouteData()
			if tt.mutateRoute != nil {
				tt.mutateRoute(&route)
			}
			if tt.mutateASR != nil {
				tt.mutateASR(&asrProfile)
			}
			if tt.mutateTTS != nil {
				tt.mutateTTS(&ttsProfile)
			}
			targets := make(map[string]struct{}, len(tt.ttsTargets))
			for _, target := range tt.ttsTargets {
				targets[target] = struct{}{}
			}
			err := validateSpeechRouteCapabilities(route, asrProfile, ttsProfile, targets)
			if tt.wantErr && !errors.Is(err, ErrSpeechRouteInvalid) {
				t.Fatalf("validateSpeechRouteCapabilities() error = %v, want speech_route_invalid", err)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("validateSpeechRouteCapabilities() error = %v", err)
			}
		})
	}
}

func TestValidateSpeechRouteCapabilitiesRejectsNonCanonicalRoutePair(t *testing.T) {
	route, asrProfile, ttsProfile := validSpeechRouteData()
	route.LanguageA = "zh-CN"
	route.LanguageB = "en-US"

	err := validateSpeechRouteCapabilities(route, asrProfile, ttsProfile, map[string]struct{}{"en-US": {}})
	if !errors.Is(err, ErrSpeechRouteInvalid) {
		t.Fatalf("validateSpeechRouteCapabilities() error = %v, want speech_route_invalid", err)
	}
}

func TestServiceStrictSpeechRouteValidationIsOptIn(t *testing.T) {
	ctx := context.Background()
	pairs := bilingualPairs()

	t.Run("non-strict service keeps legacy behavior", func(t *testing.T) {
		svc := NewService(NewMemoryStore(nil, nil), MapSessionOwner{"vs_1": "acct_1"})
		if _, err := svc.CreateConfig(ctx, "acct_1", "vs_1", "", CreateLanguageConfigRequest{Languages: pairs}); err != nil {
			t.Fatalf("CreateConfig() error = %v", err)
		}
	})

	t.Run("strict service rejects missing route", func(t *testing.T) {
		validator := &speechRouteValidatorStub{err: ErrSpeechRouteNotFound}
		svc := NewServiceWithSpeechRouteValidator(
			NewMemoryStore(nil, nil),
			MapSessionOwner{"vs_1": "acct_1"},
			validator,
		)
		_, err := svc.CreateConfig(ctx, "acct_1", "vs_1", "", CreateLanguageConfigRequest{Languages: pairs})
		if !errors.Is(err, ErrInvalidLanguagePair) || !errors.Is(err, ErrSpeechRouteNotFound) {
			t.Fatalf("CreateConfig() error = %v, want invalid_language_pair wrapping speech_route_not_found", err)
		}
		if validator.calls != 1 {
			t.Fatalf("validator calls = %d, want 1", validator.calls)
		}
	})

	t.Run("strict service accepts a complete route", func(t *testing.T) {
		route, asrProfile, ttsProfile := validSpeechRouteData()
		validator := &speechRouteValidatorStub{route: route, asr: asrProfile, tts: ttsProfile}
		svc := NewServiceWithSpeechRouteValidator(
			NewMemoryStore(nil, nil),
			MapSessionOwner{"vs_1": "acct_1"},
			validator,
		)
		if _, err := svc.CreateConfig(ctx, "acct_1", "vs_1", "", CreateLanguageConfigRequest{Languages: pairs}); err != nil {
			t.Fatalf("CreateConfig() error = %v", err)
		}
		if validator.calls != 1 {
			t.Fatalf("validator calls = %d, want 1", validator.calls)
		}
		resolved, err := svc.ResolveSpeechRoute(ctx, "zh_CN", "en_US")
		if err != nil {
			t.Fatalf("ResolveSpeechRoute() error = %v", err)
		}
		if resolved != route {
			t.Fatalf("ResolveSpeechRoute() = %#v, want %#v", resolved, route)
		}
	})
}

func validSpeechRouteData() (SpeechRoute, ASRProfile, TTSProfile) {
	return SpeechRoute{
			ID:           "route-primary",
			LanguageA:    "en-US",
			LanguageB:    "zh-CN",
			ASRProfileID: "asr-primary",
			TTSProfileID: "tts-primary",
			Enabled:      true,
		}, ASRProfile{
			ID:                 "asr-primary",
			ProviderCode:       "provider-a",
			Model:              "asr-v1",
			SupportedLanguages: []string{"en-US", "zh-CN"},
			SupportsStreaming:  true,
			InputEncoding:      "pcm_s16le",
			InputSampleRateHz:  16000,
			InputChannels:      1,
			Enabled:            true,
		}, TTSProfile{
			ID:                 "tts-primary",
			ProviderCode:       "provider-b",
			Model:              "tts-v1",
			VoiceID:            "voice-a",
			SupportedLanguages: []string{"en-US", "zh-CN"},
			SupportsStreaming:  true,
			OutputEncoding:     "pcm_s16le",
			OutputSampleRateHz: 24000,
			OutputChannels:     1,
			Enabled:            true,
		}
}

type speechRouteValidatorStub struct {
	route SpeechRoute
	asr   ASRProfile
	tts   TTSProfile
	err   error
	calls int
}

func (s *speechRouteValidatorStub) ValidateSpeechRoute(
	_ context.Context,
	pairs []LanguagePair,
	routes []OutputRoute,
) error {
	s.calls++
	if s.err != nil {
		return s.err
	}
	languageA, languageB, err := speechPairFromDirections(pairs)
	if err != nil {
		return err
	}
	routeA, routeB, err := normalizeSpeechPair(s.route.LanguageA, s.route.LanguageB)
	if err != nil || languageA != routeA || languageB != routeB {
		return fmt.Errorf("%w: %s and %s", ErrSpeechRouteNotFound, languageA, languageB)
	}
	ttsTargets := make(map[string]struct{}, len(routes))
	for _, route := range routes {
		if route.TTSEnabled {
			target, err := normalizeSpeechLanguage(route.TargetLanguage)
			if err != nil {
				return err
			}
			ttsTargets[target] = struct{}{}
		}
	}
	return validateSpeechRouteCapabilities(s.route, s.asr, s.tts, ttsTargets)
}

func (s *speechRouteValidatorStub) ResolveSpeechRoute(
	_ context.Context,
	languageA string,
	languageB string,
) (SpeechRoute, error) {
	if s.err != nil {
		return SpeechRoute{}, s.err
	}
	wantA, wantB, err := normalizeSpeechPair(s.route.LanguageA, s.route.LanguageB)
	if err != nil {
		return SpeechRoute{}, err
	}
	gotA, gotB, err := normalizeSpeechPair(languageA, languageB)
	if err != nil || gotA != wantA || gotB != wantB {
		return SpeechRoute{}, ErrSpeechRouteNotFound
	}
	return s.route, nil
}

var _ SpeechRouteValidator = (*speechRouteValidatorStub)(nil)
var _ SpeechRouteReader = (*speechRouteValidatorStub)(nil)
