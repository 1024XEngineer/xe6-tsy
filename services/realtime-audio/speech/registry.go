package speech

import (
	"strings"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/asr"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/tts"
)

// ProviderRegistry is an immutable process-local mapping from profile ids to
// vendor-neutral speech adapters. Registrations are copied at construction so
// later caller mutations cannot alter Turn selection.
type ProviderRegistry struct {
	asr map[string]asrRegistration
	tts map[string]ttsRegistration
}

type asrRegistration struct {
	profile Profile
	adapter asr.Provider
}

type ttsRegistration struct {
	profile Profile
	adapter tts.Provider
}

// NewProviderRegistry validates and copies ASR and TTS profile registrations.
// Profile id namespaces are intentionally separate because one logical label
// can identify different ASR and TTS provider configurations.
func NewProviderRegistry(asrProfiles []ASRProfile, ttsProfiles []TTSProfile) (*ProviderRegistry, error) {
	registry := &ProviderRegistry{
		asr: make(map[string]asrRegistration, len(asrProfiles)),
		tts: make(map[string]ttsRegistration, len(ttsProfiles)),
	}
	for _, configured := range asrProfiles {
		profile, err := normalizeProfile(configured.Profile)
		if err != nil {
			return nil, err
		}
		if configured.Adapter == nil {
			return nil, ErrASRProviderRequired
		}
		if _, exists := registry.asr[profile.ID]; exists {
			return nil, ErrDuplicateASRProfile
		}
		registry.asr[profile.ID] = asrRegistration{profile: profile, adapter: configured.Adapter}
	}
	for _, configured := range ttsProfiles {
		profile, err := normalizeProfile(configured.Profile)
		if err != nil {
			return nil, err
		}
		if configured.Adapter == nil {
			return nil, ErrTTSProviderRequired
		}
		if _, exists := registry.tts[profile.ID]; exists {
			return nil, ErrDuplicateTTSProfile
		}
		registry.tts[profile.ID] = ttsRegistration{profile: profile, adapter: configured.Adapter}
	}
	return registry, nil
}

// ASR returns the adapter registered for one ASR profile id.
func (r *ProviderRegistry) ASR(profileID string) (asr.Provider, error) {
	if r == nil {
		return nil, ErrProviderRegistryRequired
	}
	profileID = strings.TrimSpace(profileID)
	if profileID == "" {
		return nil, ErrProfileIDRequired
	}
	registered, ok := r.asr[profileID]
	if !ok {
		return nil, ErrASRProfileNotFound
	}
	return registered.adapter, nil
}

// TTS returns the adapter registered for one TTS profile id.
func (r *ProviderRegistry) TTS(profileID string) (tts.Provider, error) {
	if r == nil {
		return nil, ErrProviderRegistryRequired
	}
	profileID = strings.TrimSpace(profileID)
	if profileID == "" {
		return nil, ErrProfileIDRequired
	}
	registered, ok := r.tts[profileID]
	if !ok {
		return nil, ErrTTSProfileNotFound
	}
	return registered.adapter, nil
}

// ASRProfile returns an isolated copy of the ASR profile metadata.
func (r *ProviderRegistry) ASRProfile(profileID string) (Profile, error) {
	if r == nil {
		return Profile{}, ErrProviderRegistryRequired
	}
	profileID = strings.TrimSpace(profileID)
	if profileID == "" {
		return Profile{}, ErrProfileIDRequired
	}
	registered, ok := r.asr[profileID]
	if !ok {
		return Profile{}, ErrASRProfileNotFound
	}
	return cloneProfile(registered.profile), nil
}

// TTSProfile returns an isolated copy of the TTS profile metadata.
func (r *ProviderRegistry) TTSProfile(profileID string) (Profile, error) {
	if r == nil {
		return Profile{}, ErrProviderRegistryRequired
	}
	profileID = strings.TrimSpace(profileID)
	if profileID == "" {
		return Profile{}, ErrProfileIDRequired
	}
	registered, ok := r.tts[profileID]
	if !ok {
		return Profile{}, ErrTTSProfileNotFound
	}
	return cloneProfile(registered.profile), nil
}
