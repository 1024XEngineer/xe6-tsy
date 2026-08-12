package languages

import (
	"time"

	languagesv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/languages/v1"
)

type InterpretationOutputMode = languagesv1.InterpretationOutputMode

type SpeechRoute = languagesv1.SpeechRoute
type ASRProfile = languagesv1.ASRProfile
type TTSProfile = languagesv1.TTSProfile

const (
	InterpretationOutputModeBidirectional = languagesv1.InterpretationOutputModeBidirectional
	InterpretationOutputModeSingle        = languagesv1.InterpretationOutputModeSingle
)

// Config status values for LanguageConfig / LanguageConfigSnapshot.
const (
	StatusActive     = "active"
	StatusSuperseded = "superseded"
	StatusExpired    = "expired"
)

// LanguagePair is one explicit translation direction (BCP-47 codes).
// P0 bilingual sessions submit both directions as two pairs.
type LanguagePair struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

// OutputRoute controls independent media and delivery behavior for one
// translated target language.
type OutputRoute struct {
	TargetLanguage  string `json:"target_language"`
	TTSEnabled      bool   `json:"tts_enabled"`
	DeliveryEnabled bool   `json:"delivery_enabled"`
}

// LanguageConfig is the HTTP/API representation of a versioned session config.
type LanguageConfig struct {
	ID                 string                   `json:"id"`
	SessionID          string                   `json:"session_id"`
	Version            int                      `json:"version"`
	LanguagePairs      []LanguagePair           `json:"language_pairs"`
	OutputRoutes       []OutputRoute            `json:"output_routes"`
	OutputMode         InterpretationOutputMode `json:"output_mode"`
	Status             string                   `json:"status"` // active | superseded | expired
	EffectiveFrom      time.Time                `json:"effective_from"`
	EffectiveUntil     *time.Time               `json:"effective_until"`
	CreatedBy          string                   `json:"created_by"`
	CreatedAt          time.Time                `json:"created_at"`
	RequestFingerprint string                   `json:"-"` // internal idempotency fingerprint
}

// LanguageConfigSnapshot is the internal read model for session management
// and realtime translation.
// Realtime translation must copy this at turn start and keep it for the whole turn.
type LanguageConfigSnapshot struct {
	SessionID     string
	Version       int
	LanguagePairs []LanguagePair
	OutputRoutes  []OutputRoute
	Status        string
	EffectiveFrom time.Time
	UpdatedAt     time.Time
}

// SupportedLanguage is one entry in the system language catalog.
type SupportedLanguage struct {
	LanguageCode     string `json:"language_code"`
	DisplayName      string `json:"display_name"`
	DisplayNameEN    string `json:"display_name_en,omitempty"`
	SupportsAsSource bool   `json:"supports_as_source"`
	SupportsAsTarget bool   `json:"supports_as_target"`
}

// ListLanguagesResponse is returned by GET /api/v1/languages.
type ListLanguagesResponse struct {
	Languages []SupportedLanguage `json:"languages"`
}

// AutomaticDeliveryReadinessResponse reports whether single output can use
// the configured automatic delivery runtime for the authenticated account.
type AutomaticDeliveryReadinessResponse struct {
	Ready bool `json:"ready"`
}

// CreateLanguageConfigRequest is the body for POST .../language-configs.
type CreateLanguageConfigRequest struct {
	Languages       []LanguagePair `json:"languages"`
	OutputRoutes    []OutputRoute  `json:"output_routes,omitempty"`
	ExpectedVersion *int           `json:"expected_version,omitempty"`
}

// ListLanguageConfigsResponse is returned by GET .../language-configs.
type ListLanguageConfigsResponse struct {
	Items      []LanguageConfig `json:"items"`
	NextCursor *string          `json:"next_cursor"`
}
