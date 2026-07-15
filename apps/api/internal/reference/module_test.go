package reference

import (
	"context"
	"errors"
	"testing"
)

func TestNewReturnsModule(t *testing.T) {
	mod, err := New(Deps{})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if mod == nil {
		t.Fatal("New returned nil module")
	}
}

func TestServiceMethodsReturnNotImplemented(t *testing.T) {
	mod, err := New(Deps{})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	tests := []struct {
		name string
		call func() error
	}{
		{
			name: "retrieve analysis context",
			call: func() error {
				_, err := mod.RetrieveAnalysisContext(context.Background(), RetrieveAnalysisContextCommand{})
				return err
			},
		},
		{
			name: "generate reference suggestions",
			call: func() error {
				_, err := mod.GenerateReferenceSuggestions(context.Background(), GenerateReferenceSuggestionsCommand{})
				return err
			},
		},
		{
			name: "reference suggestion set by id",
			call: func() error {
				_, err := mod.ReferenceSuggestionSetByID(context.Background(), "set-1")
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.call(); !errors.Is(err, ErrNotImplemented) {
				t.Fatalf("method error = %v, want ErrNotImplemented", err)
			}
		})
	}
}

func TestEnumValuesAreStable(t *testing.T) {
	tests := map[string]string{
		"analysis prepared":      string(AnalysisContextOutcomePrepared),
		"suggestion generated":   string(SuggestionOutcomeGenerated),
		"freshness current":      string(SuggestionFreshnessCurrent),
		"basis matter set":       string(ReferenceBasisKindMatterSuggestionSet),
		"material required hint": string(MaterialRequiredHintRequired),
		"analysis purpose":       string(AnalysisPurposeMatterAnalysisOnly),
	}

	want := map[string]string{
		"analysis prepared":      "prepared",
		"suggestion generated":   "generated",
		"freshness current":      "current",
		"basis matter set":       "matter_suggestion_set",
		"material required hint": "required",
		"analysis purpose":       "matter_analysis_only",
	}

	for name, got := range tests {
		if got != want[name] {
			t.Fatalf("%s = %q, want %q", name, got, want[name])
		}
	}
}
