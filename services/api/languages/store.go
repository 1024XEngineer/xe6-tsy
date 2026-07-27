package languages

import (
	"context"
	"time"
)

// CreateConfigInput is the persistence payload for inserting a new active config.
type CreateConfigInput struct {
	SessionID          string
	LanguagePairs      []LanguagePair
	CreatedBy          string
	IdempotencyKey     string // empty means no idempotency key
	ExpectedVersion    *int   // optional optimistic lock against current active version
	RequestFingerprint string // hash of the full create request body for idempotent replay
}

// ListConfigsQuery pages version history for one session (version DESC).
type ListConfigsQuery struct {
	SessionID string
	Cursor    string // opaque: previous page's last version as decimal string; empty = start
	Limit     int
}

// Store is the language-configuration persistence port.
type Store interface {
	ListSupportedLanguages(ctx context.Context, activeOnly bool) ([]SupportedLanguage, error)
	GetActiveConfig(ctx context.Context, sessionID string) (LanguageConfig, error)
	GetConfigByIdempotencyKey(ctx context.Context, idempotencyKey string) (LanguageConfig, error)
	CreateActiveConfig(ctx context.Context, input CreateConfigInput) (LanguageConfig, error)
	ListConfigs(ctx context.Context, query ListConfigsQuery) (items []LanguageConfig, nextCursor string, err error)
}

// Clock abstracts time for deterministic tests.
type Clock interface {
	Now() time.Time
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now().UTC() }
