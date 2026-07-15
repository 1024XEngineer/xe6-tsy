package fake

import (
	"testing"

	"github.com/1024XEngineer/xe6-tsy/apps/api/pkg/speechport"
)

func TestProvider_TranslateReturnsConfiguredResult(t *testing.T) {
	provider := NewProvider(speechport.TranslateResult{Text: "合成普通话"}, nil)

	result, err := provider.Translate(t.Context(), speechport.TranslateRequest{Text: "合成四川话"})
	if err != nil {
		t.Fatalf("Translate() error = %v", err)
	}
	if result.Text != "合成普通话" {
		t.Fatalf("text = %q", result.Text)
	}
	if provider.Calls() != 1 {
		t.Fatalf("calls = %d, want 1", provider.Calls())
	}
}
