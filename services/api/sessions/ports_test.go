package sessions

import (
	"testing"
	"time"
)

func TestLanguageConfigStatusValidation(t *testing.T) {
	tests := []struct {
		name   string
		status LanguageConfigStatus
		want   bool
	}{
		{name: string(LanguageConfigActive), status: LanguageConfigActive, want: true},
		{name: string(LanguageConfigSuperseded), status: LanguageConfigSuperseded, want: true},
		{name: string(LanguageConfigExpired), status: LanguageConfigExpired, want: true},
		{name: "empty", status: "", want: false},
		{name: "inactive", status: "inactive", want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.status.Valid(); got != test.want {
				t.Fatalf("Valid() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestLanguageConfigSnapshotReady(t *testing.T) {
	tests := []struct {
		name     string
		snapshot LanguageConfigSnapshot
		want     bool
	}{
		{
			name:     "active two directions",
			snapshot: LanguageConfigSnapshot{Status: LanguageConfigActive, LanguagePairCount: 2},
			want:     true,
		},
		{
			name:     "active one direction",
			snapshot: LanguageConfigSnapshot{Status: LanguageConfigActive, LanguagePairCount: 1},
			want:     false,
		},
		{
			name:     "active extra direction",
			snapshot: LanguageConfigSnapshot{Status: LanguageConfigActive, LanguagePairCount: 3},
			want:     false,
		},
		{
			name:     "superseded",
			snapshot: LanguageConfigSnapshot{Status: LanguageConfigSuperseded, LanguagePairCount: 2},
			want:     false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.snapshot.Ready(); got != test.want {
				t.Fatalf("Ready() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestEndIntentReplayAndCompletion(t *testing.T) {
	intent := EndIntent{
		SessionID:      "vs_01TEST",
		AccountID:      "acct_01TEST",
		Reason:         EndReasonUserRequested,
		IdempotencyKey: "end-key",
		RequestHash:    "hash-a",
		RequestedAt:    time.Date(2026, 7, 27, 8, 0, 0, 0, time.UTC),
	}

	if !intent.MatchesRequest("end-key", "hash-a") {
		t.Fatal("same idempotency key and request hash should be a replay")
	}
	if intent.MatchesRequest("end-key", "hash-b") {
		t.Fatal("same idempotency key with a different request hash should conflict")
	}
	if intent.MatchesRequest("other-key", "hash-a") {
		t.Fatal("a different idempotency key should not be a replay")
	}
	if intent.Completed() {
		t.Fatal("new end intent should remain resumable")
	}

	completedAt := time.Date(2026, 7, 27, 8, 1, 0, 0, time.UTC)
	intent.CompletedAt = &completedAt
	if !intent.Completed() {
		t.Fatal("completed end intent should retain its audit state")
	}
}
