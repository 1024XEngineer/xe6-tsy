package languages

import (
	"context"
	"errors"
	"fmt"
)

// Service is the language-configuration application service (issue #88).
type Service struct {
	store    Store
	sessions SessionOwnerReader
}

// NewService wires required store and session-ownership dependencies.
func NewService(store Store, sessions SessionOwnerReader) *Service {
	if sessions == nil {
		sessions = NotImplementedSessionOwner{}
	}
	return &Service{store: store, sessions: sessions}
}

var (
	_ LanguageConfigReader   = (*Service)(nil)
	_ LanguageTargetResolver = (*Service)(nil)
)

// ListSupportedLanguages returns the catalog, optionally filtered to active rows.
func (s *Service) ListSupportedLanguages(ctx context.Context, activeOnly bool) ([]SupportedLanguage, error) {
	return s.store.ListSupportedLanguages(ctx, activeOnly)
}

// GetActiveConfig returns the HTTP model for the session's active config.
func (s *Service) GetActiveConfig(ctx context.Context, accountID, sessionID string) (LanguageConfig, error) {
	if err := s.authorizeSession(ctx, accountID, sessionID); err != nil {
		return LanguageConfig{}, err
	}
	return s.store.GetActiveConfig(ctx, sessionID)
}

// ListConfigHistory pages version history for a session (version DESC).
func (s *Service) ListConfigHistory(ctx context.Context, accountID, sessionID, cursor string, limit int) ([]LanguageConfig, string, error) {
	if err := s.authorizeSession(ctx, accountID, sessionID); err != nil {
		return nil, "", err
	}
	return s.store.ListConfigs(ctx, ListConfigsQuery{
		SessionID: sessionID,
		Cursor:    cursor,
		Limit:     limit,
	})
}

// CreateConfig creates or switches the active bilingual config for a session.
//
// Idempotency (issue #88): same key + same language pairs + same session returns
// the original config; same key with a different payload returns conflict.
func (s *Service) CreateConfig(
	ctx context.Context,
	accountID, sessionID, idempotencyKey string,
	req CreateLanguageConfigRequest,
) (LanguageConfig, error) {
	if accountID == "" {
		return LanguageConfig{}, ErrUnauthenticated
	}
	if err := s.authorizeSession(ctx, accountID, sessionID); err != nil {
		return LanguageConfig{}, err
	}
	if len(req.Languages) == 0 {
		return LanguageConfig{}, fmt.Errorf("%w: languages is required", ErrInvalidRequest)
	}

	if idempotencyKey != "" {
		existing, err := s.store.GetConfigByIdempotencyKey(ctx, idempotencyKey)
		switch {
		case err == nil:
			if existing.SessionID != sessionID || !languagePairsEqual(existing.LanguagePairs, req.Languages) {
				return LanguageConfig{}, ErrIdempotencyConflict
			}
			return existing, nil
		case errors.Is(err, ErrNoActiveConfig):
			// first use of this key
		default:
			return LanguageConfig{}, err
		}
	}

	catalog, err := s.activeCatalog(ctx)
	if err != nil {
		return LanguageConfig{}, err
	}
	if err := validateP0LanguagePairs(req.Languages, catalog); err != nil {
		return LanguageConfig{}, err
	}

	return s.store.CreateActiveConfig(ctx, CreateConfigInput{
		SessionID:       sessionID,
		LanguagePairs:   req.Languages,
		CreatedBy:       accountID,
		IdempotencyKey:  idempotencyKey,
		ExpectedVersion: req.ExpectedVersion,
	})
}

// GetCurrentConfig implements LanguageConfigReader for session management and
// realtime translation. It does not enforce HTTP account ownership.
func (s *Service) GetCurrentConfig(ctx context.Context, sessionID string) (LanguageConfigSnapshot, error) {
	cfg, err := s.store.GetActiveConfig(ctx, sessionID)
	if err != nil {
		return LanguageConfigSnapshot{}, err
	}
	return toSnapshot(cfg), nil
}

// ResolveTarget implements LanguageTargetResolver using the current active config.
func (s *Service) ResolveTarget(ctx context.Context, sessionID, sourceLanguage string) (string, int, error) {
	cfg, err := s.store.GetActiveConfig(ctx, sessionID)
	if err != nil {
		return "", 0, err
	}
	for _, pair := range cfg.LanguagePairs {
		if pair.Source == sourceLanguage {
			return pair.Target, cfg.Version, nil
		}
	}
	return "", 0, ErrUnsupportedSourceLanguage
}

func (s *Service) authorizeSession(ctx context.Context, accountID, sessionID string) error {
	if accountID == "" {
		return ErrUnauthenticated
	}
	if sessionID == "" {
		return fmt.Errorf("%w: session_id is required", ErrInvalidRequest)
	}
	ownerID, err := s.sessions.GetOwnerAccountID(ctx, sessionID)
	if err != nil {
		return err
	}
	if ownerID != accountID {
		return ErrForbidden
	}
	return nil
}

func (s *Service) activeCatalog(ctx context.Context) (map[string]SupportedLanguage, error) {
	langs, err := s.store.ListSupportedLanguages(ctx, true)
	if err != nil {
		return nil, err
	}
	out := make(map[string]SupportedLanguage, len(langs))
	for _, lang := range langs {
		out[lang.LanguageCode] = lang
	}
	return out, nil
}
