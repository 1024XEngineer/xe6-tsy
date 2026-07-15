package matter

import (
	"errors"
	"testing"
)

func TestEnumValues(t *testing.T) {
	tests := []struct {
		name string
		got  string
		want string
	}{
		{"outcome generated", string(SuggestionOutcomeGenerated), "generated"},
		{"outcome no candidate", string(SuggestionOutcomeNoCandidate), "no_candidate"},
		{"outcome provider failed", string(SuggestionOutcomeProviderFailed), "provider_failed"},
		{"freshness current", string(SuggestionFreshnessCurrent), "current"},
		{"freshness stale", string(SuggestionFreshnessStale), "stale"},
		{"freshness expired", string(SuggestionFreshnessExpired), "expired"},
		{"evidence final", string(EvidenceKindFinal), "final"},
		{"evidence manual", string(EvidenceKindManual), "manual"},
		{"prepared event", EventMatterSuggestionSetPreparedV1, "matter.suggestion_set.prepared.v1"},
		{"stale event", EventMatterSuggestionSetStaleV1, "matter.suggestion_set.stale.v1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Fatalf("value = %q, want %q", tt.got, tt.want)
			}
		})
	}
}

func TestServiceReturnsNotImplemented(t *testing.T) {
	svc := NewService()
	cmd := minimalMatterCommand()

	_, err := svc.GenerateMatterSuggestions(t.Context(), cmd)
	if !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("GenerateMatterSuggestions() error = %v, want %v", err, ErrNotImplemented)
	}

	_, err = svc.MatterSuggestionSetByID(t.Context(), "set-1")
	if !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("MatterSuggestionSetByID() error = %v, want %v", err, ErrNotImplemented)
	}
}

func TestGenerateMatterSuggestionsCommandCanRepresentFinalAndManualEvidence(t *testing.T) {
	cmd := minimalMatterCommand()

	if len(cmd.EvidenceBlocks) != 2 {
		t.Fatalf("evidence block count = %d, want 2", len(cmd.EvidenceBlocks))
	}
	if cmd.EvidenceBlocks[0].Kind != EvidenceKindFinal {
		t.Fatalf("first evidence kind = %q, want %q", cmd.EvidenceBlocks[0].Kind, EvidenceKindFinal)
	}
	if cmd.EvidenceBlocks[0].SourceRefs[0].Kind != SourceKindTranscriptFinal {
		t.Fatalf("first source kind = %q, want %q", cmd.EvidenceBlocks[0].SourceRefs[0].Kind, SourceKindTranscriptFinal)
	}
	if cmd.EvidenceBlocks[1].Kind != EvidenceKindManual {
		t.Fatalf("second evidence kind = %q, want %q", cmd.EvidenceBlocks[1].Kind, EvidenceKindManual)
	}
	if cmd.EvidenceBlocks[1].SourceRefs[0].Kind != SourceKindTranscriptManual {
		t.Fatalf("second source kind = %q, want %q", cmd.EvidenceBlocks[1].SourceRefs[0].Kind, SourceKindTranscriptManual)
	}
}

func minimalMatterCommand() GenerateMatterSuggestionsCommand {
	return GenerateMatterSuggestionsCommand{
		SessionID:       "session-1",
		OrganizationID:  "org-1",
		ConfigVersionID: "config-1",
		Scope: ApplicableScope{
			OrganizationID: "org-1",
			RegionCodes:    []string{"330100"},
		},
		TaxonomyRef: MatterTaxonomyRef{
			TaxonomyID: "taxonomy-1",
			Version:    "v1",
		},
		TranscriptVersions: TranscriptVersionSet{
			Watermark: "wm-1",
			Digest:    "digest-1",
			Items: []TranscriptVersionRef{
				{SegmentID: "seg-1", Version: "final-v1"},
				{SegmentID: "seg-2", Version: "manual-v1"},
			},
		},
		EvidenceBlocks: []EvidenceBlock{
			{
				BlockID:      "block-1",
				SpeakerLabel: "citizen",
				Kind:         EvidenceKindFinal,
				Text:         "final transcript text",
				SourceRefs: []SourceRef{
					{
						Kind:     SourceKindTranscriptFinal,
						EntityID: "seg-1",
						Version:  "final-v1",
						TextSpan: TextSpan{Start: 0, End: 21},
					},
				},
			},
			{
				BlockID:      "block-2",
				SpeakerLabel: "staff",
				Kind:         EvidenceKindManual,
				Text:         "manual transcript text",
				SourceRefs: []SourceRef{
					{
						Kind:     SourceKindTranscriptManual,
						EntityID: "seg-2",
						Version:  "manual-v1",
						TextSpan: TextSpan{Start: 0, End: 22},
					},
				},
			},
		},
	}
}
