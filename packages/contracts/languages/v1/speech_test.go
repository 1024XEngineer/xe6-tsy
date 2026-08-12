package languagesv1

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestSpeechRoutingContractsMarshalTypedMetadata(t *testing.T) {
	retiredAt := time.Date(2026, time.August, 12, 8, 0, 0, 0, time.UTC)
	tests := []struct {
		name         string
		profile      any
		requiredKeys []string
	}{
		{
			name: "ASR profile",
			profile: ASRProfile{
				ID:                 "asr-primary",
				ProviderCode:       "provider-a",
				Model:              "asr-v1",
				SupportedLanguages: []string{"zh-CN", "en-US"},
				SupportsAutoDetect: true,
				SupportsStreaming:  true,
				InputEncoding:      "pcm_s16le",
				InputSampleRateHz:  16000,
				InputChannels:      1,
				Enabled:            true,
			},
			requiredKeys: []string{
				"\"id\"",
				"\"provider_code\"",
				"\"model\"",
				"\"supported_languages\"",
				"\"input_encoding\"",
				"\"input_sample_rate_hz\"",
				"\"input_channels\"",
				"\"enabled\"",
			},
		},
		{
			name: "TTS profile",
			profile: TTSProfile{
				ID:                 "tts-primary",
				ProviderCode:       "provider-b",
				Model:              "tts-v1",
				VoiceID:            "voice-a",
				SupportedLanguages: []string{"zh-CN", "en-US"},
				SupportsStreaming:  true,
				OutputEncoding:     "pcm_s16le",
				OutputSampleRateHz: 24000,
				OutputChannels:     1,
				Enabled:            false,
				RetiredAt:          &retiredAt,
			},
			requiredKeys: []string{
				"\"id\"",
				"\"provider_code\"",
				"\"model\"",
				"\"voice_id\"",
				"\"supported_languages\"",
				"\"output_encoding\"",
				"\"output_sample_rate_hz\"",
				"\"output_channels\"",
				"\"enabled\"",
				"\"retired_at\"",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := json.Marshal(tt.profile)
			if err != nil {
				t.Fatalf("json.Marshal(%T): %v", tt.profile, err)
			}
			for _, key := range tt.requiredKeys {
				if !strings.Contains(string(raw), key) {
					t.Fatalf("%T JSON %s is missing %s", tt.profile, raw, key)
				}
			}
			for _, forbiddenKey := range []string{"\"priority\"", "\"is_active\""} {
				if strings.Contains(string(raw), forbiddenKey) {
					t.Fatalf("%T JSON %s unexpectedly contains %s", tt.profile, raw, forbiddenKey)
				}
			}
		})
	}
}

func TestSpeechRouteJSONFields(t *testing.T) {
	route := SpeechRoute{
		ID:           "route-primary",
		LanguageA:    "en-US",
		LanguageB:    "zh-CN",
		ASRProfileID: "asr-primary",
		TTSProfileID: "tts-primary",
		Enabled:      true,
	}
	raw, err := json.Marshal(route)
	if err != nil {
		t.Fatalf("json.Marshal(SpeechRoute): %v", err)
	}
	if got, want := string(raw), `{"id":"route-primary","language_a":"en-US","language_b":"zh-CN","asr_profile_id":"asr-primary","tts_profile_id":"tts-primary","enabled":true}`; got != want {
		t.Fatalf("SpeechRoute JSON = %s, want %s", got, want)
	}
}
