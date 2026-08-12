// Package speech owns the immutable provider selections captured by realtime Turns.
package speech

import (
	"errors"
	"strings"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/asr"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/tts"
)

var (
	// ErrProviderRegistryRequired indicates that provider lookup cannot proceed without a registry.
	ErrProviderRegistryRequired = errors.New("speech provider registry is required")
	// ErrRouteResolverRequired indicates that a coordinator cannot resolve language routes.
	ErrRouteResolverRequired = errors.New("speech route resolver is required")
	// ErrProfileIDRequired prevents ambiguous provider registrations and lookups.
	ErrProfileIDRequired = errors.New("speech profile id is required")
	// ErrASRProviderRequired prevents a route from resolving to an unusable ASR profile.
	ErrASRProviderRequired = errors.New("ASR provider is required")
	// ErrTTSProviderRequired prevents a route from resolving to an unusable TTS profile.
	ErrTTSProviderRequired = errors.New("TTS provider is required")
	// ErrASRProfileNotFound identifies an ASR profile absent from the registry.
	ErrASRProfileNotFound = errors.New("ASR speech profile not found")
	// ErrTTSProfileNotFound identifies a TTS profile absent from the registry.
	ErrTTSProfileNotFound = errors.New("TTS speech profile not found")
	// ErrDuplicateASRProfile rejects two ASR adapters with the same profile id.
	ErrDuplicateASRProfile = errors.New("duplicate ASR speech profile")
	// ErrDuplicateTTSProfile rejects two TTS adapters with the same profile id.
	ErrDuplicateTTSProfile = errors.New("duplicate TTS speech profile")
)

// Profile is immutable provider-selection metadata. Voice is meaningful for TTS
// profiles and may be empty for ASR profiles or models with a provider default.
type Profile struct {
	ID           string
	Provider     string
	Model        string
	Voice        string
	Capabilities []string
}

// ASRProfile registers one vendor-neutral ASR adapter under immutable metadata.
type ASRProfile struct {
	Profile Profile
	Adapter asr.Provider
}

// TTSProfile registers one vendor-neutral TTS adapter under immutable metadata.
type TTSProfile struct {
	Profile Profile
	Adapter tts.Provider
}

func normalizeProfile(profile Profile) (Profile, error) {
	profile.ID = strings.TrimSpace(profile.ID)
	if profile.ID == "" {
		return Profile{}, ErrProfileIDRequired
	}
	profile.Provider = strings.TrimSpace(profile.Provider)
	profile.Model = strings.TrimSpace(profile.Model)
	profile.Voice = strings.TrimSpace(profile.Voice)
	profile.Capabilities = append([]string(nil), profile.Capabilities...)
	return profile, nil
}

func cloneProfile(profile Profile) Profile {
	clone := profile
	clone.Capabilities = append([]string(nil), profile.Capabilities...)
	return clone
}
