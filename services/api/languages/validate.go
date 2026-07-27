package languages

import "fmt"

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

func languagePairsEqual(a, b []LanguagePair) bool {
	if len(a) != len(b) {
		return false
	}
	left := make(map[string]string, len(a))
	for _, pair := range a {
		left[pair.Source] = pair.Target
	}
	for _, pair := range b {
		if left[pair.Source] != pair.Target {
			return false
		}
	}
	return true
}

func toSnapshot(cfg LanguageConfig) LanguageConfigSnapshot {
	return LanguageConfigSnapshot{
		SessionID:     cfg.SessionID,
		Version:       cfg.Version,
		LanguagePairs: append([]LanguagePair(nil), cfg.LanguagePairs...),
		Status:        cfg.Status,
		EffectiveFrom: cfg.EffectiveFrom,
		UpdatedAt:     cfg.CreatedAt,
	}
}
