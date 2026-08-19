package recordstore

import (
	"context"
	"errors"
	"testing"
)

func TestCanonicalSessionOwnerResolvesStoredOwner(t *testing.T) {
	source := &canonicalSessionOwnerSource{
		ownerID:     "account-anonymous",
		canonicalID: "account-registered",
	}
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

func TestCanonicalSessionOwnerPropagatesSourceErrors(t *testing.T) {
	ownerErr := errors.New("read session owner")
	canonicalErr := errors.New("resolve canonical owner")
	tests := []struct {
		name         string
		ownerErr     error
		canonicalErr error
		wantErr      error
		wantCalls    int
	}{
		{name: "stored owner", ownerErr: ownerErr, wantErr: ownerErr},
		{name: "canonical owner", canonicalErr: canonicalErr, wantErr: canonicalErr, wantCalls: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := &canonicalSessionOwnerSource{
				ownerID:      "account-anonymous",
				canonicalID:  "account-registered",
				ownerErr:     test.ownerErr,
				canonicalErr: test.canonicalErr,
			}
			_, err := NewCanonicalSessionOwner(source).AccountIDForSession(t.Context(), "session-1")
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("AccountIDForSession() error = %v, want %v", err, test.wantErr)
			}
			if source.canonicalCalls != test.wantCalls {
				t.Fatalf("CanonicalAccountID() calls = %d, want %d", source.canonicalCalls, test.wantCalls)
			}
		})
	}
}

type canonicalSessionOwnerSource struct {
	ownerID        string
	ownerErr       error
	canonicalID    string
	canonicalErr   error
	canonicalInput string
	canonicalCalls int
}

func (s *canonicalSessionOwnerSource) AccountIDForSession(context.Context, string) (string, error) {
	if s.ownerErr != nil {
		return "", s.ownerErr
	}
	return s.ownerID, nil
}

func (s *canonicalSessionOwnerSource) CanonicalAccountID(_ context.Context, accountID string) (string, error) {
	s.canonicalInput = accountID
	s.canonicalCalls++
	if s.canonicalErr != nil {
		return "", s.canonicalErr
	}
	return s.canonicalID, nil
}
