package localruntime

import (
	"errors"
	"fmt"
	"strings"

	languagesv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/languages/v1"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/config"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/speech"
	"golang.org/x/text/language"
)

var (
	ErrSpeechCatalogEmpty         = errors.New("speech catalog has no active route")
	ErrSpeechCatalogInvalid       = errors.New("speech catalog is invalid")
	ErrSpeechCatalogProfileAbsent = errors.New("speech catalog profile is absent")
)

// SpeechCatalog is the validated non-secret control-plane snapshot used to
// construct the process-local speech registry. Credentials and endpoints are
// deliberately supplied separately by ProviderConfig.
type SpeechCatalog struct {
	ASRProfiles []languagesv1.ASRProfile
	TTSProfiles []languagesv1.TTSProfile
	Routes      []languagesv1.SpeechRoute
}

// ValidateSpeechCatalog enforces the realtime media contract before any vendor
// adapter is constructed. Active routes are the only source of session choices.
func ValidateSpeechCatalog(catalog SpeechCatalog) error {
	if len(catalog.Routes) == 0 {
		return ErrSpeechCatalogEmpty
	}
	asrProfiles := make(map[string]languagesv1.ASRProfile, len(catalog.ASRProfiles))
	for _, profile := range catalog.ASRProfiles {
		if err := validateCatalogASRProfile(profile); err != nil {
			return err
		}
		if _, exists := asrProfiles[profile.ID]; exists {
			return fmt.Errorf("%w: duplicate ASR profile %q", ErrSpeechCatalogInvalid, profile.ID)
		}
		asrProfiles[profile.ID] = profile
	}
	ttsProfiles := make(map[string]languagesv1.TTSProfile, len(catalog.TTSProfiles))
	for _, profile := range catalog.TTSProfiles {
		if err := validateCatalogTTSProfile(profile); err != nil {
			return err
		}
		if _, exists := ttsProfiles[profile.ID]; exists {
			return fmt.Errorf("%w: duplicate TTS profile %q", ErrSpeechCatalogInvalid, profile.ID)
		}
		ttsProfiles[profile.ID] = profile
	}
	routeKeys := make(map[string]struct{}, len(catalog.Routes))
	for _, route := range catalog.Routes {
		languageA, languageB, err := canonicalSpeechPair(route.LanguageA, route.LanguageB)
		if err != nil || route.ID == "" || !route.Enabled || route.RetiredAt != nil ||
			route.LanguageA != languageA || route.LanguageB != languageB {
			return fmt.Errorf("%w: route %q", ErrSpeechCatalogInvalid, route.ID)
		}
		key := languageA + "\x00" + languageB
		if _, exists := routeKeys[key]; exists {
			return fmt.Errorf("%w: duplicate language pair %s/%s", ErrSpeechCatalogInvalid, languageA, languageB)
		}
		routeKeys[key] = struct{}{}
		asrProfile, ok := asrProfiles[route.ASRProfileID]
		if !ok {
			return fmt.Errorf("%w: ASR profile %q", ErrSpeechCatalogProfileAbsent, route.ASRProfileID)
		}
		ttsProfile, ok := ttsProfiles[route.TTSProfileID]
		if !ok {
			return fmt.Errorf("%w: TTS profile %q", ErrSpeechCatalogProfileAbsent, route.TTSProfileID)
		}
		if err := validateRouteCoverage(languageA, languageB, asrProfile, ttsProfile); err != nil {
			return fmt.Errorf("%w: route %q: %w", ErrSpeechCatalogInvalid, route.ID, err)
		}
	}
	return nil
}

// BuildSpeechRegistry constructs every active profile so a later route switch
// is a registry lookup rather than a provider protocol or credential decision.
func BuildSpeechRegistry(catalog SpeechCatalog, providerConfig config.ProviderConfig) (*speech.ProviderRegistry, speech.RouteResolver, error) {
	if err := ValidateSpeechCatalog(catalog); err != nil {
		return nil, nil, err
	}
	asrRegistrations := make([]speech.ASRProfile, 0, len(catalog.ASRProfiles))
	for _, profile := range catalog.ASRProfiles {
		deployment := providerConfig.ASR
		deployment.SampleRate = profile.InputSampleRateHz
		adapter, err := config.BuildASRProfileAdapter(profile.ProviderCode, profile.Model, deployment)
		if err != nil {
			return nil, nil, fmt.Errorf("build ASR profile %q: %w", profile.ID, err)
		}
		asrRegistrations = append(asrRegistrations, speech.ASRProfile{
			Profile: speech.Profile{
				ID: profile.ID, Provider: profile.ProviderCode, Model: profile.Model,
				Capabilities: []string{"streaming", profile.InputEncoding, fmt.Sprintf("%dhz", profile.InputSampleRateHz), fmt.Sprintf("%d-channel", profile.InputChannels)},
			},
			Adapter: adapter,
		})
	}
	ttsRegistrations := make([]speech.TTSProfile, 0, len(catalog.TTSProfiles))
	for _, profile := range catalog.TTSProfiles {
		deployment := providerConfig.TTS
		deployment.SampleRate = profile.OutputSampleRateHz
		adapter, err := config.BuildTTSProfileAdapter(profile.ProviderCode, profile.Model, profile.VoiceID, deployment)
		if err != nil {
			return nil, nil, fmt.Errorf("build TTS profile %q: %w", profile.ID, err)
		}
		ttsRegistrations = append(ttsRegistrations, speech.TTSProfile{
			Profile: speech.Profile{
				ID: profile.ID, Provider: profile.ProviderCode, Model: profile.Model, Voice: profile.VoiceID,
				Capabilities: []string{"streaming", profile.OutputEncoding, fmt.Sprintf("%dhz", profile.OutputSampleRateHz), fmt.Sprintf("%d-channel", profile.OutputChannels)},
			},
			Adapter: adapter,
		})
	}
	registry, err := speech.NewProviderRegistry(asrRegistrations, ttsRegistrations)
	if err != nil {
		return nil, nil, fmt.Errorf("create speech provider registry: %w", err)
	}
	routes := make([]speech.SpeechRoute, 0, len(catalog.Routes))
	for _, route := range catalog.Routes {
		routes = append(routes, speech.SpeechRoute{
			LanguageA: route.LanguageA, LanguageB: route.LanguageB,
			ASRProfileID: route.ASRProfileID, TTSProfileID: route.TTSProfileID,
		})
	}
	resolver, err := speech.NewRouteResolver(routes)
	if err != nil {
		return nil, nil, fmt.Errorf("create speech route resolver: %w", err)
	}
	return registry, resolver, nil
}

