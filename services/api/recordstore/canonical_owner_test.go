package recordstore

import (
	"context"
	"testing"
)

func TestCanonicalSessionOwnerResolvesStoredOwner(t *testing.T) {
	source := &canonicalSessionOwnerSource{}
	reader := NewCanonicalSessionOwner(source)

	accountID, err := reader.AccountIDForSession(t.Context(), "session-1")
	if err != nil {
		t.Fatalf("AccountIDForSession() error = %v", err)
	}
	if accountID != "account-registered" {
		t.Fatalf("AccountIDForSession() = %q, want canonical account", accountID)
	}
	if source.canonicalInput != "account-anonymous" {
		t.Fatalf("CanonicalAccountID() input = %q, want stored session owner", source.canonicalInput)
	}
}

type canonicalSessionOwnerSource struct {
	canonicalInput string
}

func (*canonicalSessionOwnerSource) AccountIDForSession(context.Context, string) (string, error) {
	return "account-anonymous", nil
}

func (s *canonicalSessionOwnerSource) CanonicalAccountID(_ context.Context, accountID string) (string, error) {
	s.canonicalInput = accountID
	return "account-registered", nil
}
