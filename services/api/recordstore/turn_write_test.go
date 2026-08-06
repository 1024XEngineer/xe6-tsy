package recordstore

import (
	"testing"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
)

func TestNeedsAsyncAttribution(t *testing.T) {
	tests := []struct {
		name   string
		status recordsv1.AttributionStatus
		want   bool
	}{
		{name: "pending", status: recordsv1.AttributionPending, want: true},
		{name: "provisional", status: recordsv1.AttributionProvisional, want: true},
		{name: "confirmed", status: recordsv1.AttributionConfirmed, want: false},
		{name: "corrected", status: recordsv1.AttributionCorrected, want: false},
		{name: "empty", status: "", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := needsAsyncAttribution(test.status); got != test.want {
				t.Fatalf("needsAsyncAttribution(%q) = %v, want %v", test.status, got, test.want)
			}
		})
	}
}

func TestShouldEnqueueAttributionRequiresProviderEvidence(t *testing.T) {
	providerSpeakerID := "cluster_01"
	blankProviderSpeakerID := "  "
	tests := []struct {
		name  string
		event recordsv1.FinalTurnEvent
		want  bool
	}{
		{
			name:  "pending with provider evidence",
			event: recordsv1.FinalTurnEvent{AttributionStatus: recordsv1.AttributionPending, ProviderSpeakerID: &providerSpeakerID},
			want:  true,
		},
		{
			name:  "pending without provider evidence",
			event: recordsv1.FinalTurnEvent{AttributionStatus: recordsv1.AttributionPending},
		},
		{
			name:  "pending with blank provider evidence",
			event: recordsv1.FinalTurnEvent{AttributionStatus: recordsv1.AttributionPending, ProviderSpeakerID: &blankProviderSpeakerID},
		},
		{
			name:  "confirmed with provider evidence",
			event: recordsv1.FinalTurnEvent{AttributionStatus: recordsv1.AttributionConfirmed, ProviderSpeakerID: &providerSpeakerID},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := shouldEnqueueAttribution(test.event); got != test.want {
				t.Fatalf("shouldEnqueueAttribution() = %t, want %t", got, test.want)
			}
		})
	}
}
