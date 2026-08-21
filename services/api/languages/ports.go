package languages

import "context"

// LanguageConfigReader is the internal read port for the session-management
// and realtime-translation modules.
//
// Contract:
//   - Does not accept turnID and does not decide turn-scoped effectiveness.
//   - Returns the session's current active config only.
//   - Realtime translation copies the snapshot at turn start and must not
//     re-query mid-turn.
//   - Session management may call this before start to verify a valid
//     bilingual pair exists.
type LanguageConfigReader interface {
	GetCurrentConfig(ctx context.Context, sessionID string) (LanguageConfigSnapshot, error)
}

// LanguageTargetResolver is an optional helper for realtime translation.
// Prefer resolving against a local turn snapshot when one already exists;
// calling this mid-turn can observe a newer active config after a switch.
type LanguageTargetResolver interface {
	ResolveTarget(
		ctx context.Context,
		sessionID string,
		sourceLanguage string,
	) (targetLanguage string, version int, err error)
}

// DeliveryReadinessReader answers whether an account can route automatic
// translations to at least one enabled and verified destination.
type DeliveryReadinessReader interface {
	HasReadyAutomaticTarget(ctx context.Context, accountID string) (bool, error)
}
