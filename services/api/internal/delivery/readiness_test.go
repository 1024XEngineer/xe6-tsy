package delivery

import (
	"context"
	"testing"
)

type readinessPreferenceReader struct {
	preferences []Preference
}

func (r readinessPreferenceReader) ListPreferences(context.Context, string) ([]Preference, error) {
	return r.preferences, nil
}

func TestRuntimeReadinessRequiresConfiguredTargetChannel(t *testing.T) {
	tests := []struct {
		name       string
		preference Preference
		provider   *ChannelRouter
		want       bool
	}{
		{
			name: "configured email provider",
			preference: Preference{
				Channel:        ChannelEmail,
				DestinationRef: "email-1",
				Enabled:        true,
				Verified:       true,
			},
			provider: NewChannelRouter(NewFakeEmailProvider(FakeEmailProviderConfig{}), UnconfiguredProvider{}),
			want:     true,
		},
		{
			name: "target channel provider is unconfigured",
			preference: Preference{
				Channel:        ChannelWeChat,
				DestinationRef: "wechat-1",
				Enabled:        true,
				Verified:       true,
			},
			provider: NewChannelRouter(NewFakeEmailProvider(FakeEmailProviderConfig{}), UnconfiguredProvider{}),
			want:     false,
		},
		{
			name: "delivery runtime is absent",
			preference: Preference{
				Channel:        ChannelEmail,
				DestinationRef: "email-1",
				Enabled:        true,
				Verified:       true,
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			readiness := NewRuntimeReadiness(
				readinessPreferenceReader{preferences: []Preference{tt.preference}},
				tt.provider,
			)
			got, err := readiness.HasReadyAutomaticTarget(t.Context(), "account-1")
			if err != nil {
				t.Fatalf("HasReadyAutomaticTarget() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("HasReadyAutomaticTarget() = %v, want %v", got, tt.want)
			}
		})
	}
}
