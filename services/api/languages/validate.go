package languages

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// MaxIdempotencyKeyLen matches voice_session_language_configs.idempotency_key VARCHAR(128).
const MaxIdempotencyKeyLen = 128

func validateIdempotencyKey(key string) error {
	if key == "" {
		return nil
	}
	if len(key) > MaxIdempotencyKeyLen {
		return fmt.Errorf("%w: Idempotency-Key must be at most %d characters", ErrInvalidRequest, MaxIdempotencyKeyLen)
	}
	return nil
}

// validateP0LanguagePairs enforces issue #88 P0 bilingual rules:
// exactly two opposite directions covering two supported active languages.
func validateP0LanguagePairs(pairs []LanguagePair, catalog map[string]SupportedLanguage) error {
	if len(pairs) != 2 {
		return fmt.Errorf("%w: P0 requires exactly two language directions", ErrInvalidLanguagePair)
	}

	seen := make(map[string]string, 2)
	languages := make(map[string]struct{}, 2)
	for _, pair := range pairs {
		if pair.Source == "" || pair.Target == "" {
			return fmt.Errorf("%w: source and target are required", ErrInvalidRequest)
		}
		if pair.Source == pair.Target {
			return fmt.Errorf("%w: source and target must differ", ErrInvalidLanguagePair)
		}
		if _, dup := seen[pair.Source]; dup {
			return fmt.Errorf("%w: duplicate source %s", ErrInvalidLanguagePair, pair.Source)
		}

		src, ok := catalog[pair.Source]
		if !ok || !src.SupportsAsSource {
			return fmt.Errorf("%w: %s", ErrUnsupportedLanguage, pair.Source)
		}
		tgt, ok := catalog[pair.Target]
		if !ok || !tgt.SupportsAsTarget {
			return fmt.Errorf("%w: %s", ErrUnsupportedLanguage, pair.Target)
		}

		seen[pair.Source] = pair.Target
		languages[pair.Source] = struct{}{}
		languages[pair.Target] = struct{}{}
	}

	if len(languages) != 2 {
		return fmt.Errorf("%w: P0 bilingual config must cover exactly two languages", ErrInvalidLanguagePair)
	}

	for source, target := range seen {
		if seen[target] != source {
			return fmt.Errorf("%w: directions must be mutual inverses", ErrInvalidLanguagePair)
		}
	}
	return nil
}

// requestFingerprint hashes the full create request body for idempotent replay.
// Languages keep request order; expected_version is included when present.
func requestFingerprint(req CreateLanguageConfigRequest) string {
	payload := struct {
		Languages       []LanguagePair `json:"languages"`
		OutputRoutes    []OutputRoute  `json:"output_routes,omitempty"`
		ExpectedVersion *int           `json:"expected_version"`
	}{
		Languages:       req.Languages,
		OutputRoutes:    req.OutputRoutes,
		ExpectedVersion: req.ExpectedVersion,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func normalizeOutputRoutes(pairs []LanguagePair, routes []OutputRoute) ([]OutputRoute, error) {
	targets := make(map[string]struct{}, len(pairs))
	for _, pair := range pairs {
		targets[pair.Target] = struct{}{}
	}
	if len(routes) == 0 {
		defaults := make([]OutputRoute, 0, len(targets))
		for _, pair := range pairs {
			if _, exists := targets[pair.Target]; !exists {
				continue
			}
			targets[pair.Target] = struct{}{}
			defaults = append(defaults, OutputRoute{TargetLanguage: pair.Target, TTSEnabled: true, DeliveryEnabled: false})
			delete(targets, pair.Target)
		}
		return defaults, nil
	}

	seen := make(map[string]struct{}, len(routes))
	deliveryRoutes := 0
	for _, route := range routes {
		if route.TargetLanguage == "" {
			return nil, fmt.Errorf("%w: output route target_language is required", ErrInvalidRequest)
		}
		if _, exists := targets[route.TargetLanguage]; !exists {
			return nil, fmt.Errorf("%w: output route target_language %s is not configured", ErrInvalidLanguagePair, route.TargetLanguage)
		}
		if _, exists := seen[route.TargetLanguage]; exists {
			return nil, fmt.Errorf("%w: duplicate output route %s", ErrInvalidLanguagePair, route.TargetLanguage)
		}
		if route.TTSEnabled == route.DeliveryEnabled {
			return nil, fmt.Errorf("%w: output route %s must enable exactly one output", ErrInvalidRequest, route.TargetLanguage)
		}
		if route.DeliveryEnabled {
			deliveryRoutes++
		}
		seen[route.TargetLanguage] = struct{}{}
	}
	for target := range targets {
		if _, exists := seen[target]; !exists {
			return nil, fmt.Errorf("%w: missing output route %s", ErrInvalidLanguagePair, target)
		}
	}
	if deliveryRoutes > 1 {
		return nil, fmt.Errorf("%w: only one output route may enable delivery", ErrInvalidRequest)
	}
	return append([]OutputRoute(nil), routes...), nil
}

func sameIdempotentRequest(existing LanguageConfig, sessionID, fingerprint string) bool {
	return existing.SessionID == sessionID &&
		existing.RequestFingerprint != "" &&
		existing.RequestFingerprint == fingerprint
}

func toSnapshot(cfg LanguageConfig) LanguageConfigSnapshot {
	return LanguageConfigSnapshot{
		SessionID:     cfg.SessionID,
		Version:       cfg.Version,
		LanguagePairs: append([]LanguagePair(nil), cfg.LanguagePairs...),
		OutputRoutes:  append([]OutputRoute(nil), cfg.OutputRoutes...),
		Status:        cfg.Status,
		EffectiveFrom: cfg.EffectiveFrom,
		UpdatedAt:     cfg.CreatedAt,
	}
}
