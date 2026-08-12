package languagesv1

import (
	"context"
	"time"
)

// SpeechRoute is the durable control-plane selection for one unordered language
// pair. LanguageA and LanguageB are canonical BCP-47 values stored in
// lexicographic order.
type SpeechRoute struct {
	ID           string     `json:"id"`
	LanguageA    string     `json:"language_a"`
	LanguageB    string     `json:"language_b"`
	ASRProfileID string     `json:"asr_profile_id"`
	TTSProfileID string     `json:"tts_profile_id"`
	Enabled      bool       `json:"enabled"`
	RetiredAt    *time.Time `json:"retired_at,omitempty"`
}

// ASRProfile is non-secret control-plane metadata for one ASR configuration.
// Provider credentials and transport endpoints remain deployment configuration.
type ASRProfile struct {
	ID                 string     `json:"id"`
	ProviderCode       string     `json:"provider_code"`
	Model              string     `json:"model"`
	SupportedLanguages []string   `json:"supported_languages"`
	SupportsAutoDetect bool       `json:"supports_auto_detect"`
	SupportsStreaming  bool       `json:"supports_streaming"`
	InputEncoding      string     `json:"input_encoding"`
	InputSampleRateHz  int        `json:"input_sample_rate_hz"`
	InputChannels      int        `json:"input_channels"`
	Enabled            bool       `json:"enabled"`
	RetiredAt          *time.Time `json:"retired_at,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

// TTSProfile is non-secret control-plane metadata for one TTS configuration.
// Output media fields describe the adapter output expected by the media pipeline.
type TTSProfile struct {
	ID                 string     `json:"id"`
	ProviderCode       string     `json:"provider_code"`
	Model              string     `json:"model"`
	VoiceID            string     `json:"voice_id"`
	SupportedLanguages []string   `json:"supported_languages"`
	SupportsStreaming  bool       `json:"supports_streaming"`
	OutputEncoding     string     `json:"output_encoding"`
	OutputSampleRateHz int        `json:"output_sample_rate_hz"`
	OutputChannels     int        `json:"output_channels"`
	Enabled            bool       `json:"enabled"`
	RetiredAt          *time.Time `json:"retired_at,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

// SpeechRouteReader resolves the durable control-plane route for either order
// of a configured bilingual language pair.
type SpeechRouteReader interface {
	ResolveSpeechRoute(ctx context.Context, languageA, languageB string) (SpeechRoute, error)
}
