package speech

import (
	"context"
	"errors"
	"testing"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/asr"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/tts"
)

func TestProviderRegistryResolvesAdaptersAndCopiesProfileMetadata(t *testing.T) {
	asrAdapter := asr.NewFakeProvider(asr.FakeProviderConfig{})
	ttsAdapter := tts.NewFakeProvider(tts.FakeProviderConfig{})
	capabilities := []string{"streaming"}
	registry := mustRegistry(t, asrAdapter, ttsAdapter, capabilities)
	capabilities[0] = "mutated"

	gotASR, err := registry.ASR("asr-primary")
	if err != nil {
		t.Fatalf("ASR() error = %v", err)
	}
	if gotASR != asrAdapter {
		t.Fatal("ASR() returned a different adapter")
	}
	gotTTS, err := registry.TTS("tts-primary")
	if err != nil {
		t.Fatalf("TTS() error = %v", err)
	}
	if gotTTS != ttsAdapter {
		t.Fatal("TTS() returned a different adapter")
	}
	profile, err := registry.ASRProfile("asr-primary")
	if err != nil {
		t.Fatalf("ASRProfile() error = %v", err)
	}
	if profile.Capabilities[0] != "streaming" {
		t.Fatalf("ASR profile capabilities = %v, want copied metadata", profile.Capabilities)
	}
	profile.Capabilities[0] = "caller-mutated"
	again, err := registry.ASRProfile("asr-primary")
	if err != nil {
		t.Fatalf("ASRProfile() second error = %v", err)
	}
	if again.Capabilities[0] != "streaming" {
		t.Fatalf("ASR profile metadata was mutated through returned value: %v", again.Capabilities)
	}
}

func TestProviderRegistryRejectsInvalidRegistrations(t *testing.T) {
	adapter := asr.NewFakeProvider(asr.FakeProviderConfig{})
	tests := []struct {
		name string
		asr  []ASRProfile
		tts  []TTSProfile
		want error
	}{
		{
			name: "missing profile id",
			asr:  []ASRProfile{{Profile: Profile{}, Adapter: adapter}},
			want: ErrProfileIDRequired,
		},
		{
			name: "missing ASR adapter",
			asr:  []ASRProfile{{Profile: Profile{ID: "asr"}}},
			want: ErrASRProviderRequired,
		},
		{
			name: "duplicate ASR profile",
			asr: []ASRProfile{
				{Profile: Profile{ID: "asr"}, Adapter: adapter},
				{Profile: Profile{ID: "asr"}, Adapter: adapter},
			},
			want: ErrDuplicateASRProfile,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewProviderRegistry(test.asr, test.tts)
			if !errors.Is(err, test.want) {
				t.Fatalf("NewProviderRegistry() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestStaticRouteResolverResolvesBothLanguageOrders(t *testing.T) {
	resolver, err := NewRouteResolver([]SpeechRoute{{
		LanguageA: "zh-CN", LanguageB: "en-US", ASRProfileID: "asr-primary", TTSProfileID: "tts-primary",
	}})
	if err != nil {
		t.Fatalf("NewRouteResolver() error = %v", err)
	}
	for _, languages := range [][2]string{{"zh-CN", "en-US"}, {"en_US", "zh_CN"}} {
		route, err := resolver.ResolveBinding(context.Background(), languages[0], languages[1])
		if err != nil {
			t.Fatalf("ResolveBinding(%q, %q) error = %v", languages[0], languages[1], err)
		}
		if route.ASRProfileID != "asr-primary" || route.TTSProfileID != "tts-primary" {
			t.Fatalf("ResolveBinding(%q, %q) = %+v", languages[0], languages[1], route)
		}
	}
}

func TestStaticRouteResolverCanonicalizesBCP47Tags(t *testing.T) {
	resolver, err := NewRouteResolver([]SpeechRoute{{
		LanguageA: "zh_hans_cn", LanguageB: "en_us", ASRProfileID: "asr-primary", TTSProfileID: "tts-primary",
	}})
	if err != nil {
		t.Fatalf("NewRouteResolver() error = %v", err)
	}

	route, err := resolver.ResolveBinding(context.Background(), "EN-us", "zh-Hans-CN")
	if err != nil {
		t.Fatalf("ResolveBinding() error = %v", err)
	}
	if route.LanguageA != "en-US" || route.LanguageB != "zh-Hans-CN" {
		t.Fatalf("ResolveBinding() route languages = %q, %q", route.LanguageA, route.LanguageB)
	}
}

func TestStaticRouteResolverRejectsMalformedBCP47Tag(t *testing.T) {
	_, err := NewRouteResolver([]SpeechRoute{{
		LanguageA: "en--US", LanguageB: "zh-CN", ASRProfileID: "asr-primary", TTSProfileID: "tts-primary",
	}})
	if !errors.Is(err, ErrLanguageInvalid) {
		t.Fatalf("NewRouteResolver() error = %v, want %v", err, ErrLanguageInvalid)
	}
}

func TestStaticRouteResolverRejectsDuplicatePair(t *testing.T) {
	_, err := NewRouteResolver([]SpeechRoute{
		{LanguageA: "zh-CN", LanguageB: "en-US", ASRProfileID: "asr-a", TTSProfileID: "tts-a"},
		{LanguageA: "en-US", LanguageB: "zh-CN", ASRProfileID: "asr-b", TTSProfileID: "tts-b"},
	})
	if !errors.Is(err, ErrDuplicateSpeechRoute) {
		t.Fatalf("NewRouteResolver() error = %v, want %v", err, ErrDuplicateSpeechRoute)
	}
}
