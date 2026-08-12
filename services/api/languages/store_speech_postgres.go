package languages

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

type loadedSpeechRoute struct {
	route SpeechRoute
	asr   ASRProfile
	tts   TTSProfile
}

// ResolveSpeechRoute returns an active, unretired route for either ordering of
// a language pair. It is an internal control-plane read and has no HTTP route.
func (s *PostgresStore) ResolveSpeechRoute(
	ctx context.Context,
	languageA string,
	languageB string,
) (SpeechRoute, error) {
	loaded, err := s.loadSpeechRoute(ctx, languageA, languageB)
	if err != nil {
		return SpeechRoute{}, err
	}
	if err := validateLoadedSpeechRoute(loaded); err != nil {
		return SpeechRoute{}, err
	}
	return loaded.route, nil
}

// ValidateSpeechRoute ensures a persisted route can serve the requested
// bilingual configuration and only requires TTS coverage for enabled outputs.
func (s *PostgresStore) ValidateSpeechRoute(
	ctx context.Context,
	pairs []LanguagePair,
	routes []OutputRoute,
) error {
	languageA, languageB, err := speechPairFromDirections(pairs)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrSpeechRouteInvalid, err)
	}
	loaded, err := s.loadSpeechRoute(ctx, languageA, languageB)
	if err != nil {
		return err
	}

	ttsTargets := make(map[string]struct{}, len(routes))
	for _, outputRoute := range routes {
		if !outputRoute.TTSEnabled {
			continue
		}
		targetLanguage, err := normalizeSpeechLanguage(outputRoute.TargetLanguage)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrSpeechRouteInvalid, err)
		}
		ttsTargets[targetLanguage] = struct{}{}
	}
	if err := validateSpeechRouteCapabilities(loaded.route, loaded.asr, loaded.tts, ttsTargets); err != nil {
		return err
	}
	return nil
}

func (s *PostgresStore) loadSpeechRoute(
	ctx context.Context,
	languageA string,
	languageB string,
) (loadedSpeechRoute, error) {
	if s == nil || s.pool == nil {
		return loadedSpeechRoute{}, ErrNotImplemented
	}
	languageA, languageB, err := normalizeSpeechPair(languageA, languageB)
	if err != nil {
		return loadedSpeechRoute{}, fmt.Errorf("%w: %v", ErrSpeechRouteInvalid, err)
	}

	var loaded loadedSpeechRoute
	err = s.pool.QueryRow(ctx, `
SELECT
    r.id,
    r.language_a,
    r.language_b,
    r.asr_profile_id,
    r.tts_profile_id,
    r.enabled,
    r.retired_at,
    asr.id,
    asr.provider_code,
    asr.model,
    ARRAY(
        SELECT language_code
        FROM speech_asr_profile_languages
        WHERE profile_id = asr.id
        ORDER BY language_code
    ),
    asr.supports_auto_detect,
    asr.supports_streaming,
    asr.input_encoding,
    asr.input_sample_rate_hz,
    asr.input_channels,
    asr.enabled,
    asr.retired_at,
    asr.created_at,
    asr.updated_at,
    tts.id,
    tts.provider_code,
    tts.model,
    tts.voice_id,
    ARRAY(
        SELECT language_code
        FROM speech_tts_profile_languages
        WHERE profile_id = tts.id
        ORDER BY language_code
    ),
    tts.supports_streaming,
    tts.output_encoding,
    tts.output_sample_rate_hz,
    tts.output_channels,
    tts.enabled,
    tts.retired_at,
    tts.created_at,
    tts.updated_at
FROM speech_language_pair_routes AS r
JOIN speech_asr_profiles AS asr ON asr.id = r.asr_profile_id
JOIN speech_tts_profiles AS tts ON tts.id = r.tts_profile_id
WHERE r.language_a = $1
  AND r.language_b = $2
  AND r.enabled = TRUE
  AND r.retired_at IS NULL`, languageA, languageB).Scan(
		&loaded.route.ID,
		&loaded.route.LanguageA,
		&loaded.route.LanguageB,
		&loaded.route.ASRProfileID,
		&loaded.route.TTSProfileID,
		&loaded.route.Enabled,
		&loaded.route.RetiredAt,
		&loaded.asr.ID,
		&loaded.asr.ProviderCode,
		&loaded.asr.Model,
		&loaded.asr.SupportedLanguages,
		&loaded.asr.SupportsAutoDetect,
		&loaded.asr.SupportsStreaming,
		&loaded.asr.InputEncoding,
		&loaded.asr.InputSampleRateHz,
		&loaded.asr.InputChannels,
		&loaded.asr.Enabled,
		&loaded.asr.RetiredAt,
		&loaded.asr.CreatedAt,
		&loaded.asr.UpdatedAt,
		&loaded.tts.ID,
		&loaded.tts.ProviderCode,
		&loaded.tts.Model,
		&loaded.tts.VoiceID,
		&loaded.tts.SupportedLanguages,
		&loaded.tts.SupportsStreaming,
		&loaded.tts.OutputEncoding,
		&loaded.tts.OutputSampleRateHz,
		&loaded.tts.OutputChannels,
		&loaded.tts.Enabled,
		&loaded.tts.RetiredAt,
		&loaded.tts.CreatedAt,
		&loaded.tts.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return loadedSpeechRoute{}, fmt.Errorf("%w: %s and %s", ErrSpeechRouteNotFound, languageA, languageB)
	}
	if err != nil {
		return loadedSpeechRoute{}, fmt.Errorf("load speech route: %w", err)
	}
	return loaded, nil
}

func validateLoadedSpeechRoute(loaded loadedSpeechRoute) error {
	languageA, languageB, err := validateSpeechRoute(loaded.route)
	if err != nil {
		return err
	}
	if err := validateASRProfile(loaded.asr); err != nil {
		return err
	}
	if err := validateTTSProfile(loaded.tts); err != nil {
		return err
	}
	if strings.TrimSpace(loaded.route.ASRProfileID) == "" || strings.TrimSpace(loaded.route.TTSProfileID) == "" || loaded.route.ASRProfileID != loaded.asr.ID || loaded.route.TTSProfileID != loaded.tts.ID {
		return ErrSpeechRouteInvalid
	}
	asrLanguages, err := speechLanguageSet(loaded.asr.SupportedLanguages)
	if err != nil {
		return ErrSpeechRouteInvalid
	}
	if !loaded.asr.SupportsAutoDetect {
		for _, languageCode := range []string{languageA, languageB} {
			if _, ok := asrLanguages[languageCode]; !ok {
				return fmt.Errorf("%w: ASR profile %s does not support %s", ErrSpeechRouteInvalid, loaded.asr.ID, languageCode)
			}
		}
	}
	if _, err := speechLanguageSet(loaded.tts.SupportedLanguages); err != nil {
		return ErrSpeechRouteInvalid
	}
	return nil
}

var _ SpeechRouteReader = (*PostgresStore)(nil)
var _ SpeechRouteValidator = (*PostgresStore)(nil)
