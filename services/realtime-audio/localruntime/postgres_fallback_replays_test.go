package localruntime

import "testing"

func TestPostgresFallbackReplayStoreRejectsInvalidClaims(t *testing.T) {
	store := PostgresFallbackPlaybackReplayStore{}
	if _, err := store.Claim(t.Context(), "", "operation-1", "hash"); err == nil {
		t.Fatal("Claim() succeeded with empty session ID")
	}
	if _, err := store.Claim(t.Context(), "session-1", "", "hash"); err == nil {
		t.Fatal("Claim() succeeded with empty operation ID")
	}
	if _, err := store.Claim(t.Context(), "session-1", "operation-1", ""); err == nil {
		t.Fatal("Claim() succeeded with empty payload hash")
	}
	if err := store.Complete(t.Context(), "", "operation-1", "hash"); err == nil {
		t.Fatal("Complete() succeeded with empty session ID")
	}
	if err := store.Complete(t.Context(), "session-1", "", "hash"); err == nil {
		t.Fatal("Complete() succeeded with empty operation ID")
	}
	if err := store.Complete(t.Context(), "session-1", "operation-1", ""); err == nil {
		t.Fatal("Complete() succeeded with empty payload hash")
	}
}
