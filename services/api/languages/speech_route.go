package languages

import (
	"errors"
	"fmt"
	"strings"

	"golang.org/x/text/language"
)

var errSpeechLanguageInvalid = errors.New("speech route language is invalid")

// normalizeSpeechLanguage parses a BCP-47 tag and returns the stable spelling
// used by the catalog and active route lookup key. Underscores are accepted only as
// a compatibility input form and are converted before parsing.
func normalizeSpeechLanguage(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%w: language is required", errSpeechLanguageInvalid)
	}
	value = strings.ReplaceAll(value, "_", "-")
	tag, err := language.Parse(value)
	if err != nil || tag == language.Und {
		return "", fmt.Errorf("%w: %q", errSpeechLanguageInvalid, value)
	}
	return tag.String(), nil
}

func normalizeSpeechPair(languageA, languageB string) (string, string, error) {
	a, err := normalizeSpeechLanguage(languageA)
	if err != nil {
		return "", "", err
	}
	b, err := normalizeSpeechLanguage(languageB)
	if err != nil {
		return "", "", err
	}
	if a == b {
		return "", "", fmt.Errorf("%w: languages must differ", errSpeechLanguageInvalid)
	}
	if a > b {
		a, b = b, a
	}
	return a, b, nil
}

func speechLanguageSet(values []string) (map[string]struct{}, error) {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		languageCode, err := normalizeSpeechLanguage(value)
		if err != nil {
			return nil, err
		}
		set[languageCode] = struct{}{}
	}
	return set, nil
}

func speechPairFromDirections(pairs []LanguagePair) (string, string, error) {
	if len(pairs) != 2 {
		return "", "", fmt.Errorf("%w: exactly two language directions are required", errSpeechLanguageInvalid)
	}
	firstA, firstB, err := normalizeSpeechPair(pairs[0].Source, pairs[0].Target)
	if err != nil {
		return "", "", err
	}
	secondA, secondB, err := normalizeSpeechPair(pairs[1].Source, pairs[1].Target)
	if err != nil || firstA != secondA || firstB != secondB {
		return "", "", fmt.Errorf("%w: directions must describe one mutual pair", errSpeechLanguageInvalid)
	}
	firstSource, err := normalizeSpeechLanguage(pairs[0].Source)
	if err != nil {
		return "", "", err
	}
	secondSource, err := normalizeSpeechLanguage(pairs[1].Source)
	if err != nil {
		return "", "", err
	}
	if firstSource == secondSource {
		return "", "", fmt.Errorf("%w: source languages must differ", errSpeechLanguageInvalid)
	}
	return firstA, firstB, nil
}

func validateSpeechRouteCapabilities(
	route SpeechRoute,
	asrProfile ASRProfile,
	ttsProfile TTSProfile,
	ttsTargetLanguages map[string]struct{},
) error {
	languageA, languageB, err := validateSpeechRoute(route)
	if err != nil {
		return err
	}
	if err := validateASRProfile(asrProfile); err != nil {
		return err
	}
	if err := validateTTSProfile(ttsProfile); err != nil {
		return err
	}
	if strings.TrimSpace(asrProfile.ID) != strings.TrimSpace(route.ASRProfileID) || strings.TrimSpace(ttsProfile.ID) != strings.TrimSpace(route.TTSProfileID) {
		return ErrSpeechRouteInvalid
	}

	asrLanguages, err := speechLanguageSet(asrProfile.SupportedLanguages)
	if err != nil {
		return ErrSpeechRouteInvalid
	}
	if !asrProfile.SupportsAutoDetect {
		for _, languageCode := range []string{languageA, languageB} {
			if _, ok := asrLanguages[languageCode]; !ok {
				return fmt.Errorf("%w: ASR profile %s does not support %s", ErrSpeechRouteInvalid, asrProfile.ID, languageCode)
			}
		}
	}

	ttsLanguages, err := speechLanguageSet(ttsProfile.SupportedLanguages)
	if err != nil {
		return ErrSpeechRouteInvalid
	}
	for languageCode := range ttsTargetLanguages {
		if _, ok := ttsLanguages[languageCode]; !ok {
			return fmt.Errorf("%w: TTS profile %s does not support %s", ErrSpeechRouteInvalid, ttsProfile.ID, languageCode)
		}
	}
	return nil
}

func validateSpeechRoute(route SpeechRoute) (string, string, error) {
	languageA, languageB, err := normalizeSpeechPair(route.LanguageA, route.LanguageB)
	if err != nil ||
		languageA != route.LanguageA ||
		languageB != route.LanguageB ||
		strings.TrimSpace(route.ID) == "" ||
		strings.TrimSpace(route.ASRProfileID) == "" ||
		strings.TrimSpace(route.TTSProfileID) == "" ||
		!route.Enabled ||
		route.RetiredAt != nil {
		return "", "", ErrSpeechRouteInvalid
	}
	return languageA, languageB, nil
}

func validateASRProfile(profile ASRProfile) error {
	if strings.TrimSpace(profile.ID) == "" ||
		strings.TrimSpace(profile.ProviderCode) == "" ||
		strings.TrimSpace(profile.Model) == "" ||
		strings.TrimSpace(profile.InputEncoding) == "" ||
		profile.InputSampleRateHz < 1 ||
		profile.InputChannels < 1 ||
		!profile.Enabled ||
		profile.RetiredAt != nil {
		return ErrSpeechRouteInvalid
	}
	return nil
}

func validateTTSProfile(profile TTSProfile) error {
	if strings.TrimSpace(profile.ID) == "" ||
		strings.TrimSpace(profile.ProviderCode) == "" ||
		strings.TrimSpace(profile.Model) == "" ||
		strings.TrimSpace(profile.VoiceID) == "" ||
		strings.TrimSpace(profile.OutputEncoding) == "" ||
		profile.OutputSampleRateHz < 1 ||
		profile.OutputChannels < 1 ||
		!profile.Enabled ||
		profile.RetiredAt != nil {
		return ErrSpeechRouteInvalid
	}
	return nil
}
