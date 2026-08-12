package languages

import (
	"context"
	"errors"
	"fmt"
)

// Service is the language-configuration application service (issue #88).
type Service struct {
	store                Store
	sessions             SessionOwnerReader
	deliveryReadiness    DeliveryReadinessReader
	speechRouteValidator SpeechRouteValidator
	speechRouteReader    SpeechRouteReader
}

// NewService wires required store and session-ownership dependencies. The
// optional readiness reader is required before a single-output config can be created.
func NewService(store Store, sessions SessionOwnerReader, readiness ...DeliveryReadinessReader) *Service {
	return newService(store, sessions, nil, readiness...)
}

// NewServiceWithSpeechRouteValidator enables strict control-plane route
// validation while preserving NewService compatibility for existing callers.
func NewServiceWithSpeechRouteValidator(
	store Store,
	sessions SessionOwnerReader,
	validator SpeechRouteValidator,
	readiness ...DeliveryReadinessReader,
) *Service {
	return newService(store, sessions, validator, readiness...)
}

func newService(
	store Store,
	sessions SessionOwnerReader,
	validator SpeechRouteValidator,
	readiness ...DeliveryReadinessReader,
) *Service {
	if sessions == nil {
		sessions = NotImplementedSessionOwner{}
	}
	var deliveryReadiness DeliveryReadinessReader
	if len(readiness) > 0 {
		deliveryReadiness = readiness[0]
	}
	service := &Service{
		store:                store,
		sessions:             sessions,
		deliveryReadiness:    deliveryReadiness,
		speechRouteValidator: validator,
	}
	if reader, ok := store.(SpeechRouteReader); ok {
		service.speechRouteReader = reader
	}
	if reader, ok := validator.(SpeechRouteReader); ok {
		service.speechRouteReader = reader
	}
	return service
}

var (
	_ LanguageConfigReader   = (*Service)(nil)
	_ LanguageTargetResolver = (*Service)(nil)
	_ SpeechRouteReader      = (*Service)(nil)
)

// ListSupportedLanguages returns the catalog, optionally filtered to active rows.
func (s *Service) ListSupportedLanguages(ctx context.Context, activeOnly bool) ([]SupportedLanguage, error) {
	return s.store.ListSupportedLanguages(ctx, activeOnly)
}

// AutomaticDeliveryReady returns the same account-scoped readiness used when
// validating a single-output language configuration.
func (s *Service) AutomaticDeliveryReady(ctx context.Context, accountID string) (bool, error) {
	if accountID == "" {
		return false, ErrUnauthenticated
	}
	if s.deliveryReadiness == nil {
		return false, nil
	}
	return s.deliveryReadiness.HasReadyAutomaticTarget(ctx, accountID)
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
// Idempotency (issue #88): same key + same full request body + same session
// returns the original config; same key with a different body returns conflict.
func (s *Service) CreateConfig(
	ctx context.Context,
	accountID, sessionID, idempotencyKey string,
	req CreateLanguageConfigRequest,
) (LanguageConfig, error) {
	if accountID == "" {
		return LanguageConfig{}, ErrUnauthenticated
	}
	if err := validateIdempotencyKey(idempotencyKey); err != nil {
		return LanguageConfig{}, err
	}
	if err := s.authorizeSession(ctx, accountID, sessionID); err != nil {
		return LanguageConfig{}, err
	}
	if len(req.Languages) == 0 {
		return LanguageConfig{}, fmt.Errorf("%w: languages is required", ErrInvalidRequest)
	}

	catalog, err := s.activeCatalog(ctx)
	if err != nil {
		return LanguageConfig{}, err
	}
	if err := validateP0LanguagePairs(req.Languages, catalog); err != nil {
		return LanguageConfig{}, err
	}
	routes, err := normalizeOutputRoutes(req.Languages, req.OutputRoutes)
	if err != nil {
		return LanguageConfig{}, err
	}

	fingerprint := requestFingerprint(req)
	if idempotencyKey != "" {
		existing, err := s.store.GetConfigByIdempotencyKey(ctx, idempotencyKey)
		switch {
		case err == nil:
			if !sameIdempotentRequest(existing, sessionID, fingerprint) {
				return LanguageConfig{}, ErrIdempotencyConflict
			}
			return existing, nil
		case errors.Is(err, ErrNoActiveConfig):
			// first use of this key
		default:
			return LanguageConfig{}, err
		}
	}
	if s.speechRouteValidator != nil {
		if err := s.speechRouteValidator.ValidateSpeechRoute(ctx, req.Languages, routes); err != nil {
			return LanguageConfig{}, fmt.Errorf("%w: speech route validation: %w", ErrInvalidLanguagePair, err)
		}
	}
	if hasDeliveryRoute(routes) {
		ready, err := s.AutomaticDeliveryReady(ctx, accountID)
		if err != nil {
			return LanguageConfig{}, err
		}
		if !ready {
			return LanguageConfig{}, ErrDeliveryTargetRequired
		}
	}

	created, err := s.store.CreateActiveConfig(ctx, CreateConfigInput{
		SessionID:          sessionID,
		LanguagePairs:      req.Languages,
		OutputRoutes:       routes,
		CreatedBy:          accountID,
		IdempotencyKey:     idempotencyKey,
		ExpectedVersion:    req.ExpectedVersion,
		RequestFingerprint: fingerprint,
	})
	if err == nil {
		return created, nil
	}
	if idempotencyKey == "" || !errors.Is(err, ErrIdempotencyConflict) {
		return LanguageConfig{}, err
	}

	// Concurrent identical retries: both missed the pre-insert lookup; the loser
	// hit the idempotency unique index. Re-read and return when the body matches.
	existing, lookupErr := s.store.GetConfigByIdempotencyKey(ctx, idempotencyKey)
	if lookupErr != nil {
		return LanguageConfig{}, err
	}
	if !sameIdempotentRequest(existing, sessionID, fingerprint) {
		return LanguageConfig{}, ErrIdempotencyConflict
	}
	return existing, nil
}

// ResolveSpeechRoute resolves non-secret route metadata for internal consumers.
// The HTTP language configuration surface intentionally does not expose it.
func (s *Service) ResolveSpeechRoute(
	ctx context.Context,
	languageA string,
	languageB string,
) (SpeechRoute, error) {
	if s.speechRouteReader == nil {
		return SpeechRoute{}, ErrNotImplemented
	}
	return s.speechRouteReader.ResolveSpeechRoute(ctx, languageA, languageB)
}

func hasDeliveryRoute(routes []OutputRoute) bool {
	for _, route := range routes {
		if route.DeliveryEnabled {
			return true
		}
	}
	return false
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
