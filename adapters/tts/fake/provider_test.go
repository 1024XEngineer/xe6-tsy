package fake

import (
	"testing"

	"github.com/1024XEngineer/xe6-tsy/apps/api/pkg/speechport"
)

func TestProvider_SynthesizeReturnsConfiguredResult(t *testing.T) {
	provider := NewProvider(speechport.SynthesizeResult{AudioAssetRef: "asset://tts-1"}, nil)

	result, err := provider.Synthesize(t.Context(), speechport.SynthesizeRequest{Text: "合成普通话"})
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	if result.AudioAssetRef != "asset://tts-1" {
		t.Fatalf("audio asset ref = %q", result.AudioAssetRef)
	}
	if provider.Calls() != 1 {
		t.Fatalf("calls = %d, want 1", provider.Calls())
	}
}