func validateCatalogASRProfile(profile languagesv1.ASRProfile) error {
	if strings.TrimSpace(profile.ID) == "" || strings.TrimSpace(profile.ProviderCode) == "" ||
		strings.TrimSpace(profile.Model) == "" || !profile.Enabled || profile.RetiredAt != nil ||
		!profile.SupportsStreaming || !strings.EqualFold(strings.TrimSpace(profile.InputEncoding), "pcm_s16le") ||
		profile.InputSampleRateHz != 16000 || profile.InputChannels != 1 {
		return fmt.Errorf("%w: ASR profile %q does not satisfy pcm_s16le/16kHz/mono streaming contract", ErrSpeechCatalogInvalid, profile.ID)
	}
	if _, err := canonicalSpeechLanguages(profile.SupportedLanguages); err != nil && !profile.SupportsAutoDetect {
		return fmt.Errorf("%w: ASR profile %q languages: %v", ErrSpeechCatalogInvalid, profile.ID, err)
	}
	return nil
}

func validateCatalogTTSProfile(profile languagesv1.TTSProfile) error {
	if strings.TrimSpace(profile.ID) == "" || strings.TrimSpace(profile.ProviderCode) == "" ||
		strings.TrimSpace(profile.Model) == "" || strings.TrimSpace(profile.VoiceID) == "" ||
		!profile.Enabled || profile.RetiredAt != nil || !profile.SupportsStreaming ||
		!strings.EqualFold(strings.TrimSpace(profile.OutputEncoding), "pcm_s16le") ||
		profile.OutputSampleRateHz != 24000 || profile.OutputChannels != 1 {
		return fmt.Errorf("%w: TTS profile %q does not satisfy pcm_s16le/24kHz/mono streaming contract", ErrSpeechCatalogInvalid, profile.ID)
	}
	if _, err := canonicalSpeechLanguages(profile.SupportedLanguages); err != nil {
		return fmt.Errorf("%w: TTS profile %q languages: %v", ErrSpeechCatalogInvalid, profile.ID, err)
	}
	return nil
}

func validateRouteCoverage(languageA, languageB string, asrProfile languagesv1.ASRProfile, ttsProfile languagesv1.TTSProfile) error {
	asrLanguages, err := canonicalSpeechLanguages(asrProfile.SupportedLanguages)
	if err != nil {
		return err
	}
	if !asrProfile.SupportsAutoDetect {
		if _, ok := asrLanguages[languageA]; !ok {
			return fmt.Errorf("ASR profile %q does not support %s", asrProfile.ID, languageA)
		}
		if _, ok := asrLanguages[languageB]; !ok {
			return fmt.Errorf("ASR profile %q does not support %s", asrProfile.ID, languageB)
		}
	}
	ttsLanguages, err := canonicalSpeechLanguages(ttsProfile.SupportedLanguages)
	if err != nil {
		return err
	}
	for _, languageCode := range []string{languageA, languageB} {
		if _, ok := ttsLanguages[languageCode]; !ok {
			return fmt.Errorf("TTS profile %q does not support %s", ttsProfile.ID, languageCode)
		}
	}
	return nil
}

func canonicalSpeechPair(languageA, languageB string) (string, string, error) {
	a, err := canonicalSpeechLanguage(languageA)
	if err != nil {
		return "", "", err
	}
	b, err := canonicalSpeechLanguage(languageB)
	if err != nil {
		return "", "", err
	}
	if a == b {
		return "", "", errors.New("speech route languages must differ")
	}
	if a > b {
		a, b = b, a
	}
	return a, b, nil
}

func canonicalSpeechLanguages(values []string) (map[string]struct{}, error) {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		languageCode, err := canonicalSpeechLanguage(value)
		if err != nil {
			return nil, err
		}
		set[languageCode] = struct{}{}
	}
	return set, nil
}

func canonicalSpeechLanguage(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("speech language is required")
	}
	tag, err := language.Parse(strings.ReplaceAll(value, "_", "-"))
	if err != nil || tag == language.Und {
		return "", fmt.Errorf("speech language %q is invalid", value)
	}
	return tag.String(), nil
}
