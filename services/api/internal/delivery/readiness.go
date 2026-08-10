package delivery

import (
	"context"
	"strings"
)

type preferenceReader interface {
	ListPreferences(context.Context, string) ([]Preference, error)
}

// RuntimeReadiness combines durable target state with the provider capability
// used by the configured delivery worker. A target is not usable for single
// output when the runtime or its channel provider is absent.
type RuntimeReadiness struct {
	preferences preferenceReader
	provider    *ChannelRouter
}

// NewRuntimeReadiness constructs the language-service readiness boundary.
func NewRuntimeReadiness(preferences preferenceReader, provider *ChannelRouter) *RuntimeReadiness {
	return &RuntimeReadiness{preferences: preferences, provider: provider}
}

// HasReadyAutomaticTarget implements the language configuration readiness port.
func (r *RuntimeReadiness) HasReadyAutomaticTarget(ctx context.Context, accountID string) (bool, error) {
	if r == nil || r.preferences == nil || r.provider == nil {
		return false, nil
	}
	preferences, err := r.preferences.ListPreferences(ctx, accountID)
	if err != nil {
		return false, err
	}
	for _, preference := range preferences {
		if preference.Enabled && preference.Verified &&
			strings.TrimSpace(preference.DestinationRef) != "" &&
			r.provider.SupportsChannel(preference.Channel) {
			return true, nil
		}
	}
	return false, nil
}
