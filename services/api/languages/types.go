package languages

import "time"

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

// LanguageConfig is the HTTP/API representation of a versioned session config.
type LanguageConfig struct {
	ID             string         `json:"id"`
	SessionID      string         `json:"session_id"`
	Version        int            `json:"version"`
	LanguagePairs  []LanguagePair `json:"language_pairs"`
	Status         string         `json:"status"` // active | superseded | expired
	EffectiveFrom  time.Time      `json:"effective_from"`
	EffectiveUntil *time.Time     `json:"effective_until,omitempty"`
	CreatedBy      string         `json:"created_by"`
	CreatedAt      time.Time      `json:"created_at"`
}

// LanguageConfigSnapshot is the internal read model for session management
// and realtime translation.
// Realtime translation must copy this at turn start and keep it for the whole turn.
type LanguageConfigSnapshot struct {
	SessionID     string
	Version       int
	LanguagePairs []LanguagePair
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

// CreateLanguageConfigRequest is the body for POST .../language-configs.
type CreateLanguageConfigRequest struct {
	Languages       []LanguagePair `json:"languages"`
	ExpectedVersion *int           `json:"expected_version,omitempty"`
}

// ListLanguageConfigsResponse is returned by GET .../language-configs.
type ListLanguageConfigsResponse struct {
	Items      []LanguageConfig `json:"items"`
	NextCursor *string          `json:"next_cursor"`
}
