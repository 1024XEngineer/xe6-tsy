package localruntime

import (
	"context"
	"errors"
	"testing"

	languagesv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/languages/v1"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/config"
)

func TestBuildSpeechRegistrySupportsMultipleProfiles(t *testing.T) {
	catalog := validSpeechCatalog()
	catalog.ASRProfiles = append(catalog.ASRProfiles, languagesv1.ASRProfile{
		ID: "asr-secondary", ProviderCode: "aliyun", Model: "qwen3-asr-flash-realtime",
		SupportedLanguages: []string{"en-US", "zh-CN"}, SupportsStreaming: true,
		InputEncoding: "pcm_s16le", InputSampleRateHz: 16000, InputChannels: 1, Enabled: true,
	})
	catalog.TTSProfiles = append(catalog.TTSProfiles, languagesv1.TTSProfile{
		ID: "tts-secondary", ProviderCode: "aliyun", Model: "qwen3-tts-flash",
		VoiceID: "voice-secondary", SupportedLanguages: []string{"en-US", "zh-CN"}, SupportsStreaming: true,
		OutputEncoding: "pcm_s16le", OutputSampleRateHz: 24000, OutputChannels: 1, Enabled: true,
	})
	catalog.Routes = append(catalog.Routes, languagesv1.SpeechRoute{
		ID: "route-secondary", LanguageA: "en-US", LanguageB: "ja-JP", ASRProfileID: "asr-secondary", TTSProfileID: "tts-secondary", Enabled: true,
	})
	catalog.ASRProfiles[1].SupportedLanguages = []string{"en-US", "ja-JP"}
	catalog.TTSProfiles[1].SupportedLanguages = []string{"en-US", "ja-JP"}

	registry, resolver, err := BuildSpeechRegistry(catalog, config.ProviderConfig{
		ASR: config.ASRConfig{APIKey: "asr-key", BaseURL: "https://example.com/compatible-mode/v1"},
		TTS: config.TTSConfig{APIKey: "tts-key", BaseURL: "https://example.com/api/v1"},
	})
	if err != nil {
		t.Fatalf("BuildSpeechRegistry() error = %v", err)
	}
	if _, err := registry.ASR("asr-secondary"); err != nil {
		t.Fatalf("secondary ASR lookup: %v", err)
	}
	if _, err := registry.TTS("tts-secondary"); err != nil {
		t.Fatalf("secondary TTS lookup: %v", err)
	}
	route, err := resolver.ResolveBinding(context.Background(), "ja-JP", "en-US")
	if err != nil {
		t.Fatalf("secondary route lookup: %v", err)
	}
	if route.ASRProfileID != "asr-secondary" || route.TTSProfileID != "tts-secondary" {
		t.Fatalf("secondary route = %#v", route)
	}
}

func TestValidateSpeechCatalogRejectsBrokenActiveReferences(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*SpeechCatalog)
		want   error
	}{
		{
			name: "missing ASR profile",
			mutate: func(catalog *SpeechCatalog) {
				catalog.Routes[0].ASRProfileID = "missing"
			},
			want: ErrSpeechCatalogProfileAbsent,
		},
		{
			name: "retired TTS profile",
			mutate: func(catalog *SpeechCatalog) {
				catalog.TTSProfiles[0].Enabled = false
			},
			want: ErrSpeechCatalogInvalid,
		},
		{
			name: "unknown provider",
			mutate: func(catalog *SpeechCatalog) {
				catalog.ASRProfiles[0].ProviderCode = "legacy"
			},
			want: config.ErrUnsupportedProvider,
		},
		{
			name: "legacy profile cannot construct",
			mutate: func(catalog *SpeechCatalog) {
				catalog.ASRProfiles[0].ProviderCode = "legacy"
				catalog.TTSProfiles[0].ProviderCode = "legacy"
			},
			want: config.ErrUnsupportedProvider,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			catalog := validSpeechCatalog()
			test.mutate(&catalog)
			if test.name == "unknown provider" || test.name == "legacy profile cannot construct" {
				_, _, err := BuildSpeechRegistry(catalog, config.ProviderConfig{
					ASR: config.ASRConfig{APIKey: "asr-key", BaseURL: "https://example.com"},
					TTS: config.TTSConfig{APIKey: "tts-key", BaseURL: "https://example.com"},
				})
				if !errors.Is(err, test.want) {
					t.Fatalf("BuildSpeechRegistry() error = %v, want %v", err, test.want)
				}
				return
			}
			err := ValidateSpeechCatalog(catalog)
			if !errors.Is(err, test.want) {
				t.Fatalf("ValidateSpeechCatalog() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestValidateSpeechCatalogRequiresExactMediaContract(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*SpeechCatalog)
	}{
		{name: "ASR sample rate", mutate: func(c *SpeechCatalog) { c.ASRProfiles[0].InputSampleRateHz = 8000 }},
		{name: "ASR encoding", mutate: func(c *SpeechCatalog) { c.ASRProfiles[0].InputEncoding = "wav" }},
		{name: "TTS sample rate", mutate: func(c *SpeechCatalog) { c.TTSProfiles[0].OutputSampleRateHz = 16000 }},
		{name: "TTS mono", mutate: func(c *SpeechCatalog) { c.TTSProfiles[0].OutputChannels = 2 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			catalog := validSpeechCatalog()
			test.mutate(&catalog)
			if err := ValidateSpeechCatalog(catalog); !errors.Is(err, ErrSpeechCatalogInvalid) {
				t.Fatalf("ValidateSpeechCatalog() error = %v, want invalid catalog", err)
			}
		})
	}
}

func validSpeechCatalog() SpeechCatalog {
	return SpeechCatalog{
		ASRProfiles: []languagesv1.ASRProfile{{
			ID: "asr-primary", ProviderCode: "aliyun", Model: "qwen3-asr-flash-realtime",
			SupportedLanguages: []string{"en-US", "zh-CN"}, SupportsStreaming: true,
			InputEncoding: "pcm_s16le", InputSampleRateHz: 16000, InputChannels: 1, Enabled: true,
		}},
		TTSProfiles: []languagesv1.TTSProfile{{
			ID: "tts-primary", ProviderCode: "aliyun", Model: "qwen3-tts-flash",
			VoiceID: "voice-primary", SupportedLanguages: []string{"en-US", "zh-CN"}, SupportsStreaming: true,
			OutputEncoding: "pcm_s16le", OutputSampleRateHz: 24000, OutputChannels: 1, Enabled: true,
		}},
		Routes: []languagesv1.SpeechRoute{{
			ID: "route-primary", LanguageA: "en-US", LanguageB: "zh-CN",
			ASRProfileID: "asr-primary", TTSProfileID: "tts-primary", Enabled: true,
		}},
	}
}
